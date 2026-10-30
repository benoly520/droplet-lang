// Package check 对语法树做名字解析与类型检查。
//
// 本文件由 check.go 同包拆分而来，职责：// 第一遍：收集声明、填类型、注册函数签名。
package check

import (
	"droplet/internal/ast"
	"droplet/internal/token"
)

// ---------- 第一遍：声明 ----------
// ---------- 第一遍：声明 ----------

func (c *checker) declare() {
	for _, it := range c.prog.Items {
		switch n := it.(type) {
		case *ast.RecordType:
			if builtin(n.Name) != nil {
				c.errorf(n.Pos, "E1", "类型名 %s 与内置类型冲突", n.Name)
				continue
			}
			if _, dup := c.types[n.Name]; dup {
				c.errorf(n.Pos, "E1", "类型 %s 重复定义", n.Name)
				continue
			}
			c.types[n.Name] = &Type{
				Kind:         KRecord,
				Name:         n.Name,
				Pos:          n.Pos,
				FieldsByName: map[string]*Field{},
			}

		case *ast.SumType:
			if builtin(n.Name) != nil {
				c.errorf(n.Pos, "E1", "类型名 %s 与内置类型冲突", n.Name)
				continue
			}
			if _, dup := c.types[n.Name]; dup {
				c.errorf(n.Pos, "E1", "类型 %s 重复定义", n.Name)
				continue
			}
			c.types[n.Name] = &Type{
				Kind:           KSum,
				Name:           n.Name,
				Pos:            n.Pos,
				VariantsByName: map[string]*Variant{},
			}

		case *ast.HostDecl:
			if _, dup := c.hosts[n.Name]; dup {
				c.errorf(n.Pos, "E1", "host %s 重复定义", n.Name)
				continue
			}
			c.hosts[n.Name] = &Type{
				Kind:    KHost,
				Name:    n.Name,
				Pos:     n.Pos,
				Methods: map[string]*FnSig{},
			}

		case *ast.EnvDecl:
			// D0 不实现 env：parser 认识它、checker 与 gen 都不处理，不报错就等于
			// 静默丢配置（半实现比不支持更危险）。
			c.errorf(n.Pos, "E1",
				"env 声明不在 D0 子集（D1 实现）；配置目前走环境变量，见 docs/IMPL-PLAN.md §6")
		}
	}
}

func (c *checker) fillTypes() {
	for _, it := range c.prog.Items {
		switch n := it.(type) {
		case *ast.RecordType:
			t := c.types[n.Name]
			if t == nil || t.Kind != KRecord {
				continue
			}
			c.fillFields(t, n.Fields)

		case *ast.SumType:
			t := c.types[n.Name]
			if t == nil || t.Kind != KSum {
				continue
			}
			for _, v := range n.Variants {
				if _, dup := t.VariantsByName[v.Name]; dup {
					c.errorf(v.Pos, "E1", "变体 %s 在 %s 中重复", v.Name, t.Name)
					continue
				}
				vv := &Variant{Name: v.Name, Pos: v.Pos, FieldsByName: map[string]*Field{}}
				if len(v.Fields) > 0 {
					// 运行时的错误只保留 tag，字段无处安放；gen 以前也会拒。
					// 在 checker 拦下来，「check ok」才能等于「能构建」。
					c.errorf(v.Pos, "E1",
						"D0 不支持带字段的变体 %s.%s：错误只按 tag 映射到状态码", t.Name, v.Name)
					continue
				}
				for _, f := range v.Fields {
					if _, dup := vv.FieldsByName[f.Name]; dup {
						c.errorf(f.Pos, "E1", "字段 %s 在变体 %s 中重复", f.Name, v.Name)
						continue
					}
					fd := &Field{Name: f.Name, Pos: f.Pos, Type: c.resolve(f.Type)}
					vv.Fields = append(vv.Fields, fd)
					vv.FieldsByName[f.Name] = fd
				}
				t.Variants = append(t.Variants, vv)
				t.VariantsByName[v.Name] = vv
				c.variantOrder = append(c.variantOrder, v.Name)
			}
		}
	}
}

// fillFields 把语法字段解析成检查期字段，重复字段报 E1。
func (c *checker) fillFields(owner *Type, fields []ast.Field) {
	for _, f := range fields {
		if _, dup := owner.FieldsByName[f.Name]; dup {
			c.errorf(f.Pos, "E1", "字段 %s 重复", f.Name)
			continue
		}
		fd := &Field{Name: f.Name, Pos: f.Pos, Type: c.resolve(f.Type)}
		owner.Fields = append(owner.Fields, fd)
		owner.FieldsByName[f.Name] = fd
	}
}

// ---------- 函数与能力声明 ----------
func (c *checker) declareFns() {
	for _, it := range c.prog.Items {
		switch n := it.(type) {
		case *ast.FnDecl:
			c.registerFn(n, "", false)

		case *ast.EntryDecl:
			for _, fn := range n.Fns {
				c.registerFn(fn, "", true)
			}

		case *ast.HostDecl:
			h := c.hosts[n.Name]
			if h == nil {
				continue
			}
			for _, fn := range n.Fns {
				sig := c.newSig(fn, n.Name)
				if _, dup := h.Methods[fn.Name]; dup {
					c.errorf(fn.Pos, "E1", "host %s 的能力 %s 重复", n.Name, fn.Name)
					continue
				}
				h.Methods[fn.Name] = sig
				c.checkHostCap(n.Name, fn, sig)
			}
		}
	}
}

// ---------- 签名与类型解析 ----------
func (c *checker) registerFn(fn *ast.FnDecl, host string, isEntry bool) *FnSig {
	sig := c.newSig(fn, host)
	sig.IsEntry = isEntry
	if _, dup := c.fns[fn.Name]; dup {
		c.errorf(fn.Pos, "E1", "函数 %s 重复定义", fn.Name)
		return sig
	}
	c.fns[fn.Name] = sig
	return sig
}

func (c *checker) newSig(fn *ast.FnDecl, host string) *FnSig {
	sig := &FnSig{Name: fn.Name, Pos: fn.Pos, Host: host, Decl: fn}
	for _, p := range fn.Params {
		sig.Params = append(sig.Params, c.resolve(p.Type))
		sig.ParamNames = append(sig.ParamNames, p.Name)
	}
	sig.Ret = c.resolve(fn.Ret)
	return sig
}

// ---------- 类型解析 ----------

func (c *checker) resolve(t ast.Type) *Type {
	switch n := t.(type) {
	case nil:
		return tInval
	case *ast.Named:
		return c.resolveNamed(n.Name, n.Pos)
	case *ast.Union:
		out := &Type{Kind: KUnion, Pos: n.Pos}
		for _, it := range n.Items {
			out.Items = append(out.Items, c.resolve(it))
		}
		if len(out.Items) < 2 {
			c.errorf(n.Pos, "E1", "联合返回至少需要两个成员")
		}
		return out
	}
	return tInval
}

func (c *checker) resolveNamed(name string, pos token.Pos) *Type {
	if t := builtin(name); t != nil {
		return t
	}
	if t, ok := c.types[name]; ok {
		return t
	}
	if h, ok := c.hosts[name]; ok {
		return h
	}
	c.errorf(pos, "E1", "未定义类型 %s", name)
	return tInval
}
