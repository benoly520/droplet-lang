// Package gen 把检查通过的语法树生成为 C11 源码。
//
// 本文件由 c.go 同包拆分而来，职责：表达式（构造、调用、能力、传播、运算）。
package gen

import (
	"fmt"
	"strings"

	"droplet/internal/ast"
	"droplet/internal/check"
	"droplet/internal/host"
	"droplet/internal/token"
)

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
		if t := g.res.ExprTypes[n]; t != nil && t.Kind == check.KSum {
			return g.variantCon(t, n.Field, nil)
		}
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
		if n.Variant != "" {
			return g.variantCon(t, n.Variant, n.Fields)
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

// variantCon 生成变体构造：`(dp_T_X){ .tag = N, .u.V = { .m_f = … } }`。
// 零字段变体只写 tag —— C99 的指定初始化会把余下字节清零。
func (g *gen) variantCon(t *check.Type, variant string, fields []ast.NamedExpr) string {
	tag, ok := g.res.Tags[variant]
	if !ok {
		g.failf(t.Pos, "变体 %s 没有分配 tag", variant)
		return "0"
	}
	if len(fields) == 0 {
		return fmt.Sprintf("((%s){ .tag = %d })", cType(t), tag)
	}
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		parts = append(parts, fmt.Sprintf(".m_%s = %s", f.Name, g.expr(f.Value)))
	}
	return fmt.Sprintf("((%s){ .tag = %d, .u.%s = { %s } })",
		cType(t), tag, variant, strings.Join(parts, ", "))
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
	if len(args) != hc.Arity() {
		g.failf(sel.Pos, "能力 %s 需要 %d 个参数，得到 %d", key, hc.Arity(), len(args))
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
	if hc.Wrap != nil {
		value = hc.Wrap(value)
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
