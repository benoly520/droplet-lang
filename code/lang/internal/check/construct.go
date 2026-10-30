// Package check 对语法树做名字解析与类型检查。
//
// 本文件负责构造：record 字面量 `Name { … }`，以及 sum 变体构造
// `T.V`（零字段）与 `T.V { … }`（带载荷）。
package check

import (
	"droplet/internal/ast"
	"droplet/internal/token"
)

// ---------- 构造 ----------

func (c *checker) checkConstruct(n *ast.Construct) *Type {
	t := c.resolveNamed(n.TypeName, n.Pos)
	if t.Kind == KInvalid {
		for _, f := range n.Fields {
			c.checkExpr(f.Value)
		}
		return tInval
	}
	if n.Variant != "" {
		return c.checkVariant(t, n.Variant, n.Fields, n.Pos)
	}
	if t.Kind != KRecord {
		c.errorf(n.Pos, "E1", "%s 不是 record，不能用字面量构造", t.Name)
		for _, f := range n.Fields {
			c.checkExpr(f.Value)
		}
		return tInval
	}
	if t.IsEnv {
		// 配置的值来自启动环境，不来自源码：能写 `App{ … }` 就等于把部署事实硬编码进二进制，
		// 而 SPEC §5.7 要的正是「缺键在起服务前就失败」而不是「缺键被一个字面量补上」。
		c.errorf(n.Pos, "E1",
			"配置类型 %s 不能在源码里构造：它的值来自 env 声明与启动环境", t.Name)
		for _, f := range n.Fields {
			c.checkExpr(f.Value)
		}
		return t
	}
	c.checkFields(t.Name, t.Fields, t.FieldsByName, n.Fields, n.Pos)
	return t
}

// checkVariant 检查 `T.V`（fields 为 nil）与 `T.V { … }` 两种变体构造。
func (c *checker) checkVariant(t *Type, name string, fields []ast.NamedExpr, pos token.Pos) *Type {
	if t.Kind != KSum {
		c.errorf(pos, "E1", "%s 不是 sum，没有变体 %s", t.Name, name)
		for _, f := range fields {
			c.checkExpr(f.Value)
		}
		return tInval
	}
	v, ok := t.VariantsByName[name]
	if !ok {
		c.errorf(pos, "E1", "%s 没有变体 %s", t.Name, name)
		for _, f := range fields {
			c.checkExpr(f.Value)
		}
		return tInval
	}
	if len(v.Fields) > 0 && fields == nil {
		c.errorf(pos, "E1", "变体 %s.%s 有 %d 个字段，要写成 `%s.%s { … }`",
			t.Name, name, len(v.Fields), t.Name, name)
		return t
	}
	if len(v.Fields) == 0 && len(fields) > 0 {
		c.errorf(pos, "E1", "变体 %s.%s 没有字段，不写 `{ … }`", t.Name, name)
	}
	c.checkFields(t.Name+"."+name, v.Fields, v.FieldsByName, fields, pos)
	return t
}

// checkFields 校验构造实参：字段存在、不重复、类型可赋值、不缺字段。
//
// record 与变体共用这一段：两者都是「具名字段 + 必须写全」，差别只在 owner 叫什么。
func (c *checker) checkFields(owner string, want []*Field, wantByName map[string]*Field,
	got []ast.NamedExpr, pos token.Pos) {
	seen := map[string]bool{}
	for _, f := range got {
		vt := c.checkExpr(f.Value)
		fd, ok := wantByName[f.Name]
		if !ok {
			c.errorf(f.Pos, "E1", "%s 没有字段 %s", owner, f.Name)
			continue
		}
		if seen[f.Name] {
			c.errorf(f.Pos, "E1", "字段 %s 重复赋值", f.Name)
		}
		seen[f.Name] = true
		if !assignable(vt, fd.Type) {
			c.errorf(f.Pos, "E1", "%s.%s 应为 %s，得到 %s", owner, f.Name, fd.Type, vt)
		}
	}
	for _, fd := range want {
		if !seen[fd.Name] {
			c.errorf(pos, "E1", "构造 %s 缺少字段 %s", owner, fd.Name)
		}
	}
}
