// Package gen 把检查通过的语法树生成为 C11 源码。
//
// 生成规则与 docs/IMPL-PLAN.md §5.2 对应，差异只允许记入文档后再改。
package gen

import (
	"fmt"
	"sort"
	"strings"

	"droplet/internal/ast"
	"droplet/internal/check"
	"droplet/internal/host"
	"droplet/internal/token"
)

type gen struct {
	prog *ast.Program
	res  *check.Result
	out  strings.Builder

	body  []string // 当前函数体的语句缓冲
	depth int
	tmps  int
	cur   *check.FnSig
	errs  []error
}

// C 生成完整 C 源码；出现生成期错误时返回 error。
func C(prog *ast.Program, res *check.Result) (string, error) {
	g := &gen{prog: prog, res: res}
	g.header()
	g.typeDecls()
	g.retTypes()
	g.protos()
	g.funcs()
	g.main()

	if len(g.errs) > 0 {
		var sb strings.Builder
		for _, e := range g.errs {
			sb.WriteString(e.Error())
			sb.WriteByte('\n')
		}
		return "", fmt.Errorf("%s", sb.String())
	}
	return g.out.String(), nil
}

// ---------- 顶层拼接 ----------

func (g *gen) header() {
	g.line("/* 由 droplet 生成，请勿手工编辑 */")
	g.line("#include \"rt.h\"")
	g.line("")
	g.line("#include <stdio.h>")
	g.line("#include <stdlib.h>")
	g.line("")
}

func (g *gen) typeDecls() {
	var names []string
	for _, it := range g.prog.Items {
		switch n := it.(type) {
		case *ast.RecordType:
			names = append(names, n.Name)
		case *ast.SumType:
			names = append(names, n.Name)
			for _, v := range n.Variants {
				if len(v.Fields) > 0 {
					g.failf(v.Pos, "M2 不支持带字段的错误/和类型变体 %s.%s", n.Name, v.Name)
				}
			}
		}
	}
	if len(names) == 0 {
		return
	}

	// 先全部前向声明，再逐个定义，避免定义顺序问题。
	for _, name := range names {
		g.line("typedef struct dp_T_%s dp_T_%s;", name, name)
	}
	g.line("")

	for _, name := range names {
		t := g.res.Types[name]
		if t == nil {
			continue
		}
		g.line("struct dp_T_%s {", name)
		switch t.Kind {
		case check.KRecord:
			if len(t.Fields) == 0 {
				g.line("  uint8_t _pad;")
			}
			for _, f := range t.Fields {
				g.line("  %s m_%s;", cType(f.Type), f.Name)
			}
		case check.KSum:
			g.line("  uint16_t tag;")
			g.line("  union {")
			if len(t.Variants) == 0 {
				g.line("    uint8_t _pad;")
			}
			for _, v := range t.Variants {
				g.line("    struct { uint8_t _pad; } %s;", v.Name)
			}
			g.line("  } u;")
		}
		g.line("};")
		g.line("")
	}
}

// retTypes 为每个返回联合的函数生成一个 R 结构。
func (g *gen) retTypes() {
	n := 0
	for _, name := range sortedFnNames(g.res) {
		sig := g.res.Sigs[name]
		if sig.Ret.Kind != check.KUnion {
			continue
		}
		g.line("typedef struct dp_R_%s {", name)
		g.line("  uint16_t tag;")
		g.line("  union {")
		g.line("    %s ok;", cType(sig.Ret.Items[0]))
		g.line("    dp_error err;")
		g.line("  } u;")
		g.line("} dp_R_%s;", name)
		n++
	}
	if n > 0 {
		g.line("")
	}
}

func (g *gen) protos() {
	for _, name := range sortedFnNames(g.res) {
		sig := g.res.Sigs[name]
		g.line("static %s dp_f_%s(%s);", g.fnRetType(sig), name, g.signature(sig))
	}
	if len(g.res.Sigs) > 0 {
		g.line("")
	}
}

func (g *gen) funcs() {
	for _, it := range g.prog.Items {
		switch n := it.(type) {
		case *ast.FnDecl:
			g.fn(n)
		case *ast.EntryDecl:
			for _, fn := range n.Fns {
				g.fn(fn)
			}
		}
	}
}

// ---------- 函数 ----------

