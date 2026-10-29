// Package ast 定义 Droplet 的抽象语法树。
//
// 约定：每个节点都带源码位置，且 AST 不做隐式归一化；
// 所有语义检查留到 checker，保证错误能定位到具体 token。
package ast

import "droplet/internal/token"

// Node 是所有 AST 节点的公共接口。
type Node interface{ node() }

// Item 是顶层声明。
type Item interface {
	Node
	item()
}

// Expr 是表达式。
type Expr interface {
	Node
	expr()
}

// Stmt 是语句。
type Stmt interface {
	Node
	stmt()
}

// Type 是类型表达式。
type Type interface {
	Node
	typ()
}

// Program 是编译单元：多个 .dlt 文件合并后的结果。
type Program struct {
	File  string
	Items []Item
}

// ---------- 类型声明 ----------

// RecordType 是 `type T { f: U }` 形式的结构体。
type RecordType struct {
	Pos    token.Pos
	Name   string
	Fields []Field
}

// SumType 是 `type T = A | B` 形式的和类型。
type SumType struct {
	Pos      token.Pos
	Name     string
	Variants []Variant
}

// Field 是 record 或变体里的字段。
type Field struct {
	Pos  token.Pos
	Name string
	Type Type
}

// Variant 是 sum 的一个变体，可带字段。
type Variant struct {
	Pos    token.Pos
	Name   string
	Fields []Field
}

// ---------- 函数与入口 ----------

// FnDecl 是函数声明。Body 为 nil 时表示 host 提供的实现（无函数体）。
type FnDecl struct {
	Pos     token.Pos
	Name    string
	Params  []Param
	Ret     Type
	Intents []Intent
	Body    *Block
}

// Param 是函数参数。
type Param struct {
	Pos  token.Pos
	Name string
	Type Type
}

// EntryDecl 是可执行入口，内含一个或多个函数。
type EntryDecl struct {
	Pos  token.Pos
	Name string
	Fns  []*FnDecl
}

// HostDecl 声明一组由运行时提供实现的能力。
type HostDecl struct {
	Pos  token.Pos
	Name string
	Fns  []*FnDecl
}

// EnvDecl 声明服务启动时的配置项。
type EnvDecl struct {
	Pos    token.Pos
	Name   string
	Fields []EnvField
}

// EnvField 是一个配置项，Default 可为 nil。
type EnvField struct {
	Pos     token.Pos
	Name    string
	Type    Type
	Default Expr
}

// BoundaryDecl 声明对外契约。
type BoundaryDecl struct {
	Pos     token.Pos
	Name    string
	Version int
	Methods []*BndMethod
}

// BndMethod 是契约里的一个方法。
type BndMethod struct {
	Pos        token.Pos
	Name       string
	Params     []Param
	Ret        Type
	Idempotent bool
	Intents    []Intent
}

// Intent 是意图声明（requires / invariant / example / effects / cap / idempotent）。
type Intent struct {
	Pos   token.Pos
	Kind  string
	Text  string
	Expr  Expr
	Names []string
}

// ---------- 语句 ----------

// Block 是一段语句序列，同时充当 if/match 的分支体。
type Block struct {
	Pos   token.Pos
	Stmts []Stmt
}

// Let 是 `let x = e` 或 `let x: T = e`。
type Let struct {
	Pos   token.Pos
	Name  string
	Type  Type
	Value Expr
}

// Return 是 `return e` 或 `return`（Value 为 nil）。
type Return struct {
	Pos   token.Pos
	Value Expr
}

// If 是条件语句，Else 可能是 *Block 或 *If，也可能为 nil。
type If struct {
	Pos  token.Pos
	Cond Expr
	Then *Block
	Else Node
}

// Match 是穷尽匹配。
type Match struct {
	Pos     token.Pos
	Subject Expr
	Arms    []Arm
}

// Arm 是 match 的一个分支。
type Arm struct {
	Pos     token.Pos
	Pattern Pattern
	Body    Node
}

