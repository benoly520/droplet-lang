// Package check 对语法树做名字解析与类型检查。
//
// 本文件由 check.go 同包拆分而来，职责：意图表达式（`requires` / `example`）
// 的类型检查与常数求值 —— E10 里"能静态判否"的那一半。
//
// E10 的完整定义（§7.8.1）是对候选实现做机械变异、看意图是否抓得住，那需要独立
// 验证器。这里只做不需要验证器的部分，并且只报**确定**的结论：
//
//  1. 意图表达式必须是 bool（以前完全没检查，`requires 1` 能一路通过）；
//  2. 常量求值为 true 的 requires / example = 空断言（无约束力）；
//  3. 常量求值为 false = 恒假，永远无法满足；
//  4. 调用点实参全是常量时，被调函数的前置条件必须成立（§7.2 的 E10 定义原文：
//     "requires 在调用点可静态判否"）。
//
// 判不出来就放过。把"判不出来"报成错误会让 E10 变成税，而它本该只在有牙齿时咬人。
package check

import (
	"strconv"
	"strings"

	"droplet/internal/ast"
	"droplet/internal/print"
)

// checkIntents 检查一组意图里的表达式。调用方负责把参数放进 scope、
// 把 curFn 设为所属函数（boundary 方法没有函数体，curFn 为 nil）。
func (c *checker) checkIntents(owner string, intents []ast.Intent) {
	for _, it := range intents {
		if it.Expr == nil {
			continue
		}
		t := c.checkExpr(it.Expr)
		if t.Kind == KInvalid {
			continue
		}
		if t.Kind != KBool {
			c.errorf(it.Pos, "E1", "%s 的 %s 必须是 bool，得到 %s", owner, it.Kind, t)
			continue
		}
		v, ok := evalConst(it.Expr, nil)
		if !ok || v.kind.Kind != KBool {
			continue
		}
		src := print.Expr(it.Expr)
		if v.bo {
			c.errorf(it.Pos, "E10",
				"%s 的 %s `%s` 恒为 true：它对实现没有任何约束力（§7.8.1 的空声明）",
				owner, it.Kind, src)
			continue
		}
		c.errorf(it.Pos, "E10", "%s 的 %s `%s` 恒为 false：这个意图永远无法满足",
			owner, it.Kind, src)
	}
}

// checkRequiresAtCall 是 E10 的调用点那一半：实参全为常量时，前置条件必须成立。
// 实参只要有一个定不出值就整条放过 —— 半截求值会给出假的"恒假"。
func (c *checker) checkRequiresAtCall(n *ast.Call, name string, sig *FnSig) {
	if sig.Decl == nil || len(sig.Decl.Intents) == 0 ||
		len(n.Args) != len(sig.ParamNames) {
		return
	}
	env := make(map[string]constVal, len(n.Args))
	for i, a := range n.Args {
		v, ok := evalConst(a, nil)
		if !ok {
			return
		}
		env[sig.ParamNames[i]] = v
	}
	for _, it := range sig.Decl.Intents {
		if it.Kind != "requires" || it.Expr == nil {
			continue
		}
		v, ok := evalConst(it.Expr, env)
		if !ok || v.kind.Kind != KBool || v.bo {
			continue
		}
		parts := make([]string, 0, len(n.Args))
		for i, a := range n.Args {
			parts = append(parts, sig.ParamNames[i]+" = "+print.Expr(a))
		}
		c.errorf(n.Pos, "E10",
			"%s 的前置条件 `%s` 在实参（%s）下恒假：这个调用永远不可能成立",
			name, print.Expr(it.Expr), strings.Join(parts, ", "))
	}
}

// ---------- 常数求值 ----------

// constVal 是常数求值的结果。只覆盖意图里写得出来的值：
// 意图拿不到运行时数据（没有 host 调用、没有记录构造），所以这几种足够。
type constVal struct {
	kind *Type
	u    uint64 // u64
	s    string // str
	b    []byte // bytes
	bo   bool   // bool
}

