// Package gen 把检查通过的语法树生成为 C11 源码。
//
// 本文件由 c.go 同包拆分而来，职责：语句（let/return/if/match）。
package gen

import (
	"droplet/internal/ast"
	"droplet/internal/check"
)

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
				g.emit("return (%s){ .tag = 0, .u.ok = %s };", g.fnRetType(g.cur), okUnitLit(g.cur.Ret))
			} else {
				g.emit("return %s;", okUnitLit(g.cur.Ret))
			}
			return
		}
		val := g.expr(n.Value)
		if g.cur.Ret.Kind == check.KUnion {
			if isErrItem(g.cur.Ret, g.res.ExprTypes[n.Value]) {
				// 直接构造错误变体（D2：`return ApiError.NotAllowed`）：值本身的
				// `.tag` 就是运行时认的错误号，搬进联合的 tag 即可。
				g.emit("return (%s){ .tag = (%s).tag };", g.fnRetType(g.cur), val)
			} else {
				g.emit("return (%s){ .tag = 0, .u.ok = %s };", g.fnRetType(g.cur), val)
			}
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

// isErrItem 判断 t 是不是 ret 这个联合里的错误成员（第 1 项及之后）。
// 错误项是 sum：它的变体 tag 就是运行时要的状态码编号。
func isErrItem(ret, t *check.Type) bool {
	if ret == nil || ret.Kind != check.KUnion || t == nil || t.Kind != check.KSum {
		return false
	}
	for _, it := range ret.Items[1:] {
		if it.Kind == check.KSum && it.Name == t.Name {
			return true
		}
	}
	return false
}

// okUnitLit 是裸 `return` 的单位值（D2 / R3）：Unit 是「没有值」，bytes 是空 body。
// 联合按成功成员取类型：`.u.ok` 的 C 类型就是它。
func okUnitLit(t *check.Type) string {
	if t != nil && t.Kind == check.KUnion {
		t = t.Items[0]
	}
	if t != nil && t.Kind == check.KBytes {
		return "((dp_bytes){0})"
	}
	return "((dp_unit){0})"
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
