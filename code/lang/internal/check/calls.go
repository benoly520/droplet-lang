// Package check 对语法树做名字解析与类型检查。
//
// 本文件由 check.go 同包拆分而来，职责：// 调用与实参检查。
package check

import (
	"droplet/internal/ast"
	"droplet/internal/token"
)

// ---------- 调用与实参 ----------
func (c *checker) checkCall(n *ast.Call) *Type {
	var sig *FnSig

	switch callee := n.Callee.(type) {
	case *ast.Ident:
		sig = c.fns[callee.Name]
		if sig == nil {
			c.errorf(callee.Pos, "E1", "未定义函数 %s", callee.Name)
			c.checkArgs(n.Pos, n.Args, nil)
			return tInval
		}

	case *ast.Select:
		ht := c.checkExpr(callee.Target)
		if ht.Kind == KInvalid {
			c.checkArgs(n.Pos, n.Args, nil)
			return tInval
		}
		if ht.Kind != KHost {
			c.errorf(callee.Pos, "E1", "%s 不是 host，不能调用能力 %s", ht, callee.Field)
			c.checkArgs(n.Pos, n.Args, nil)
			return tInval
		}
		sig = ht.Methods[callee.Field]
		if sig == nil {
			c.errorf(callee.Pos, "E1", "host %s 没有能力 %s", ht.Name, callee.Field)
			c.checkArgs(n.Pos, n.Args, nil)
			return tInval
		}

	default:
		c.errorf(n.Pos, "E1", "只能调用具名函数或 host 能力")
		c.checkArgs(n.Pos, n.Args, nil)
		return tInval
	}

	c.checkArgs(n.Pos, n.Args, sig)
	return sig.Ret
}

// checkArgs 检查实参个数与类型；sig 为 nil 时只检查表达式本身。
func (c *checker) checkArgs(pos token.Pos, args []ast.Expr, sig *FnSig) {
	if sig == nil {
		for _, a := range args {
			c.checkExpr(a)
		}
		return
	}
	if len(args) != len(sig.Params) {
		c.errorf(pos, "E1", "%s 需要 %d 个参数，得到 %d 个", sig.Name, len(sig.Params), len(args))
	}
	for i, a := range args {
		at := c.checkExpr(a)
		if i >= len(sig.Params) {
			continue
		}
		if !assignable(at, sig.Params[i]) {
			c.errorf(posOf(a), "E1", "%s 的第 %d 个参数应为 %s，得到 %s",
				sig.Name, i+1, sig.Params[i], at)
		}
	}
}

// ---------- 位置工具 ----------
// posOf 取出表达式的位置；AST 接口未提供统一的位置方法。
func posOf(e ast.Expr) token.Pos {
	switch n := e.(type) {
	case *ast.Ident:
		return n.Pos
	case *ast.IntLit:
		return n.Pos
	case *ast.StrLit:
		return n.Pos
	case *ast.BytesLit:
		return n.Pos
	case *ast.BoolLit:
		return n.Pos
	case *ast.UnitLit:
		return n.Pos
	case *ast.Call:
		return n.Pos
	case *ast.Construct:
		return n.Pos
	case *ast.Select:
		return n.Pos
	case *ast.Binary:
		return n.Pos
	case *ast.Propagate:
		return n.Pos
	}
	return token.Pos{}
}
