// Package check 对语法树做名字解析与类型检查。
//
// 与 parse 的差别：parse 遇到第一个语法错误就停（fail-fast），
// check 收集全部错误（collect-all）并排序输出，避免写代码时改一个错跑一次。
package check

import (
	"fmt"
	"sort"

	"droplet/internal/ast"
	"droplet/internal/host"
	"droplet/internal/token"
)

// ---------- 类型表示 ----------

// Kind 是检查期类型的种类。
type Kind int

const (
	KInvalid Kind = iota // 错误占位，对任何类型都可赋值，用于吞掉级联错误
	KStr
	KBytes
	KU64
	KI64
	KBool
	KUnit
	KRecord
	KSum
	KHost
	KUnion
	KHostMethod
)

// Type 是检查期类型。record/sum/host 以名字唯一，字段按指针连接，允许互相引用。
type Type struct {
	Kind           Kind
	Name           string
	Pos            token.Pos
	Fields         []*Field
	FieldsByName   map[string]*Field
	Variants       []*Variant
	VariantsByName map[string]*Variant
	Methods        map[string]*FnSig
	Items          []*Type // KUnion 的成员，第 0 个是成功类型
	Method         *FnSig  // KHostMethod 指向的能力签名
}

// Field 是 record 字段或变体字段。
type Field struct {
	Name string
	Type *Type
	Pos  token.Pos
}

// Variant 是 sum 的一个变体。
type Variant struct {
	Name         string
	Pos          token.Pos
	Fields       []*Field
	FieldsByName map[string]*Field
}

// FnSig 是函数签名。
type FnSig struct {
	Name       string
	Pos        token.Pos
	Params     []*Type
	ParamNames []string
	Ret        *Type
	Host       string // 所属 host 名；普通函数为空
	IsEntry    bool
	Decl       *ast.FnDecl
}

var (
	tStr   = &Type{Kind: KStr, Name: "str"}
	tBytes = &Type{Kind: KBytes, Name: "bytes"}
	tU64   = &Type{Kind: KU64, Name: "u64"}
	tI64   = &Type{Kind: KI64, Name: "i64"}
	tBool  = &Type{Kind: KBool, Name: "bool"}
	tUnit  = &Type{Kind: KUnit, Name: "Unit"}
	tInval = &Type{Kind: KInvalid, Name: "<invalid>"}
)

func builtin(name string) *Type {
	switch name {
	case "str":
		return tStr
	case "bytes":
		return tBytes
	case "u64":
		return tU64
	case "i64":
		return tI64
	case "bool":
		return tBool
	case "Unit":
		return tUnit
	}
	return nil
}

func (t *Type) String() string {
	if t == nil {
		return "<nil>"
	}
	if t.Kind == KUnion {
		s := ""
		for i, it := range t.Items {
			if i > 0 {
				s += " | "
			}
			s += it.String()
		}
		return s
	}
	return t.Name
}

// ---------- 结果 ----------

// Error 是一条检查错误。
type Error struct {
	Pos  token.Pos
	Code string
	Msg  string
}

func (e Error) Error() string {
	return fmt.Sprintf("%s: %s: %s", e.Pos, e.Code, e.Msg)
}

// Result 是检查结果。Errors 为空表示通过。
//
// ExprTypes / LetTypes / Sigs / Types 是给 codegen 用的副产物：
// 生成阶段不再重新推导类型，避免两套推导漂移。
type Result struct {
	Errors     []Error
	Decls      int
	FnCount    int
	Tags       map[string]int // sum 变体名 → 编号，0 保留给成功值
	EntryPoint string

	ExprTypes map[ast.Expr]*Type
	LetTypes  map[*ast.Let]*Type
	Sigs      map[string]*FnSig
	Types     map[string]*Type
	Hosts     map[string]*Type
}

// ---------- 检查器 ----------

type checker struct {
	prog  *ast.Program
	types map[string]*Type
	hosts map[string]*Type
	fns   map[string]*FnSig

	errs []Error

	scope *scope
	curFn *FnSig

	variantOrder []string        // 变体声明顺序，保证 tag 分配稳定
	errorVariant map[string]bool // 进入过 `!` 传播的错误变体
	seenPos      map[string]bool // 去重：同一位置的同一错误只报一次

	exprTypes map[ast.Expr]*Type
	letTypes  map[*ast.Let]*Type
}

type scope struct {
	vars   map[string]*Type
	parent *scope
}

func newScope(parent *scope) *scope {
	return &scope{vars: map[string]*Type{}, parent: parent}
}

func (s *scope) define(name string, t *Type) { s.vars[name] = t }

func (s *scope) lookup(name string) *Type {
	for cur := s; cur != nil; cur = cur.parent {
		if t, ok := cur.vars[name]; ok {
			return t
		}
	}
	return nil
}

