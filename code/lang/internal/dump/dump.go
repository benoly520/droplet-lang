// Package dump 把语法树转成可序列化的通用值，供 `droplet parse` 输出 JSON。
package dump

import (
	"fmt"

	"droplet/internal/ast"
	"droplet/internal/token"
)

// Program 转储整个编译单元。
func Program(p *ast.Program) any {
	items := make([]any, 0, len(p.Items))
	for _, it := range p.Items {
		items = append(items, item(it))
	}
	file := p.File
	if file == "" {
		file = "<unit>"
	}
	return obj("Program", pos(p.Items), "file", file, "items", items)
}

func posOf(p token.Pos) map[string]any {
	return map[string]any{"file": p.File, "line": p.Line, "col": p.Col}
}

// pos 取第一个节点的位置作为容器位置；空列表返回 nil。
func pos(items []ast.Item) token.Pos {
	if len(items) == 0 {
		return token.Pos{}
	}
	switch n := items[0].(type) {
	case *ast.RecordType:
		return n.Pos
	case *ast.SumType:
		return n.Pos
	case *ast.FnDecl:
		return n.Pos
	case *ast.HostDecl:
		return n.Pos
	case *ast.EnvDecl:
		return n.Pos
	case *ast.BoundaryDecl:
		return n.Pos
	}
	return token.Pos{}
}