func (g *gen) signature(sig *check.FnSig) string {
	parts := []string{"dp_arena* a"}
	for i, p := range sig.Params {
		if p.Kind == check.KHost {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s v_%s", cType(p), sig.ParamNames[i]))
	}
	return strings.Join(parts, ", ")
}

func (g *gen) fnRetType(sig *check.FnSig) string {
	if sig.Ret.Kind == check.KUnion {
		return "dp_R_" + sig.Name
	}
	return cType(sig.Ret)
}

func (g *gen) fn(fn *ast.FnDecl) {
	sig := g.res.Sigs[fn.Name]
	if sig == nil || fn.Body == nil {
		return
	}

	prev, prevDepth := g.cur, g.depth
	g.cur, g.depth = sig, 0
	g.body = nil

	g.stmtList(fn.Body.Stmts)
	// 只在控制流可能落下时补兜底 return，避免生成不可达代码。
	if !terminates(fn.Body) {
		g.emit("return %s;", g.zeroReturn())
	}

	g.line("static %s dp_f_%s(%s) {", g.fnRetType(sig), fn.Name, g.signature(sig))
	g.line("  (void)a;")
	for _, s := range g.body {
		g.line("%s", s)
	}
	g.line("}")
	g.line("")

	g.cur, g.depth = prev, prevDepth
}

// terminates 判断节点是否必定提前离开函数。
// 只做保守判断：不确定时返回 false，宁可多一条不可达 return。
func terminates(s ast.Node) bool {
	switch n := s.(type) {
	case *ast.Return:
		return true
	case *ast.Block:
		if len(n.Stmts) == 0 {
			return false
		}
		return terminates(n.Stmts[len(n.Stmts)-1])
	case *ast.If:
		if n.Else == nil {
			return false
		}
		return terminates(n.Then) && terminates(n.Else)
	case *ast.Match:
		if len(n.Arms) == 0 {
			return false
		}
		for _, arm := range n.Arms {
			if !terminates(arm.Body) {
				return false
			}
		}
		return true
	}
	return false
}

// zeroReturn 生成兜底返回值（源程序若所有分支都 return 则不会执行）。
func (g *gen) zeroReturn() string {
	if g.cur.Ret.Kind == check.KUnion {
		return fmt.Sprintf("(%s){0}", g.fnRetType(g.cur))
	}
	if g.cur.Ret.Kind == check.KUnit {
		return "((dp_unit){0})"
	}
	return "0"
}

// ---------- 语句 ----------

func (g *gen) stmtList(stmts []ast.Stmt) {
	for _, s := range stmts {
		g.stmt(s)
	}
}

func (g *gen) stmt(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.Let:
		val := g.expr(n.Value)
		t := g.res.LetTypes[n]
		if t == nil || t.Kind == check.KInvalid {
			t = g.zerosType(n.Value)
		}
		g.emit("%s v_%s = %s;", cType(t), n.Name, val)

	case *ast.Return:
		if n.Value == nil {
			if g.cur.Ret.Kind == check.KUnion {
				g.emit("return (%s){ .tag = 0, .u.ok = ((dp_unit){0}) };", g.fnRetType(g.cur))
			} else {
				g.emit("return ((dp_unit){0});")
			}
			return
		}
		val := g.expr(n.Value)
		if g.cur.Ret.Kind == check.KUnion {
			g.emit("return (%s){ .tag = 0, .u.ok = %s };", g.fnRetType(g.cur), val)
		} else {
			g.emit("return %s;", val)
		}

	case *ast.If:
		cond := g.expr(n.Cond)
		g.emit("if (%s) {", cond)
		g.nested(n.Then)
		if n.Else == nil {
			g.emit("}")
			return
		}
		g.emit("} else {")
		switch e := n.Else.(type) {
		case *ast.Block:
			g.nested(e)
		case ast.Stmt:
			g.depth++
			g.stmt(e)
			g.depth--
		}
		g.emit("}")

	case *ast.Match:
		g.matchStmt(n)

	case *ast.ExprStmt:
		if val := g.expr(n.X); val != "" {
			g.emit("(void)(%s);", val)
		}
	}
}

// nested 生成一个子块，自动增加缩进。
func (g *gen) nested(b *ast.Block) {
	g.depth++
	g.stmtList(b.Stmts)
	g.depth--
}

func (g *gen) matchStmt(n *ast.Match) {
	subj := g.expr(n.Subject)
	st := g.res.ExprTypes[n.Subject]
	if st == nil {
		g.failf(n.Pos, "无法确定 match 主体类型")
		return
	}

	tmp := g.newTmp()
	g.emit("%s %s = %s;", cType(st), tmp, subj)
	g.emit("switch (%s.tag) {", tmp)

	for _, arm := range n.Arms {
		g.depth++
		if arm.Pattern.Wildcard {
			g.emit("default: {")
		} else {
			tag := g.res.Tags[arm.Pattern.Name]
			g.emit("case %d: {", tag)
			if v := st.VariantsByName[arm.Pattern.Name]; v != nil {
				for i, f := range v.Fields {
					if i < len(arm.Pattern.Fields) {
						g.emit("%s v_%s = %s.u.%s.m_%s;",
							cType(f.Type), arm.Pattern.Fields[i], tmp, v.Name, f.Name)
					}
				}
			}
		}
		g.depth++
		switch b := arm.Body.(type) {
		case *ast.Block:
			g.stmtList(b.Stmts)
		case ast.Expr:
			g.emit("(void)(%s);", g.expr(b))
		}
		g.depth--
		g.emit("break;")
		g.emit("}")
		g.depth--
	}
	g.emit("}")
}

