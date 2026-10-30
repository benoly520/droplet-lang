// Package contract 把语法树里的 boundary / env / 函数签名导出成 JSON，
// 并把 checker 算出的变体编号一并带上。
//
// 这份 JSON 是「契约先行」的产物：消费方按它写客户端，服务端按它写实现，
// 两边都改不动对方不认的东西。
package contract

import (
	"sort"
	"strconv"
	"strings"

	"droplet/internal/ast"
	"droplet/internal/check"
	"droplet/internal/token"
)

// Doc 是导出的契约文档。
type Doc struct {
	Boundaries []Boundary `json:"boundaries"`
	Functions  []Function `json:"functions"`
	Tags       []Tag      `json:"tags"`
	Envs       []EnvGroup `json:"envs,omitempty"`
}

// Boundary 是一个对外接口声明。
type Boundary struct {
	Name    string   `json:"name"`
	Version int      `json:"version"`
	Methods []Method `json:"methods"`
}

// Method 是契约里的一个方法。带路由的方法同时是一条 HTTP 端点：
// Verb + Path 是它对外的地址（D3 起由声明合成，不再靠 handler 里的字符串比较），
// Impl 指向实现函数，Precondition 表示进 handler 之前要比对 If-Match。
type Method struct {
	Name         string   `json:"name"`
	Params       []Field  `json:"params"`
	Ret          string   `json:"ret"`
	Idempotent   bool     `json:"idempotent"`
	Verb         string   `json:"verb,omitempty"`
	Path         string   `json:"path,omitempty"`
	Impl         string   `json:"impl,omitempty"`
	Precondition bool     `json:"precondition,omitempty"`
	Intents      []Intent `json:"intents"`
	Pos          string   `json:"pos"`
}

// Field 是参数或配置项。
type Field struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Default string `json:"default,omitempty"`
}

// EnvGroup 是一个 env 块。Prefix 是环境变量前缀，运维对照的就这一行。
type EnvGroup struct {
	Name   string `json:"name"`
	Prefix string `json:"prefix"`
	Keys   []Env  `json:"keys"`
}

// Env 是一个配置键。required 为 true 代表缺失即启动失败；
// var 是它的读取面 —— 源码里不出现这个名字，但部署的人必须看得见（SPEC §5.7）。
type Env struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Var      string `json:"var"`
	Default  string `json:"default,omitempty"`
	Required bool   `json:"required"`
}

// Function 是一个函数签名；Entry 为 true 表示它是入口。
// Hosts 列出参数里出现的 host 组名，用来说明这个函数会碰到哪些能力。
type Function struct {
	Name   string   `json:"name"`
	Params []Field  `json:"params"`
	Ret    string   `json:"ret"`
	Entry  bool     `json:"entry"`
	Hosts  []string `json:"hosts,omitempty"`
	Pos    string   `json:"pos"`
}

// Intent 保留意图声明的原文，不做语义解释。
// Text 是字符串原文，Names 是 `effects a, b` 里的标识符；
// requires / example 的表达式暂不导出（没有表达式打印器）。
type Intent struct {
	Kind  string   `json:"kind"`
	Text  string   `json:"text,omitempty"`
	Names []string `json:"names,omitempty"`
}

// Tag 是 sum 变体的编号，`!` 传播与 HTTP 状态码映射都依赖它。
// 表里只有真实变体；成功值固定为 0，不占一行。
type Tag struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

// okTag 是 checker 给成功值起的内部名字，不属于契约里的变体。
const okTag = "<ok>"