// obj 组装一个带 kind 与 pos 的节点对象。
func obj(kind string, p token.Pos, kv ...any) map[string]any {
	m := map[string]any{"kind": kind}
	if p.Line > 0 {
		m["pos"] = posOf(p)
	}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func items(in []ast.Item) []any {
	out := make([]any, 0, len(in))
	for _, it := range in {
		out = append(out, item(it))
	}
	return out
}

func item(it ast.Item) any {
	switch n := it.(type) {
	case *ast.RecordType:
		return obj("RecordType", n.Pos, "name", n.Name, "fields", fields(n.Fields))
	case *ast.SumType:
		return obj("SumType", n.Pos, "name", n.Name, "variants", variants(n.Variants))
	case *ast.FnDecl:
		return obj("FnDecl", n.Pos, "name", n.Name, "entry", n.Entry, "params", params(n.Params),
			"ret", typ(n.Ret), "intents", intents(n.Intents), "body", block(n.Body))
	case *ast.HostDecl:
		return obj("HostDecl", n.Pos, "name", n.Name, "fns", fns(n.Fns))
	case *ast.EnvDecl:
		return obj("EnvDecl", n.Pos, "name", n.Name, "fields", envFields(n.Fields))
	case *ast.BoundaryDecl:
		return obj("BoundaryDecl", n.Pos, "name", n.Name, "version", n.Version,
			"methods", methods(n.Methods))
	}
	return obj("UnknownItem", token.Pos{}, "type", fmt.Sprintf("%T", it))
}

func fns(in []*ast.FnDecl) []any {
	out := make([]any, 0, len(in))
	for _, f := range in {
		out = append(out, item(f))
	}
	return out
}

func fields(in []ast.Field) []any {
	out := make([]any, 0, len(in))
	for _, f := range in {
		out = append(out, obj("Field", f.Pos, "name", f.Name, "type", typ(f.Type)))
	}
	return out
}

func variants(in []ast.Variant) []any {
	out := make([]any, 0, len(in))
	for _, v := range in {
		out = append(out, obj("Variant", v.Pos, "name", v.Name, "fields", fields(v.Fields)))
	}
	return out
}

func params(in []ast.Param) []any {
	out := make([]any, 0, len(in))
	for _, p := range in {
		out = append(out, obj("Param", p.Pos, "name", p.Name, "type", typ(p.Type)))
	}
	return out
}

func envFields(in []ast.EnvField) []any {
	out := make([]any, 0, len(in))
	for _, f := range in {
		out = append(out, obj("EnvField", f.Pos, "name", f.Name,
			"type", typ(f.Type), "default", expr(f.Default)))
	}
	return out
}

func methods(in []*ast.BndMethod) []any {
	out := make([]any, 0, len(in))
	for _, m := range in {
		out = append(out, obj("BndMethod", m.Pos, "name", m.Name, "params", params(m.Params),
			"ret", typ(m.Ret), "idempotent", m.Idempotent, "intents", intents(m.Intents)))
	}
	return out
}

func intents(in []ast.Intent) []any {
	out := make([]any, 0, len(in))
	for _, it := range in {
		// 字段名不能叫 kind：obj 已经用 kind 存节点类型，再写一次会把它覆盖成
		// invariant / requires 这类规则名。
		out = append(out, obj("Intent", it.Pos, "rule", it.Kind, "text", it.Text,
			"names", it.Names, "expr", expr(it.Expr)))
	}
	return out
}

func block(b *ast.Block) any {
	if b == nil {
		return nil
	}
	stmts := make([]any, 0, len(b.Stmts))
	for _, s := range b.Stmts {
		stmts = append(stmts, stmt(s))
	}
	return obj("Block", b.Pos, "stmts", stmts)
}

func stmt(s ast.Stmt) any {
	switch n := s.(type) {
	case *ast.Let:
		return obj("Let", n.Pos, "name", n.Name, "type", typ(n.Type), "value", expr(n.Value))
	case *ast.Return:
		return obj("Return", n.Pos, "value", expr(n.Value))
	case *ast.If:
		return obj("If", n.Pos, "cond", expr(n.Cond), "then", block(n.Then), "else", node(n.Else))
	case *ast.Match:
		arms := make([]any, 0, len(n.Arms))
		for _, a := range n.Arms {
			arms = append(arms, obj("Arm", a.Pos,
				"pattern", pattern(a.Pattern), "body", node(a.Body)))
		}
		return obj("Match", n.Pos, "subject", expr(n.Subject), "arms", arms)
	case *ast.ExprStmt:
		return obj("ExprStmt", n.Pos, "x", expr(n.X))
	}
	return obj("UnknownStmt", token.Pos{}, "type", fmt.Sprintf("%T", s))
}

func pattern(p ast.Pattern) map[string]any {
	m := obj("Pattern", p.Pos, "name", p.Name, "fields", p.Fields)
	if p.Wildcard {
		m["wildcard"] = true
	}
	return m
}

// node 转储任意节点（Block / Expr / If），用于 match 分支体与 else 分支。
func node(n ast.Node) any {
	switch v := n.(type) {
	case nil:
		return nil
	case *ast.Block:
		return block(v)
	case *ast.If:
		return stmt(v)
	case ast.Expr:
		return expr(v)
	}
	return obj("UnknownNode", token.Pos{}, "type", fmt.Sprintf("%T", n))
}

func expr(e ast.Expr) any {
	switch n := e.(type) {
	case nil:
		return nil
	case *ast.Ident:
		return obj("Ident", n.Pos, "name", n.Name)
	case *ast.IntLit:
		return obj("IntLit", n.Pos, "value", n.Value)
	case *ast.StrLit:
		return obj("StrLit", n.Pos, "value", n.Value)
	case *ast.BytesLit:
		return obj("BytesLit", n.Pos, "value", string(n.Value))
	case *ast.BoolLit:
		return obj("BoolLit", n.Pos, "value", n.Value)
	case *ast.UnitLit:
		return obj("UnitLit", n.Pos)
	case *ast.Call:
		args := make([]any, 0, len(n.Args))
		for _, a := range n.Args {
			args = append(args, expr(a))
		}
		return obj("Call", n.Pos, "callee", expr(n.Callee), "args", args)
	case *ast.Construct:
		fs := make([]any, 0, len(n.Fields))
		for _, f := range n.Fields {
			fs = append(fs, obj("NamedExpr", f.Pos, "name", f.Name, "value", expr(f.Value)))
		}
		return obj("Construct", n.Pos, "type", n.TypeName, "variant", n.Variant, "fields", fs)
	case *ast.Select:
		return obj("Select", n.Pos, "target", expr(n.Target), "field", n.Field)
	case *ast.Binary:
		return obj("Binary", n.Pos, "op", n.Op, "left", expr(n.Left), "right", expr(n.Right))
	case *ast.Propagate:
		return obj("Propagate", n.Pos, "inner", expr(n.Inner))
	}
	return obj("UnknownExpr", token.Pos{}, "type", fmt.Sprintf("%T", e))
}

func typ(t ast.Type) any {
	switch n := t.(type) {
	case nil:
		return nil
	case *ast.Named:
		return obj("Named", n.Pos, "name", n.Name)
	case *ast.Union:
		its := make([]any, 0, len(n.Items))
		for _, it := range n.Items {
			its = append(its, typ(it))
		}
		return obj("Union", n.Pos, "items", its)
	}
	return obj("UnknownType", token.Pos{}, "type", fmt.Sprintf("%T", t))
}
