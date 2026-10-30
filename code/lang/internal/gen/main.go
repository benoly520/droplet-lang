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

	if idx := requestParamIndex(sig); idx >= 0 {
		g.serviceMain(sig, idx)
		return
	}
	g.cliMain(sig)
}

// requestParamIndex 找出 entry 里的 Request 参数。
// 有它 → 生成 HTTP 服务；没有 → 生成命令行调用（如 hello）。
func requestParamIndex(sig *check.FnSig) int {
	for i, p := range sig.Params {
		if p.Kind == check.KRecord && p.Name == "Request" {
			return i
		}
	}
	return -1
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
		g.failf(token.Pos{}, "Response %s 缺少 status 字段", respType.Name)
		return
	}

	g.line("static int dp_serve_handle(dp_arena* a, dp_str root, const dp_request* rq, dp_response* out) {")
	g.line("  (void)root;")

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

	// 其余参数：root 注入，其他类型给零值
	args := []string{"a"}
	rootUsed := false
	for i, p := range sig.Params {
		if p.Kind == check.KHost {
			continue
		}
		if i == reqIdx {
			args = append(args, "v_req")
			continue
		}
		if p.Kind == check.KStr && !rootUsed {
			args = append(args, "root")
			rootUsed = true
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
		case "etag":
			g.line("  out->etag = _r.u.ok.m_etag;")
		case "content_type":
			g.line("  out->content_type = _r.u.ok.m_content_type;")
		}
	}
	g.line("  return 0;")
	g.line("}")
	g.line("")
	g.line("int main(void) {")
	g.line("  dp_cfg cfg = dp_cfg_from_env();")
	g.line("  return dp_serve(dp_serve_handle, &cfg);")
	g.line("}")
}
