// Package check 对语法树做名字解析与类型检查。
//
// 本文件由 check.go 同包拆分而来，职责：// 第二遍：检查函数体与语句。
package check

import (
	"droplet/internal/ast"
)

// ---------- 第二遍：检查函数体 ----------
// ---------- 第二遍：检查函数体 ----------

func (c *checker) checkBodies() {
	for _, it := range c.prog.Items {
		switch n := it.(type) {
		case *ast.FnDecl:
			c.checkFn(n)

		case *ast.EntryDecl:
			for _, fn := range n.Fns {
				c.checkFn(fn)
			}

		case *ast.HostDecl:
			for _, fn := range n.Fns {
				if fn.Body != nil {
					c.errorf(fn.Pos, "E1", "host 能力 %s 不能有函数体", fn.Name)
				}
			}

		case *ast.BoundaryDecl:
			c.checkBoundary(n)
		}
	}
}

// ---------- 函数体与语句 ----------
func (c *checker) checkFn(fn *ast.FnDecl) {
	sig := c.fns[fn.Name]
	if sig == nil {
		// host 能力由调用方单独查表，这里只处理普通函数与入口函数
		return
	}
	if fn.Body == nil {
		return
	}
	if sig.IsEntry && !hasIntent(fn.Intents) {
		c.errorf(fn.Pos, "E11", "entry 函数 %s 缺少 example 或 invariant", fn.Name)
	}

	prevScope, prevFn := c.scope, c.curFn
	c.scope = newScope(nil)
	for i, name := range sig.ParamNames {
		c.scope.define(name, sig.Params[i])
	}
	c.curFn = sig

	c.checkBlock(fn.Body)

	c.scope, c.curFn = prevScope, prevFn
}

func (c *checker) checkBlock(b *ast.Block) {
	if b == nil {
		return
	}
	prev := c.scope
	c.scope = newScope(prev)
	for _, s := range b.Stmts {
		c.checkStmt(s)
	}
	c.scope = prev
}

func (c *checker) checkStmt(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.Let:
		vt := c.checkExpr(n.Value)
		c.letTypes[n] = vt
		if n.Type != nil {
			decl := c.resolve(n.Type)
			if !assignable(vt, decl) {
				c.errorf(n.Pos, "E1", "变量 %s 声明为 %s，但初始值是 %s", n.Name, decl, vt)
			}
			c.scope.define(n.Name, decl)
			return
		}
		if vt.Kind == KUnion {
			c.errorf(n.Pos, "E1", "变量 %s 的值是联合类型 %s，请用 `!` 或 match 处理", n.Name, vt)
			c.scope.define(n.Name, tInval)
			return
		}
		c.scope.define(n.Name, vt)

	case *ast.Return:
		if n.Value == nil {
			if !isUnit(c.curFn.Ret) {
				c.errorf(n.Pos, "E1", "函数 %s 返回 %s，不能空 return", c.curFn.Name, c.curFn.Ret)
			}
			return
		}
		vt := c.checkExpr(n.Value)
		if vt.Kind == KUnit && c.curFn.Ret.Kind == KUnit {
			// `return Unit` 零信息：返回类型已经写着 Unit，函数体不需要再写一次。
			c.errorf(n.Pos, "E1",
				"`return Unit` 是多余的：返回 %s 的函数不写 return，删掉这一行即可", c.curFn.Ret)
			return
		}
		if !returnable(vt, c.curFn.Ret) {
			c.errorf(n.Pos, "E1", "返回 %s，但函数 %s 声明返回 %s", vt, c.curFn.Name, c.curFn.Ret)
		}

	case *ast.If:
		ct := c.checkExpr(n.Cond)
		if ct.Kind != KBool && ct.Kind != KInvalid {
			c.errorf(n.Pos, "E1", "if 条件必须是 bool，得到 %s", ct)
		}
		c.checkBlock(n.Then)
		if n.Else != nil {
			switch e := n.Else.(type) {
			case *ast.Block:
				c.checkBlock(e)
			case ast.Stmt:
				c.checkStmt(e)
			}
		}

	case *ast.Match:
		c.checkMatch(n)

	case *ast.ExprStmt:
		t := c.checkExpr(n.X)
		if t.Kind == KUnion {
			c.errorf(n.Pos, "E1", "返回值 %s 被静默丢弃：请用 `!` 传播或 match 处理", t)
		}
	}
}

func (c *checker) checkMatch(m *ast.Match) {
	st := c.checkExpr(m.Subject)
	hasWildcard := false
	covered := map[string]bool{}

	for _, arm := range m.Arms {
		prev := c.scope
		c.scope = newScope(prev)

		if arm.Pattern.Wildcard {
			if hasWildcard {
				c.errorf(arm.Pos, "E1", "match 出现多个 `_` 分支")
			}
			hasWildcard = true
		} else if st.Kind == KSum {
			v, ok := st.VariantsByName[arm.Pattern.Name]
			if !ok {
				c.errorf(arm.Pos, "E1", "%s 没有变体 %s", st.Name, arm.Pattern.Name)
			} else {
				if covered[v.Name] {
					c.errorf(arm.Pos, "E1", "变体 %s 被重复匹配", v.Name)
				}
				covered[v.Name] = true
				c.bindPattern(v, arm.Pattern)
			}
		} else if st.Kind != KInvalid {
			c.errorf(arm.Pos, "E1", "只有 sum 类型可以用变体匹配，%s 不是 sum", st)
		}

		switch b := arm.Body.(type) {
		case *ast.Block:
			c.checkBlock(b)
		case ast.Expr:
			c.checkExpr(b)
		}
		c.scope = prev
	}

	if st.Kind == KSum && !hasWildcard {
		for _, v := range st.Variants {
			if !covered[v.Name] {
				c.errorf(m.Pos, "E1", "match 未穷尽：缺少变体 %s", v.Name)
			}
		}
	}
}

// bindPattern 把变体字段绑进当前分支作用域。
func (c *checker) bindPattern(v *Variant, p ast.Pattern) {
	if len(p.Fields) != len(v.Fields) {
		c.errorf(p.Pos, "E1", "变体 %s 有 %d 个字段，模式绑定了 %d 个",
			v.Name, len(v.Fields), len(p.Fields))
		return
	}
	for i, name := range p.Fields {
		c.scope.define(name, v.Fields[i].Type)
	}
}
