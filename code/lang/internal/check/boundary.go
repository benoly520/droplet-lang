// Package check 对语法树做名字解析与类型检查。
//
// 本文件由 check.go 同包拆分而来，职责：boundary 方法检查：可序列化性与意图。
package check

import (
	"droplet/internal/ast"
)

// ---------- 边界方法 ----------
func (c *checker) checkBoundary(b *ast.BoundaryDecl) {
	seen := map[string]bool{}
	for _, m := range b.Methods {
		if seen[m.Name] {
			c.errorf(m.Pos, "E1", "boundary 方法 %s 重复", m.Name)
		}
		seen[m.Name] = true

		if !hasIntent(m.Intents) {
			c.errorf(m.Pos, "E11", "boundary 方法 %s 缺少 example 或 invariant", m.Name)
		}
		for _, p := range m.Params {
			pt := c.resolve(p.Type)
			if !c.serializable(pt, map[*Type]bool{}) {
				c.errorf(p.Pos, "E3", "参数 %s 的类型 %s 不可跨边界序列化", p.Name, pt)
			}
		}
		rt := c.resolve(m.Ret)
		if !c.serializable(rt, map[*Type]bool{}) {
			c.errorf(m.Pos, "E3", "返回类型 %s 不可跨边界序列化", rt)
		}
	}
}

// ---------- 可序列化判定 ----------
// serializable 判断类型能否跨 boundary 传输；seen 用于打断自我引用。
func (c *checker) serializable(t *Type, seen map[*Type]bool) bool {
	if t == nil || t.Kind == KInvalid {
		return true
	}
	switch t.Kind {
	case KStr, KBytes, KU64, KI64, KBool, KUnit:
		return true
	case KRecord:
		if seen[t] {
			return true
		}
		seen[t] = true
		for _, f := range t.Fields {
			if !c.serializable(f.Type, seen) {
				return false
			}
		}
		return true
	case KSum:
		if seen[t] {
			return true
		}
		seen[t] = true
		for _, v := range t.Variants {
			for _, f := range v.Fields {
				if !c.serializable(f.Type, seen) {
					return false
				}
			}
		}
		return true
	case KUnion:
		for _, it := range t.Items {
			if !c.serializable(it, seen) {
				return false
			}
		}
		return true
	}
	return false
}

// ---------- 意图判定 ----------
func hasIntent(intents []ast.Intent) bool {
	for _, it := range intents {
		if it.Kind == "example" || it.Kind == "invariant" {
			return true
		}
	}
	return false
}
