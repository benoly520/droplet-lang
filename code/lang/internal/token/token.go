// Package token 定义词法单元与源码位置。
package token

import "fmt"

// Kind 是词法单元的类别。
type Kind int

const (
	EOF Kind = iota
	Ident
	Int
	Str
	Bytes

	// 固定关键字（10 个，SPEC §14.2 预算内）。
	KwType
	KwFn
	KwEntry
	KwHost
	KwEnv
	KwBoundary
	KwLet
	KwReturn
	KwMatch
	KwIf

	// 上下文关键字：不占固定关键字预算，只在特定位置成立。
	CtxElse
	CtxWith
	CtxIdempotency
	CtxRequires
	CtxInvariant
	CtxExample
	CtxEffects
	CtxCap
	CtxBy

	LBrace
	RBrace
	LParen
	RParen
	LBrack
	RBrack
	Comma
	Colon
	Semi
	Arrow
	FatArrow
	Assign
	Pipe
	Bang
	Dot
	At
	Lt
	Gt

	EqEq
	NotEq
	Le
	Ge
	And
	Or
	Plus
	Minus
	Star
	Slash
	Percent
)

var kindNames = map[Kind]string{
	EOF: "EOF", Ident: "Ident", Int: "Int", Str: "Str", Bytes: "Bytes",

	KwType: "type", KwFn: "fn", KwEntry: "entry", KwHost: "host", KwEnv: "env",
	KwBoundary: "boundary", KwLet: "let", KwReturn: "return", KwMatch: "match", KwIf: "if",

	CtxElse: "else", CtxWith: "with", CtxIdempotency: "idempotency",
	CtxRequires: "requires", CtxInvariant: "invariant", CtxExample: "example",
	CtxEffects: "effects", CtxCap: "cap", CtxBy: "by",

	LBrace: "{", RBrace: "}", LParen: "(", RParen: ")", LBrack: "[", RBrack: "]",
	Comma: ",", Colon: ":", Semi: ";", Arrow: "->", FatArrow: "=>", Assign: "=",
	Pipe: "|", Bang: "!", Dot: ".", At: "@", Lt: "<", Gt: ">",

	EqEq: "==", NotEq: "!=", Le: "<=", Ge: ">=", And: "&&", Or: "||",
	Plus: "+", Minus: "-", Star: "*", Slash: "/", Percent: "%",
}

// String 返回类别的可读名，用于错误信息与 AST 转储。
func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Pos 是源码位置，行列均从 1 开始。
type Pos struct {
	File string
	Line int
	Col  int
}

// String 输出 file:line:col，供编译器报错使用。
func (p Pos) String() string {
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

// Token 是一个词法单元。
type Token struct {
	Kind Kind
	Text string
	Pos  Pos
}

// String 用于调试输出。
func (t Token) String() string {
	if t.Kind == Ident || t.Kind == Int || t.Kind == Str || t.Kind == Bytes {
		return fmt.Sprintf("%s(%q)@%s", t.Kind, t.Text, t.Pos)
	}
	return fmt.Sprintf("%s@%s", t.Kind, t.Pos)
}

var keywords = map[string]Kind{
	"type": KwType, "fn": KwFn, "entry": KwEntry, "host": KwHost,
	"env": KwEnv, "boundary": KwBoundary, "let": KwLet, "return": KwReturn,
	"match": KwMatch, "if": KwIf,

	"else": CtxElse, "with": CtxWith, "idempotency": CtxIdempotency,
	"requires": CtxRequires, "invariant": CtxInvariant, "example": CtxExample,
	"effects": CtxEffects, "cap": CtxCap, "by": CtxBy,
}

// Lookup 判断标识符是否是关键字（含上下文关键字）。
func Lookup(ident string) (Kind, bool) {
	k, ok := keywords[ident]
	return k, ok
}
