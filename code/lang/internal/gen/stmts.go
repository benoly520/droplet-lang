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
