// Package gen 把检查通过的语法树生成为 C11 源码。
//
// 本文件由 c.go 同包拆分而来，职责：两种入口产物（CLI main 与 HTTP 服务 main）。
package gen

import (
	"fmt"
	"strings"

	"droplet/internal/check"
	"droplet/internal/token"
)

// ---------- main ----------

func (g *gen) main() {
	// 服务形态由 boundary 的路由表定义（D3）：有路由就没有 entry，反之亦然。
	if len(g.res.Routes) > 0 {
		g.serviceMain()
		return
	}
	sig := g.res.Sigs[g.res.EntryPoint]
	if sig == nil {
		// 无入口的程序（如只有契约与工具函数）：仍引用全部函数，
		// 避免 -Wunused-function 在 -Werror 下把构建卡住。
		g.line("int main(void) {")
		for _, name := range sortedFnNames(g.res) {
			g.line("  (void)&dp_f_%s;", name)
		}
		g.line("  return 0;")
		g.line("}")
		return
	}
	g.cliMain(sig)
}

// cliMain 生成命令行形态：参数按位置取 argv，缺省用类型零值。
func (g *gen) cliMain(sig *check.FnSig) {
	g.line("int main(int argc, char** argv) {")
	g.line("  (void)argc;")
	g.line("  (void)argv;")
	g.line("  dp_arena* a = dp_arena_new(1u << 20);")
	g.line("  if (!a) { return 2; }")

	args := []string{"a"}
	argIdx := 1
	for i, p := range sig.Params {
		if p.Kind == check.KHost {
			continue
		}
		name := "v_" + sig.ParamNames[i]
		switch p.Kind {
		case check.KStr:
			g.line("  dp_str %s = argc > %d ? dp_str_from_cstr(argv[%d]) : dp_str_lit(\"\");", name, argIdx, argIdx)
		case check.KU64:
			g.line("  uint64_t %s = argc > %d ? (uint64_t)strtoull(argv[%d], 0, 10) : 0ull;", name, argIdx, argIdx)
		case check.KI64:
			g.line("  int64_t %s = argc > %d ? (int64_t)strtoll(argv[%d], 0, 10) : 0ll;", name, argIdx, argIdx)
		default:
			g.line("  %s %s = {0};", cType(p), name)
		}
		args = append(args, name)
		argIdx++
	}

	call := fmt.Sprintf("dp_f_%s(%s)", sig.Name, strings.Join(args, ", "))
	if sig.Ret.Kind == check.KUnion {
		g.line("  dp_R_%s r = %s;", sig.Name, call)
		g.line("  if (r.tag != 0) {")
		g.line("    fprintf(stderr, \"droplet: 入口返回错误 tag=%%u\\n\", (unsigned)r.tag);")
		g.line("    dp_arena_free(a);")
		g.line("    return 1;")
		g.line("  }")
	} else {
		g.line("  %s;", call)
	}

	g.line("  dp_arena_free(a);")
	g.line("  return 0;")
	g.line("}")
}