// ---------- 表达式 ----------

// expr 返回 C 表达式；需要前置语句时先写入 body。
func (g *gen) expr(e ast.Expr) string {
	switch n := e.(type) {
	case nil:
		return "0"

	case *ast.IntLit:
		return n.Value + "ull"

	case *ast.StrLit:
		return "dp_str_lit(" + cString(n.Value) + ")"

	case *ast.BytesLit:
		return "dp_bytes_lit(" + cString(string(n.Value)) + ")"

	case *ast.BoolLit:
		if n.Value {
			return "true"
		}
		return "false"

	case *ast.UnitLit:
		return "((dp_unit){0})"

	case *ast.Ident:
		return "v_" + n.Name

	case *ast.Select:
		if t := g.res.ExprTypes[n.Target]; t != nil && t.Kind == check.KHost {
			return "" // 能力引用由 call 处理，不单独求值
		}
		return g.expr(n.Target) + ".m_" + n.Field

	case *ast.Binary:
		return g.binary(n)

	case *ast.Construct:
		t := g.res.ExprTypes[n]
		if t == nil {
			g.failf(n.Pos, "无法确定构造 %s 的类型", n.TypeName)
			return "0"
		}
		parts := make([]string, 0, len(n.Fields))
		for _, f := range n.Fields {
			parts = append(parts, fmt.Sprintf(".m_%s = %s", f.Name, g.expr(f.Value)))
		}
		return fmt.Sprintf("((%s){ %s })", cType(t), strings.Join(parts, ", "))

	case *ast.Call:
		return g.call(n)

	case *ast.Propagate:
		return g.propagate(n)
	}

	g.failf(token.Pos{}, "M2 未支持的表达式 %T", e)
	return "0"
}

func (g *gen) binary(n *ast.Binary) string {
	l, r := g.expr(n.Left), g.expr(n.Right)

	if n.Op == "==" || n.Op == "!=" {
		fn := "dp_val_eq"
		if t := g.res.ExprTypes[n.Left]; t != nil {
			switch t.Kind {
			case check.KStr:
				fn = "dp_str_eq"
			case check.KBytes:
				fn = "dp_bytes_eq"
			}
		}
		if n.Op == "==" {
			return fmt.Sprintf("%s(%s, %s)", fn, l, r)
		}
		return fmt.Sprintf("!%s(%s, %s)", fn, l, r)
	}
	return fmt.Sprintf("(%s %s %s)", l, n.Op, r)
}

func (g *gen) call(n *ast.Call) string {
	if sel, ok := n.Callee.(*ast.Select); ok {
		return g.hostCall(sel, n.Args)
	}
	id, ok := n.Callee.(*ast.Ident)
	if !ok {
		g.failf(n.Pos, "M2 只支持具名函数调用")
		return "0"
	}
	sig := g.res.Sigs[id.Name]
	if sig == nil {
		g.failf(n.Pos, "代码生成时找不到函数 %s", id.Name)
		return "0"
	}

	args := []string{"a"}
	for _, a := range n.Args {
		args = append(args, g.expr(a))
	}
	joined := strings.Join(args, ", ")

	if sig.Ret.Kind != check.KUnion {
		return fmt.Sprintf("dp_f_%s(%s)", id.Name, joined)
	}
	tmp := g.newTmp()
	g.emit("%s %s = dp_f_%s(%s);", "dp_R_"+id.Name, tmp, id.Name, joined)
	return tmp
}

// hostWraps 把运行时返回值转成语言层面的类型（如 u64 → ETag）。
var hostWraps = map[string]func(expr string, okType *check.Type) string{
	"BlobFs.write": wrapETag,
}

// wrapETag 把 dp_blob_write 的 u64 包成契约里的 ETag。
func wrapETag(expr string, okType *check.Type) string {
	if okType != nil && okType.Kind == check.KRecord && okType.Name == "ETag" {
		return "((dp_T_ETag){ .m_hash = " + expr + " })"
	}
	return expr
}

