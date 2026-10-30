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
		case *ast.EnvDecl:
			// env 块名就是一个 record：它的 struct 与字段由这里统一生成，
			// 不同之处只在构造点（只有生成的加载器造得出它）。
			names = append(names, n.Name)
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
				if len(v.Fields) == 0 {
					g.line("    struct { uint8_t _pad; } %s;", v.Name)
					continue
				}
				parts := make([]string, 0, len(v.Fields))
				for _, f := range v.Fields {
					parts = append(parts, fmt.Sprintf("%s m_%s;", cType(f.Type), f.Name))
				}
				g.line("    struct { %s } %s;", strings.Join(parts, " "), v.Name)
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
		// 入口现在也是普通的 FnDecl（带 Entry 标记），生成上没有区别：
		// 谁进 main 由 res.EntryPoint 决定。
		if n, ok := it.(*ast.FnDecl); ok {
			g.fn(n)
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
	switch g.cur.Ret.Kind {
	case check.KUnit:
		return "((dp_unit){0})"
	case check.KBytes:
		return "((dp_bytes){0})"
	}
	return "0"
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