// evalConst 在常数环境里求值。判不出来就返回 false —— 调用方据此**跳过**，
// 而不是猜一个值。
func evalConst(e ast.Expr, env map[string]constVal) (constVal, bool) {
	switch n := e.(type) {
	case *ast.IntLit:
		u, err := strconv.ParseUint(n.Value, 10, 64)
		if err != nil {
			return constVal{}, false
		}
		return constVal{kind: tU64, u: u}, true
	case *ast.StrLit:
		return constVal{kind: tStr, s: n.Value}, true
	case *ast.BytesLit:
		return constVal{kind: tBytes, b: n.Value}, true
	case *ast.BoolLit:
		return constVal{kind: tBool, bo: n.Value}, true
	case *ast.UnitLit:
		return constVal{kind: tUnit}, true
	case *ast.Ident:
		v, ok := env[n.Name]
		return v, ok
	case *ast.Binary:
		return evalBinary(n, env)
	}
	return constVal{}, false
}

func evalBinary(n *ast.Binary, env map[string]constVal) (constVal, bool) {
	tBoolV := func(b bool) (constVal, bool) { return constVal{kind: tBool, bo: b}, true }

	// `&&` / `||` 先看左侧：`false && (定不出值)` 仍然是确定的 false。
	if n.Op == "&&" || n.Op == "||" {
		l, ok := evalConst(n.Left, env)
		if !ok || l.kind.Kind != KBool {
			return constVal{}, false
		}
		if n.Op == "&&" && !l.bo {
			return tBoolV(false)
		}
		if n.Op == "||" && l.bo {
			return tBoolV(true)
		}
		r, ok := evalConst(n.Right, env)
		if !ok || r.kind.Kind != KBool {
			return constVal{}, false
		}
		return tBoolV(r.bo)
	}

	l, ok := evalConst(n.Left, env)
	if !ok {
		return constVal{}, false
	}
	r, ok := evalConst(n.Right, env)
	if !ok || l.kind != r.kind {
		return constVal{}, false
	}

	switch n.Op {
	case "==", "!=":
		eq, ok := constEqual(l, r)
		if !ok {
			return constVal{}, false
		}
		return tBoolV(eq == (n.Op == "=="))
	case "<", "<=", ">", ">=":
		return compareConst(n.Op, l, r)
	case "+", "-", "*", "/", "%":
		return arithConst(n.Op, l, r)
	}
	return constVal{}, false
}

func constEqual(l, r constVal) (bool, bool) {
	switch l.kind.Kind {
	case KU64:
		return l.u == r.u, true
	case KStr:
		return l.s == r.s, true
	case KBytes:
		return string(l.b) == string(r.b), true
	case KBool:
		return l.bo == r.bo, true
	case KUnit:
		return true, true
	}
	return false, false
}

func compareConst(op string, l, r constVal) (constVal, bool) {
	var cmp int
	switch l.kind.Kind {
	case KU64:
		cmp = cmpU64(l.u, r.u)
	case KStr:
		cmp = strings.Compare(l.s, r.s)
	default:
		return constVal{}, false
	}
	var b bool
	switch op {
	case "<":
		b = cmp < 0
	case "<=":
		b = cmp <= 0
	case ">":
		b = cmp > 0
	case ">=":
		b = cmp >= 0
	}
	return constVal{kind: tBool, bo: b}, true
}

func cmpU64(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// arithConst 只做 u64：意图里写不出 i64 字面量（没有一元负号），
// 溢出或除零一律判"定不出来"，不静默回绕。
func arithConst(op string, l, r constVal) (constVal, bool) {
	if l.kind.Kind != KU64 {
		return constVal{}, false
	}
	const maxU64 = ^uint64(0)
	var u uint64
	switch op {
	case "+":
		if l.u > maxU64-r.u {
			return constVal{}, false
		}
		u = l.u + r.u
	case "-":
		if r.u > l.u {
			return constVal{}, false
		}
		u = l.u - r.u
	case "*":
		if r.u != 0 && l.u > maxU64/r.u {
			return constVal{}, false
		}
		u = l.u * r.u
	case "/", "%":
		if r.u == 0 {
			return constVal{}, false
		}
		if op == "/" {
			u = l.u / r.u
		} else {
			u = l.u % r.u
		}
	default:
		return constVal{}, false
	}
	return constVal{kind: tU64, u: u}, true
}
