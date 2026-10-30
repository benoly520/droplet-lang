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