// hostCall 把能力调用内联成运行时函数，参数顺序按运行时原型调整。
//
// 联合返回的能力不直接给出值，而是包成一个临时的 tag/err 结构，
// 形状和 `!` 的期望完全一致，后续传播只需复制 tag。
func (g *gen) hostCall(sel *ast.Select, args []ast.Expr) string {
	ht := g.res.ExprTypes[sel.Target]
	if ht == nil || ht.Kind != check.KHost {
		g.failf(sel.Pos, "代码生成时 %s 不是 host", sel.Field)
		return "0"
	}
	key := ht.Name + "." + sel.Field

	// 能力表与 check 共用一处（internal/host）：这里拿不到表项，说明
	// checker 放行了运行时并不提供的能力——防御性兜底，正常路径下 check 已拦下。
	hc, ok := host.Lookup(key)
	if !ok {
		g.failf(sel.Pos, "运行时不提供能力 %s", key)
		return "0"
	}
	if len(args) != hc.Arity {
		g.failf(sel.Pos, "能力 %s 需要 %d 个参数，得到 %d", key, hc.Arity, len(args))
		return "0"
	}

	argv := make([]string, 0, len(args))
	for _, a := range args {
		argv = append(argv, g.expr(a))
	}

	sig := ht.Methods[sel.Field]
	if sig == nil || sig.Ret == nil || sig.Ret.Kind != check.KUnion {
		return hc.Emit(argv, "")
	}

	okType := sig.Ret.Items[0]
	errTmp := g.newTmp()
	tmp := g.newTmp()
	value := hc.Emit(argv, errTmp)
	if wrap, ok := hostWraps[key]; ok {
		value = wrap(value, okType)
	}
	g.emit("struct { uint16_t tag; union { %s ok; dp_error err; } u; } %s = {0};", cType(okType), tmp)
	g.emit("{ int %s = 0; %s.u.ok = %s; if (%s != 0) { %s.tag = (uint16_t)%s; %s.u.err.tag = (uint16_t)%s; } }",
		errTmp, tmp, value, errTmp, tmp, errTmp, tmp, errTmp)
	return tmp
}

// propagate 实现 `e!`：错误直接返回，成功值继续参与运算。
func (g *gen) propagate(n *ast.Propagate) string {
	inner := g.expr(n.Inner)
	if g.cur == nil {
		g.failf(n.Pos, "`!` 出现在函数体之外")
		return "0"
	}
	tmp := g.newTmp()
	g.emit("if (%s.tag != 0) {", inner)
	g.depth++
	g.emit("%s %s = {0};", g.fnRetType(g.cur), tmp)
	g.emit("%s.tag = %s.tag;", tmp, inner)
	g.emit("%s.u.err = %s.u.err;", tmp, inner)
	g.emit("return %s;", tmp)
	g.depth--
	g.emit("}")
	return inner + ".u.ok"
}

// zerosType 在推导失败时给出占位类型，避免生成非法 C。
func (g *gen) zerosType(e ast.Expr) *check.Type {
	return &check.Type{Kind: check.KUnit, Name: "Unit"}
}

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

// ---------- 辅助 ----------

func (g *gen) line(format string, a ...any) {
	g.out.WriteString(fmt.Sprintf(format, a...))
	g.out.WriteByte('\n')
}

func (g *gen) emit(format string, a ...any) {
	indent := strings.Repeat("  ", g.depth+1)
	g.body = append(g.body, indent+fmt.Sprintf(format, a...))
}

func (g *gen) newTmp() string {
	g.tmps++
	return fmt.Sprintf("_t%d", g.tmps)
}

func (g *gen) failf(pos token.Pos, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	if pos.Line > 0 {
		g.errs = append(g.errs, fmt.Errorf("%s: E-GEN: %s", pos, msg))
		return
	}
	g.errs = append(g.errs, fmt.Errorf("E-GEN: %s", msg))
}

// cType 把检查期类型映射为 C 类型。
func cType(t *check.Type) string {
	if t == nil {
		return "dp_unit"
	}
	switch t.Kind {
	case check.KStr:
		return "dp_str"
	case check.KBytes:
		return "dp_bytes"
	case check.KU64:
		return "uint64_t"
	case check.KI64:
		return "int64_t"
	case check.KBool:
		return "bool"
	case check.KUnit:
		return "dp_unit"
	case check.KRecord, check.KSum:
		return "dp_T_" + t.Name
	}
	return "dp_unit"
}

// cString 输出 C 字符串字面量；非可打印字节用三位八进制转义，避免与后续数字粘连。
func cString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b.WriteString("\\\"")
		case '\\':
			b.WriteString("\\\\")
		case '\n':
			b.WriteString("\\n")
		case '\t':
			b.WriteString("\\t")
		case '\r':
			b.WriteString("\\r")
		default:
			if c < 0x20 || c >= 0x7f {
				b.WriteString(fmt.Sprintf("\\%03o", c))
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func sortedFnNames(res *check.Result) []string {
	names := make([]string, 0, len(res.Sigs))
	for name := range res.Sigs {
		names = append(names, name)
	}
	// 稳定输出：按名字排序，保证同一程序生成同一份 C。
	sort.Strings(names)
	return names
}
