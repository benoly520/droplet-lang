// Package check 对语法树做名字解析与类型检查。
//
// 本文件由 check.go 同包拆分而来，职责：// 表达式类型推导。
package check

import (
	"droplet/internal/ast"
)

// ---------- 表达式 ----------
// ---------- 表达式 ----------

// checkExpr 推导表达式类型，并把结果记入 exprTypes 供 codegen 使用。
func (c *checker) checkExpr(e ast.Expr) *Type {
	t := c.checkExprNode(e)
	if e != nil {
		c.exprTypes[e] = t
	}
	return t
}

func (c *checker) checkExprNode(e ast.Expr) *Type {
	switch n := e.(type) {
	case nil:
		return tInval

	case *ast.IntLit:
		return tU64
	case *ast.StrLit:
		return tStr
	case *ast.BytesLit:
		return tBytes
	case *ast.BoolLit:
		return tBool
	case *ast.UnitLit:
		return tUnit

	case *ast.Ident:
		if t := c.scope.lookup(n.Name); t != nil {
			return t
		}
		if _, ok := c.fns[n.Name]; ok {
			c.errorf(n.Pos, "E1", "函数 %s 不能当作值使用，需要调用它", n.Name)
			return tInval
		}
		c.errorf(n.Pos, "E1", "未定义标识符 %s", n.Name)
		return tInval

	case *ast.Select:
		tt := c.checkExpr(n.Target)
		switch tt.Kind {
		case KRecord:
			f, ok := tt.FieldsByName[n.Field]
			if !ok {
				c.errorf(n.Pos, "E1", "%s 没有字段 %s", tt.Name, n.Field)
				return tInval
			}
			return f.Type
		case KHost:
			sig, ok := tt.Methods[n.Field]
			if !ok {
				c.errorf(n.Pos, "E1", "host %s 没有能力 %s", tt.Name, n.Field)
				return tInval
			}
			return &Type{Kind: KHostMethod, Name: tt.Name + "." + n.Field, Method: sig}
		case KInvalid:
			return tInval
		default:
			c.errorf(n.Pos, "E1", "%s 不是 record 或 host，不能用 .%s", tt, n.Field)
			return tInval
		}

	case *ast.Call:
		return c.checkCall(n)

	case *ast.Construct:
		return c.checkConstruct(n)

	case *ast.Propagate:
		return c.checkPropagate(n)

	case *ast.Binary:
		return c.checkBinary(n)
	}

	return tInval
}

// ---------- 构造、传播与运算 ----------
func (c *checker) checkConstruct(n *ast.Construct) *Type {
	t := c.resolveNamed(n.TypeName, n.Pos)
	if t.Kind == KInvalid {
		for _, f := range n.Fields {
			c.checkExpr(f.Value)
		}
		return tInval
	}
	if t.Kind != KRecord {
		c.errorf(n.Pos, "E1", "%s 不是 record，不能用字面量构造", t.Name)
		for _, f := range n.Fields {
			c.checkExpr(f.Value)
		}
		return tInval
	}

	seen := map[string]bool{}
	for _, f := range n.Fields {
		vt := c.checkExpr(f.Value)
		fd, ok := t.FieldsByName[f.Name]
		if !ok {
			c.errorf(f.Pos, "E1", "%s 没有字段 %s", t.Name, f.Name)
			continue
		}
		if seen[f.Name] {
			c.errorf(f.Pos, "E1", "字段 %s 重复赋值", f.Name)
		}
		seen[f.Name] = true
		if !assignable(vt, fd.Type) {
			c.errorf(f.Pos, "E1", "%s.%s 应为 %s，得到 %s", t.Name, f.Name, fd.Type, vt)
		}
	}
	for _, fd := range t.Fields {
		if !seen[fd.Name] {
			c.errorf(n.Pos, "E1", "构造 %s 缺少字段 %s", t.Name, fd.Name)
		}
	}
	return t
}

// checkPropagate 实现 C1：`!` 只能用于联合返回，且错误必须已被声明。
func (c *checker) checkPropagate(n *ast.Propagate) *Type {
	it := c.checkExpr(n.Inner)
	if it.Kind == KInvalid {
		return tInval
	}
	if it.Kind != KUnion {
		c.errorf(n.Pos, "E1", "`!` 只能用于联合返回，%s 不是联合", it)
		return it
	}

	success := it.Items[0]
	errs := it.Items[1:]

	ret := c.curFn.Ret
	if ret.Kind != KUnion {
		c.errorf(n.Pos, "E1", "`!` 所在函数 %s 返回 %s，不是联合，无法传播错误",
			c.curFn.Name, ret)
		return success
	}
	for _, e := range errs {
		if !containsType(ret.Items[1:], e) {
			c.errorf(n.Pos, "E1", "函数 %s 的返回类型 %s 缺少错误 %s，无法用 `!` 传播",
				c.curFn.Name, ret, e)
			continue
		}
		c.markError(e)
	}
	return success
}

func (c *checker) checkBinary(n *ast.Binary) *Type {
	lt := c.checkExpr(n.Left)
	rt := c.checkExpr(n.Right)

	if lt.Kind == KUnion || rt.Kind == KUnion {
		c.errorf(n.Pos, "E1", "二元运算的操作数不能是联合类型，请先用 `!` 取值")
		return tInval
	}
	if lt.Kind == KInvalid || rt.Kind == KInvalid {
		return tInval
	}

	switch n.Op {
	case "==", "!=":
		if !comparable(lt) {
			c.errorf(n.Pos, "E1", "%s 不能用 %s 比较", lt, n.Op)
			return tInval
		}
		if !sameType(lt, rt) {
			c.errorf(n.Pos, "E1", "比较两侧类型不同：%s 与 %s", lt, rt)
		}
		return tBool

	case "<", "<=", ">", ">=":
		if !ordered(lt) {
			c.errorf(n.Pos, "E1", "%s 不支持顺序比较", lt)
			return tInval
		}
		if !sameType(lt, rt) {
			c.errorf(n.Pos, "E1", "比较两侧类型不同：%s 与 %s", lt, rt)
		}
		return tBool

	case "&&", "||":
		if lt.Kind != KBool || rt.Kind != KBool {
			c.errorf(n.Pos, "E1", "%s 两侧必须是 bool，得到 %s 与 %s", n.Op, lt, rt)
		}
		return tBool

	case "+", "-", "*", "/", "%":
		if !numeric(lt) || !numeric(rt) {
			c.errorf(n.Pos, "E1", "%s 只支持数值类型，得到 %s 与 %s", n.Op, lt, rt)
			return tInval
		}
		if !sameType(lt, rt) {
			c.errorf(n.Pos, "E1", "算术两侧类型不同：%s 与 %s", lt, rt)
			return tInval
		}
		return lt
	}

	c.errorf(n.Pos, "E1", "未支持的运算符 %s", n.Op)
	return tInval
}