// Pattern 是 match 分支模式，Wildcard 表示 `_`。
type Pattern struct {
	Pos      token.Pos
	Name     string
	Fields   []string
	Wildcard bool
}

// ExprStmt 是表达式语句。
type ExprStmt struct {
	Pos token.Pos
	X   Expr
}

// ---------- 表达式 ----------

// Ident 是标识符引用。
type Ident struct {
	Pos  token.Pos
	Name string
}

// IntLit 是整数字面量。
type IntLit struct {
	Pos   token.Pos
	Value string
}

// StrLit 是字符串字面量。
type StrLit struct {
	Pos   token.Pos
	Value string
}

// BytesLit 是字节字面量。
type BytesLit struct {
	Pos   token.Pos
	Value []byte
}

// BoolLit 是 true / false。
type BoolLit struct {
	Pos   token.Pos
	Value bool
}

// UnitLit 是 Unit 字面量。
type UnitLit struct {
	Pos token.Pos
}

// Call 是函数调用，Callee 可为 Ident 或 Select。
type Call struct {
	Pos    token.Pos
	Callee Expr
	Args   []Expr
}

// Construct 是 `Name { f: v }` 构造。
type Construct struct {
	Pos      token.Pos
	TypeName string
	Fields   []NamedExpr
}

// NamedExpr 是构造里的 `f: v`。
type NamedExpr struct {
	Pos   token.Pos
	Name  string
	Value Expr
}

// Select 是 `x.f` 字段访问。
type Select struct {
	Pos    token.Pos
	Target Expr
	Field  string
}

// Binary 是二元运算。无优先级：解析时左结合，写法要求显式括号。
type Binary struct {
	Pos   token.Pos
	Op    string
	Left  Expr
	Right Expr
}

// Propagate 是 `e!`，把错误就地向上传播。
type Propagate struct {
	Pos   token.Pos
	Inner Expr
}

// ---------- 类型表达式 ----------

// Named 是命名类型（含 str/bytes/u64/i64/bool/Unit 等原语）。
type Named struct {
	Pos  token.Pos
	Name string
}

// Union 是联合返回 `A | E1 | E2`。
type Union struct {
	Pos   token.Pos
	Items []Type
}

// ---------- 接口实现标记 ----------

func (*Program) node() {}

func (*RecordType) node() {}
func (*RecordType) item() {}

func (*SumType) node() {}
func (*SumType) item() {}

func (*FnDecl) node() {}
func (*FnDecl) item() {}

func (*EntryDecl) node() {}
func (*EntryDecl) item() {}

func (*HostDecl) node() {}
func (*HostDecl) item() {}

func (*EnvDecl) node() {}
func (*EnvDecl) item() {}

func (*BoundaryDecl) node() {}
func (*BoundaryDecl) item() {}

func (*Block) node() {}

func (*Let) node() {}
func (*Let) stmt() {}

func (*Return) node() {}
func (*Return) stmt() {}

func (*If) node() {}
func (*If) stmt() {}

func (*Match) node() {}
func (*Match) stmt() {}

func (*ExprStmt) node() {}
func (*ExprStmt) stmt() {}

func (*Ident) node() {}
func (*Ident) expr() {}

func (*IntLit) node() {}
func (*IntLit) expr() {}

func (*StrLit) node() {}
func (*StrLit) expr() {}

func (*BytesLit) node() {}
func (*BytesLit) expr() {}

func (*BoolLit) node() {}
func (*BoolLit) expr() {}

func (*UnitLit) node() {}
func (*UnitLit) expr() {}

func (*Call) node() {}
func (*Call) expr() {}

func (*Construct) node() {}
func (*Construct) expr() {}

func (*Select) node() {}
func (*Select) expr() {}

func (*Binary) node() {}
func (*Binary) expr() {}

func (*Propagate) node() {}
func (*Propagate) expr() {}

func (*Named) node() {}
func (*Named) typ()  {}

func (*Union) node() {}
func (*Union) typ()  {}