// Program 检查一个编译单元。返回值总是非 nil；错误在 Result.Errors 里。
func Program(p *ast.Program) *Result {
	c := &checker{
		prog:         p,
		types:        map[string]*Type{},
		hosts:        map[string]*Type{},
		fns:          map[string]*FnSig{},
		errorVariant: map[string]bool{},
		seenPos:      map[string]bool{},
		exprTypes:    map[ast.Expr]*Type{},
		letTypes:     map[*ast.Let]*Type{},
	}
	c.declare()
	c.fillTypes()
	c.declareFns()
	c.checkBodies()

	sort.SliceStable(c.errs, func(i, j int) bool {
		a, b := c.errs[i].Pos, c.errs[j].Pos
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})

	res := &Result{
		Errors:    c.errs,
		Decls:     len(p.Items),
		FnCount:   len(c.fns),
		Tags:      c.assignTags(),
		ExprTypes: c.exprTypes,
		LetTypes:  c.letTypes,
		Sigs:      c.fns,
		Types:     c.types,
		Hosts:     c.hosts,
	}
	for name, sig := range c.fns {
		if sig.IsEntry {
			res.EntryPoint = name
		}
	}
	return res
}

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
				c.checkHostCap(n.Name, fn)
			}
		}
	}
}

// checkHostCap 确认声明的能力运行时真的提供、且参数个数对得上。
//
// 能力表是 check 与 gen 共用的（internal/host）：只有 checker 也查一次，
// `droplet check` 的 ok 才等于「能构建」（以前只能到 gen 才报 E-GEN）。
func (c *checker) checkHostCap(group string, fn *ast.FnDecl) {
	key := group + "." + fn.Name
	cap, ok := host.Lookup(key)
	if !ok {
		c.errorf(fn.Pos, "E2", "运行时不提供能力 %s；D0 能力表见 internal/host/host.go", key)
		return
	}
	if len(fn.Params) != cap.Arity {
		c.errorf(fn.Pos, "E2", "能力 %s 需要 %d 个参数，声明了 %d", key, cap.Arity, len(fn.Params))
	}
}

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

// ---------- 辅助 ----------

func (c *checker) errorf(pos token.Pos, code, format string, a ...any) {
	key := fmt.Sprintf("%s|%s|%s", pos, code, fmt.Sprintf(format, a...))
	if c.seenPos[key] {
		return
	}
	c.seenPos[key] = true
	c.errs = append(c.errs, Error{Pos: pos, Code: code, Msg: fmt.Sprintf(format, a...)})
}

// markError 把错误类型里的变体标记为错误变体，供 tag 分配使用。
func (c *checker) markError(t *Type) {
	if t.Kind != KSum {
		return
	}
	for _, v := range t.Variants {
		c.errorVariant[v.Name] = true
	}
}

func (c *checker) assignTags() map[string]int {
	tags := map[string]int{"<ok>": 0}
	next := 100
	for _, name := range c.variantOrder {
		if c.errorVariant[name] {
			if _, ok := tags[name]; !ok {
				tags[name] = next
				next++
			}
		}
	}
	next = 1
	for _, name := range c.variantOrder {
		if _, ok := tags[name]; ok {
			continue
		}
		tags[name] = next
		next++
	}
	return tags
}

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

func hasIntent(intents []ast.Intent) bool {
	for _, it := range intents {
		if it.Kind == "example" || it.Kind == "invariant" {
			return true
		}
	}
	return false
}

func isUnit(t *Type) bool {
	return t != nil && (t.Kind == KUnit || t.Kind == KInvalid)
}

func assignable(from, to *Type) bool {
	if from == nil || to == nil {
		return true
	}
	if from.Kind == KInvalid || to.Kind == KInvalid {
		return true
	}
	if from.Kind == KUnion {
		// 联合不能隐式取成功值，必须用 `!`
		if to.Kind == KUnion {
			return sameType(from, to)
		}
		return false
	}
	if to.Kind == KUnion {
		for _, it := range to.Items {
			if sameType(from, it) {
				return true
			}
		}
		return false
	}
	return sameType(from, to)
}

func returnable(from, to *Type) bool { return assignable(from, to) }

func sameType(a, b *Type) bool {
	if a == nil || b == nil {
		return true
	}
	if a == b {
		return true
	}
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case KRecord, KSum, KHost:
		return a.Name == b.Name
	case KUnion:
		if len(a.Items) != len(b.Items) {
			return false
		}
		for i := range a.Items {
			if !sameType(a.Items[i], b.Items[i]) {
				return false
			}
		}
		return true
	}
	return true
}

func containsType(list []*Type, t *Type) bool {
	for _, it := range list {
		if sameType(it, t) {
			return true
		}
	}
	return false
}

func comparable(t *Type) bool {
	switch t.Kind {
	case KStr, KBytes, KU64, KI64, KBool:
		return true
	}
	return false
}

func ordered(t *Type) bool {
	switch t.Kind {
	case KStr, KU64, KI64:
		return true
	}
	return false
}

func numeric(t *Type) bool {
	return t.Kind == KU64 || t.Kind == KI64
}