// Build 从语法树导出契约。res 可以为 nil（此时不导出 tag 表）。
func Build(prog *ast.Program, res *check.Result) Doc {
	doc := Doc{
		Boundaries: []Boundary{},
		Functions:  []Function{},
		Tags:       []Tag{},
	}

	hostSet := map[string]bool{}
	if res != nil {
		for name := range res.Hosts {
			hostSet[name] = true
		}
	}

	for _, it := range prog.Items {
		switch n := it.(type) {
		case *ast.BoundaryDecl:
			doc.Boundaries = append(doc.Boundaries, boundaryOf(n))
		case *ast.FnDecl:
			doc.Functions = append(doc.Functions, functionOf(n, n.Entry, hostSet))
		case *ast.EnvDecl:
			doc.Envs = append(doc.Envs, envOf(n))
		}
	}

	if res != nil {
		names := make([]string, 0, len(res.Tags))
		for name := range res.Tags {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if name == okTag {
				continue
			}
			doc.Tags = append(doc.Tags, Tag{Name: name, Value: res.Tags[name]})
		}
	}
	return doc
}

// envOf 导出一个 env 块。前缀与变量名的规则只定在 check 里一处，这里只负责呈现。
func envOf(n *ast.EnvDecl) EnvGroup {
	g := EnvGroup{Name: n.Name, Prefix: strings.ToUpper(n.Name), Keys: make([]Env, 0, len(n.Fields))}
	for _, f := range n.Fields {
		g.Keys = append(g.Keys, Env{
			Name:     f.Name,
			Type:     typeOf(f.Type),
			Var:      check.EnvVar(g.Prefix, f.Name),
			Default:  exprOf(f.Default),
			Required: f.Default == nil,
		})
	}
	return g
}

func boundaryOf(n *ast.BoundaryDecl) Boundary {
	b := Boundary{Name: n.Name, Version: n.Version, Methods: make([]Method, 0, len(n.Methods))}
	for _, m := range n.Methods {
		method := Method{
			Name:         m.Name,
			Params:       fieldsOf(m.Params),
			Ret:          typeOf(m.Ret),
			Idempotent:   m.Idempotent,
			Verb:         strings.ToUpper(m.Verb),
			Path:         m.Path,
			Impl:         m.Impl,
			Precondition: m.Precondition,
			Intents:      intentsOf(m.Intents),
			Pos:          posOf(m.Pos),
		}
		b.Methods = append(b.Methods, method)
	}
	return b
}

func functionOf(n *ast.FnDecl, entry bool, hosts map[string]bool) Function {
	f := Function{
		Name:   n.Name,
		Params: fieldsOf(n.Params),
		Ret:    typeOf(n.Ret),
		Entry:  entry,
		Hosts:  hostsOf(n.Params, hosts),
		Pos:    posOf(n.Pos),
	}
	return f
}

// hostsOf 挑出参数类型里属于 host 声明的那几个，按参数顺序去重。
func hostsOf(params []ast.Param, hosts map[string]bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range params {
		named, ok := p.Type.(*ast.Named)
		if !ok || !hosts[named.Name] || seen[named.Name] {
			continue
		}
		seen[named.Name] = true
		out = append(out, named.Name)
	}
	return out
}

func fieldsOf(params []ast.Param) []Field {
	out := make([]Field, 0, len(params))
	for _, p := range params {
		out = append(out, Field{Name: p.Name, Type: typeOf(p.Type)})
	}
	return out
}

func intentsOf(items []ast.Intent) []Intent {
	out := make([]Intent, 0, len(items))
	for _, it := range items {
		out = append(out, Intent{Kind: it.Kind, Text: it.Text, Names: it.Names})
	}
	return out
}

func typeOf(t ast.Type) string {
	switch n := t.(type) {
	case nil:
		return ""
	case *ast.Named:
		return n.Name
	case *ast.Union:
		s := ""
		for i, it := range n.Items {
			if i > 0 {
				s += " | "
			}
			s += typeOf(it)
		}
		return s
	}
	return "?"
}

// exprOf 只渲染能确定写法的字面量；其余返回空串，不臆造源码原文。
func exprOf(e ast.Expr) string {
	switch n := e.(type) {
	case nil:
		return ""
	case *ast.StrLit:
		return strconv.Quote(n.Value)
	case *ast.IntLit:
		return n.Value
	case *ast.BoolLit:
		if n.Value {
			return "true"
		}
		return "false"
	}
	return ""
}

func posOf(p token.Pos) string {
	if p.Line == 0 {
		return ""
	}
	return p.String()
}