// serviceMain 生成 HTTP 服务形态：入口是 boundary 合成的路由表（D3 / IMPL-PLAN §8.5）。
//
// 每个方法一条 `if (method == X && path == Y)`，守卫在调用实现之前跑：
// 条件请求（with precondition）由生成的守卫调运行时那一处实现（dp_precondition_check），
// handler 不参与；成功恒 200，状态码仍只在错误变体 → 状态码的表里出现（D2 的 R1/R2）。
func (g *gen) serviceMain() {
	// 配置先于处理器：静态 g_cfg 与加载器要出现在使用它之前，C 没有提升声明。
	g.cfgLoader()
	g.line("static int dp_serve_handle(dp_arena* a, const dp_request* rq, dp_response* out) {")
	for _, r := range g.res.Routes {
		g.line("  if (dp_str_eq(rq->method, dp_str_lit(\"%s\")) && dp_str_eq(rq->path, dp_str_lit(\"%s\"))) {",
			r.Verb, r.Path)
		if r.Precondition {
			// 预条件是「进 handler 之前」的事实：与 key 合法性同级，取消它要改声明。
			g.line("    int _pc = dp_precondition_check(a, g_cfg.m_root, rq->key, rq->if_match);")
			g.line("    if (_pc != 0) { return _pc; }")
		}
		sig := g.res.Sigs[r.Fn]
		if sig == nil {
			g.failf(token.Pos{}, "路由 %s %s 的实现 %s 不存在", r.Verb, r.Path, r.Fn)
			return
		}
		args := []string{"a"}
		for i, p := range sig.Params {
			switch {
			case i < len(r.Slots):
				switch r.Slots[i] {
				case "key":
					args = append(args, "rq->key")
				case "body":
					args = append(args, "rq->body")
				default:
					g.failf(token.Pos{}, "boundary 参数 %s 没有生成期绑定规则", r.Slots[i])
					return
				}
			case p.Kind == check.KHost:
				// 能力不传值：调用点直接内联到 dp_blob_* / dp_ver_*（见 §5.3）。
			case p.IsEnv:
				args = append(args, "g_cfg")
			default:
				g.failf(token.Pos{}, "实现 %s 的参数 %s 无法绑定", r.Fn, sig.ParamNames[i])
				return
			}
		}
		g.line("    dp_R_%s _r = dp_f_%s(%s);", r.Fn, r.Fn, strings.Join(args, ", "))
		g.line("    if (_r.tag != 0) { return (int)_r.tag; }")
		g.line("    out->status = 200;")
		g.line("    out->content_type = dp_str_lit(\"application/json\");")
		if r.Ret != nil && r.Ret.Kind == check.KBytes {
			// Unit 成功 = 200 + Content-Length: 0：out->body 已是零值。
			g.line("    out->body = _r.u.ok;")
		}
		g.line("    return 0;")
		g.line("  }")
	}
	// 没有命中任何路由：405。这条兜底以前写在 handler 的 if-chain 末尾，
	// 现在长在生成的 dispatcher 里 —— 它不属于任何一条路由，就不该由某条路由实现。
	g.line("  return (int)DP_ERR_NOT_ALLOWED;")
	g.line("}")
	g.line("")
	g.line("int main(void) {")
	g.line("  if (dp_cfg_load() != 0) { return 1; }")
	g.line("  dp_cfg cfg = {0};")
	// 五个底座键到 dp_cfg 的映射：类型转换处就是范围校验处，
	// 以前那些「port=0 就回退 8787」的静默降级已全部删掉。
	g.line("  if (g_cfg.m_port == 0ull || g_cfg.m_port > 65535ull) {")
	g.line("    fprintf(stderr, \"droplet: 配置 port=%%llu 不在 1..65535\\n\", (unsigned long long)g_cfg.m_port);")
	g.line("    return 1;")
	g.line("  }")
	g.line("  cfg.port = (uint16_t)g_cfg.m_port;")
	g.line("  cfg.bind = g_cfg.m_bind;")
	g.line("  cfg.root = g_cfg.m_root;")
	g.line("  cfg.token = g_cfg.m_token;")
	g.line("  if (g_cfg.m_max_body == 0ull) {")
	g.line("    fprintf(stderr, \"droplet: 配置 max_body 必须大于 0\\n\");")
	g.line("    return 1;")
	g.line("  }")
	g.line("  cfg.max_body = (size_t)g_cfg.m_max_body;")
	g.line("  return dp_serve(dp_serve_handle, &cfg);")
	g.line("}")
}

// cfgLoader 按 env 声明生成配置加载器：每个键一段 getenv，有默认值的用声明里的
// 默认值，没默认值的缺失即失败退出（SPEC §5.7 的「缺键就起不来」在 D1 落在启动瞬间）。
// 环境变量名由块名推出（APP_ROOT），源码里不出现它；u64 键非数字同样直接失败，
// 不回退——一个把 8080x 默默当默认端口起来的服务比一个不起的服务更难查。
func (g *gen) cfgLoader() {
	env := g.res.Env
	if env == nil {
		g.failf(token.Pos{}, "服务形态缺少 env 声明：这一步应由 checker 拦下")
		return
	}
	g.line("static dp_T_%s g_cfg;", env.Name)
	g.line("static int dp_cfg_load(void) {")
	for _, k := range env.Keys {
		name := check.EnvVar(env.Prefix, k.Name)
		g.line("  {")
		g.line("    const char* v = getenv(%q);", name)
		switch k.Type.Kind {
		case check.KU64:
			g.line("    if (v != NULL && *v != '\\0') {")
			g.line("      char* end = NULL;")
			g.line("      unsigned long long x = strtoull(v, &end, 10);")
			g.line("      if (end == v || end == NULL || *end != '\\0') {")
			g.line("        fprintf(stderr, \"droplet: 配置 %s=%%s 不是整数\\n\", v);", name)
			g.line("        return 1;")
			g.line("      }")
			g.line("      g_cfg.m_%s = (uint64_t)x;", k.Name)
			g.line("    } else {")
			if !k.HasDefault {
				g.missingKey(name, env, k)
			} else {
				g.line("      g_cfg.m_%s = %sull; /* 声明里的默认值 */", k.Name, k.Default)
			}
			g.line("    }")
		case check.KStr:
			if k.HasDefault {
				g.line("    g_cfg.m_%s = (v != NULL && *v != '\\0') ? dp_str_from_cstr(v) : dp_str_lit(%s);",
					k.Name, cString(k.Default))
			} else {
				g.line("    if (v == NULL || *v == '\\0') {")
				g.missingKey(name, env, k)
				g.line("    }")
				g.line("    g_cfg.m_%s = dp_str_from_cstr(v);", k.Name)
			}
		}
		g.line("  }")
	}
	g.line("  return 0;")
	g.line("}")
	g.line("")
}

// missingKey 生成「必填键缺失就退出」那段：报错里要说出环境变量名、块名与键的类型。
func (g *gen) missingKey(name string, env *check.EnvSpec, k check.EnvKey) {
	g.line("      fprintf(stderr, \"droplet: 缺少配置 %s（env %s 的 %s: %s 没有默认值）\\n\");",
		name, env.Name, k.Name, k.Type.Name)
	g.line("      return 1;")
}
