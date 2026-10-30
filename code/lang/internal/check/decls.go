// Package check 对语法树做名字解析与类型检查。
//
// 本文件由 check.go 同包拆分而来，职责：第一遍：收集声明、填类型、注册函数签名。
package check

import (
	"droplet/internal/ast"
	"droplet/internal/token"
)

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

		case *ast.BoundaryDecl:
			if c.bnd != nil {
				c.errorf(n.Pos, "E1", "boundary 块只能有一个：%s 与 %s，D3 不支持多张路由表",
					c.bnd.Name, n.Name)
				continue
			}
			c.bnd = n

		case *ast.EnvDecl:
			// env 块名就是一个类型（D1）：入口参数写 cfg: App 就是普通的 record 引用，
			// 不需要新的类型面；不同的是它只能由生成的加载器构造，源码里写不出字面量。
			if c.env != nil {
				c.errorf(n.Pos, "E1", "env 块只能有一个：%s 与 %s，D1 不支持多套配置",
					c.env.Name, n.Name)
				continue
			}
			if builtin(n.Name) != nil {
				c.errorf(n.Pos, "E1", "配置名 %s 与内置类型冲突", n.Name)
				continue
			}
			if _, dup := c.types[n.Name]; dup {
				c.errorf(n.Pos, "E1", "配置名 %s 与类型 %s 重名", n.Name, n.Name)
				continue
			}
			c.types[n.Name] = &Type{
				Kind:         KRecord,
				Name:         n.Name,
				Pos:          n.Pos,
				FieldsByName: map[string]*Field{},
				IsEnv:        true,
			}
			c.env = &EnvSpec{Name: n.Name, Prefix: envPrefix(n.Name), Pos: n.Pos}
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
				// tag 按变体名全局分配（match 与契约 JSON 都只认名字），
				// 同名变体出现在两个 sum 里会让 tag 撞车，在声明处就拦下。
				if owner, dup := c.variantOwner[v.Name]; dup {
					c.errorf(v.Pos, "E1", "变体名 %s 已被 %s 使用：tag 全局唯一，换个名字", v.Name, owner)
					continue
				}
				c.variantOwner[v.Name] = t.Name + "." + v.Name
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

		case *ast.EnvDecl:
			if c.env == nil || c.env.Name != n.Name {
				continue // 重名的块已在 declare() 报过，不在此处叠加
			}
			c.fillEnv(n)
		}
	}
}

// fillEnv 把 env 键解析成字段与加载面：类型限子集、默认值形状必须匹配类型。
func (c *checker) fillEnv(n *ast.EnvDecl) {
	owner := c.types[n.Name]
	for _, f := range n.Fields {
		if _, dup := owner.FieldsByName[f.Name]; dup {
			c.errorf(f.Pos, "E1", "配置键 %s 重复", f.Name)
			continue
		}
		ft := c.resolve(f.Type)
		if ft.Kind != KU64 && ft.Kind != KStr {
			c.errorf(f.Pos, "E1", "配置键 %s 的类型 %s 不支持：env 只接受 u64 与 str",
				f.Name, ft.Name)
			continue
		}
		key := EnvKey{Name: f.Name, Pos: f.Pos, Type: ft}
		if val, has, ok := c.envDefault(f, ft); ok {
			key.Default, key.HasDefault = val, has
		}
		c.env.Keys = append(c.env.Keys, key)
		fd := &Field{Name: f.Name, Pos: f.Pos, Type: ft}
		owner.Fields = append(owner.Fields, fd)
		owner.FieldsByName[f.Name] = fd
	}
}

// envDefault 校验默认值与声明类型是否同形。
// 返回（值, 有没有默认值, 形状是否合法）：第三个为 false 时已报过错。
func (c *checker) envDefault(f ast.EnvField, ft *Type) (string, bool, bool) {
	if f.Default == nil {
		return "", false, true // 必填：没有默认值，但不是错误
	}
	bad := func(got string) (string, bool, bool) {
		c.errorf(f.Pos, "E1",
			"配置键 %s 声明为 %s，默认值却是 %s：删掉默认值或改成同类型字面量", f.Name, ft.Name, got)
		return "", false, false
	}
	switch n := f.Default.(type) {
	case *ast.IntLit:
		if ft.Kind != KU64 {
			return bad("整数字面量")
		}
		return n.Value, true, true
	case *ast.StrLit:
		if ft.Kind != KStr {
			return bad("字符串字面量")
		}
		return n.Value, true, true
	default:
		return bad("非字面量")
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
			c.registerFn(n, "", n.Entry)

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
