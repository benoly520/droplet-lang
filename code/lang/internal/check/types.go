// Package check 对语法树做名字解析与类型检查。
//
// 本文件由 check.go 同包拆分而来，职责：类型表示、作用域与类型判定。
package check

import (
	"droplet/internal/ast"
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
	IsEnv          bool    // 由 env 块带来的 record：只能由生成的加载器构造
}

// EnvKey 是一个配置键。Default 存的是**值本身**（整数的十进制写法或字符串内容），
// 不是 C 文本：怎么落进 C 由 gen 决定，校验与导出都只看这个值。
type EnvKey struct {
	Name       string
	Pos        token.Pos
	Type       *Type
	Default    string // HasDefault 为 false 时无意义
	HasDefault bool
}

// EnvSpec 是程序的 env 声明。Prefix 是环境变量前缀（块名大写），
// 所以下面每个键的读取面是 <Prefix>_<键名大写>：`.dlt` 源码里永远不出现环境变
// 量名（SPEC §5.7「配置不是环境变量名」）。
type EnvSpec struct {
	Name   string
	Prefix string
	Keys   []EnvKey
	Pos    token.Pos
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
	IsService  bool // 服务形态：带 Request 参数且返回 Response | 错误
	ReqIdx     int  // Request 参数在 Params 里的下标，非服务形态为 -1
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

// ---------- 作用域 ----------
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

// ---------- 类型判定 ----------
func isUnit(t *Type) bool {
	return t != nil && (t.Kind == KUnit || t.Kind == KInvalid)
}

// okType 取出返回类型的成功成员：联合取第 0 项，其余就是它本身。
// 服务入口的「成功即值」规则（D2 / R1）与裸 `return` 的单位值（R3）都按它判定。
func okType(t *Type) *Type {
	if t != nil && t.Kind == KUnion && len(t.Items) > 0 {
		return t.Items[0]
	}
	return t
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
