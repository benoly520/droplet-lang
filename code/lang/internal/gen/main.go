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

	if sig.IsService {
		g.serviceMain(sig, sig.ReqIdx)
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

// Request 字段 → dp_request 字段。不在表里的字段无法从 HTTP 请求填充。
var requestFields = map[string]string{
	"method":        "rq->method",
	"path":          "rq->path",
	"key":           "rq->key",
	"query":         "rq->query",
	"authorization": "rq->authorization",
	"if_match":      "rq->if_match",
	"body":          "rq->body",
}

// serviceMain 生成 HTTP 服务形态：entry 函数成为请求处理器，main 交给 dp_serve。
func (g *gen) serviceMain(sig *check.FnSig, reqIdx int) {
	reqType := sig.Params[reqIdx]
	if sig.Ret == nil || sig.Ret.Kind != check.KUnion {
		g.failf(token.Pos{}, "entry %s 用于服务时必须返回 Response | 错误", sig.Name)
		return
	}
	respType := sig.Ret.Items[0]
	if respType.Kind != check.KRecord {
		g.failf(token.Pos{}, "entry %s 的成功返回值必须是 record，得到 %s", sig.Name, respType.Name)
		return
	}
	if respType.FieldsByName["status"] == nil {
		g.failf(token.Pos{}, "响应类型 %s 缺少 status 字段：运行时只按 status 映射状态码", respType.Name)
		return
	}

	// 配置先于处理器：静态 g_cfg 与加载器要出现在使用它之前，C 没有提升声明。
	g.cfgLoader()
	g.line("static int dp_serve_handle(dp_arena* a, const dp_request* rq, dp_response* out) {")

	// 从 dp_request 构造 Request
	g.line("  dp_T_%s v_req = {0};", reqType.Name)
	for _, f := range reqType.Fields {
		src, ok := requestFields[f.Name]
		if !ok {
			g.failf(token.Pos{}, "Request.%s 无法从 HTTP 请求填充", f.Name)
			continue
		}
		g.line("  v_req.m_%s = %s;", f.Name, src)
	}

	// 其余参数：host 能力由运行时提供（生成期跳过），env 类型注入加载好的配置，
	// 其他形状在 checker 里已被拦住（见 check/env.go 的绑定规则）。
	args := []string{"a"}
	for i, p := range sig.Params {
		if p.Kind == check.KHost {
			continue
		}
		if i == reqIdx {
			args = append(args, "v_req")
			continue
		}
		if p.IsEnv {
			args = append(args, "g_cfg")
			continue
		}
		name := "v_" + sig.ParamNames[i]
		g.line("  %s %s = {0};", cType(p), name)
		args = append(args, name)
	}

	g.line("  dp_R_%s _r = dp_f_%s(%s);", sig.Name, sig.Name, strings.Join(args, ", "))
	g.line("  if (_r.tag != 0) { return (int)_r.tag; }")
	g.line("  out->content_type = dp_str_lit(\"application/json\");")
	for _, f := range respType.Fields {
		switch f.Name {
		case "status":
			g.line("  out->status = (uint16_t)_r.u.ok.m_status;")
		case "body":
			g.line("  out->body = _r.u.ok.m_body;")
		case "content_type":
			g.line("  out->content_type = _r.u.ok.m_content_type;")
		case "etag":
			// v0.10 的第四条语法减法：ETag 由运行时从 body 推导（见 rt_http.c）。
			g.failf(token.Pos{}, "Response.etag 不用写：ETag 头由运行时从 body 推导（SPEC §2.2 H7），删掉这个字段")
		default:
			g.failf(token.Pos{}, "Response.%s 无法映射到响应：可写的只有 status / body / content_type", f.Name)
		}
	}
	g.line("  return 0;")
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
