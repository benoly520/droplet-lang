// Package parse 把词法单元组装为语法树。
//
// 采用递归下降 + panic/recover 做错误短路：遇到第一个语法错误就停止，
// 保证报错位置准确、不级联。
package parse

import (
	"fmt"
	"strconv"
	"strings"

	"droplet/internal/ast"
	"droplet/internal/lex"
	"droplet/internal/token"
)

// bail 用于把解析错误从深处抛到 File 边界。
type bail struct{ err error }

// Parser 持有单个文件的词法单元序列。
type Parser struct {
	toks []token.Token
	i    int
	file string
}

// File 解析单个源文件，返回其语法树。
func File(file, src string) (*ast.Program, error) {
	toks, err := lex.New(file, src).Tokens()
	if err != nil {
		return nil, err
	}
	p := &Parser{toks: toks, file: file}
	return p.parseProgram()
}

func (p *Parser) parseProgram() (prog *ast.Program, err error) {
	defer func() {
		if r := recover(); r != nil {
			if b, ok := r.(bail); ok {
				err = b.err
				return
			}
			panic(r)
		}
	}()

	prog = &ast.Program{File: p.file}
	for !p.at(token.EOF) {
		prog.Items = append(prog.Items, p.parseItem())
	}
	return prog, nil
}

// ---------- 基础辅助 ----------

func (p *Parser) cur() token.Token { return p.toks[p.i] }

func (p *Parser) at(k token.Kind) bool { return p.toks[p.i].Kind == k }

// peekKind 返回相对当前位置第 n 个 token 的类别（0 为当前）。
func (p *Parser) peekKind(n int) token.Kind {
	if p.i+n >= len(p.toks) {
		return token.EOF
	}
	return p.toks[p.i+n].Kind
}

func (p *Parser) advance() token.Token {
	t := p.toks[p.i]
	if t.Kind != token.EOF {
		p.i++
	}
	return t
}

func (p *Parser) fail(pos token.Pos, format string, a ...any) {
	panic(bail{fmt.Errorf("%s: %s", pos, fmt.Sprintf(format, a...))})
}

func (p *Parser) expect(k token.Kind, what string) token.Token {
	if !p.at(k) {
		p.fail(p.cur().Pos, "E0006: 期望 %s，%s", what, p.got())
	}
	return p.advance()
}

func (p *Parser) expectIdent(what string) token.Token {
	if !p.at(token.Ident) {
		p.fail(p.cur().Pos, "E0006: 期望 %s，%s", what, p.got())
	}
	return p.advance()
}

// got 描述当前位置的 token，对 EOF 给出可读说法。
func (p *Parser) got() string {
	t := p.cur()
	if t.Kind == token.EOF {
		return "但已到文件结束"
	}
	return fmt.Sprintf("得到 %q", t.Text)
}

// ---------- 顶层 ----------

func (p *Parser) parseItem() ast.Item {
	switch p.cur().Kind {
	case token.KwType:
		return p.parseTypeDecl()
	case token.KwFn:
		return p.parseFn(true)
	case token.KwEntry:
		return p.parseEntry()
	case token.KwHost:
		return p.parseHost()
	case token.KwEnv:
		return p.parseEnv()
	case token.KwBoundary:
		return p.parseBoundary()
	default:
		p.fail(p.cur().Pos, "E0007: 顶层只允许 type/fn/entry/host/env/boundary，得到 %q", p.cur().Text)
		return nil
	}
}

func (p *Parser) parseTypeDecl() ast.Item {
	start := p.advance().Pos // type
	name := p.expectIdent("类型名").Text

	// `=` 开头是 sum，`{` 开头是 record，用这一个符号区分，不新增关键字。
	if p.at(token.Assign) {
		p.advance()
		variants := []ast.Variant{p.parseVariant()}
		for p.at(token.Pipe) {
			p.advance()
			variants = append(variants, p.parseVariant())
		}
		return &ast.SumType{Pos: start, Name: name, Variants: variants}
	}
	return &ast.RecordType{Pos: start, Name: name, Fields: p.parseFields()}
}

func (p *Parser) parseFields() []ast.Field {
	p.expect(token.LBrace, "{")
	fields := []ast.Field{}
	for !p.at(token.RBrace) {
		f := ast.Field{Pos: p.cur().Pos, Name: p.expectIdent("字段名").Text}
		p.expect(token.Colon, ":")
		f.Type = p.parseType()
		fields = append(fields, f)
		if p.at(token.Comma) {
			p.advance()
			continue
		}
		break
	}
	p.expect(token.RBrace, "}")
	return fields
}

func (p *Parser) parseVariant() ast.Variant {
	v := ast.Variant{Pos: p.cur().Pos, Name: p.expectIdent("变体名").Text}
	if p.at(token.LBrace) {
		v.Fields = p.parseFields()
	}
	return v
}

// ---------- 函数与入口 ----------

func (p *Parser) parseFn(allowBody bool) *ast.FnDecl {
	start := p.advance().Pos // fn
	fn := &ast.FnDecl{Pos: start, Name: p.expectIdent("函数名").Text}
	p.expect(token.LParen, "(")
	fn.Params = p.parseParams()
	p.expect(token.RParen, ")")
	p.expect(token.Arrow, "->")
	fn.Ret = p.parseType()
	fn.Intents = p.parseIntents()
	if allowBody && p.at(token.LBrace) {
		fn.Body = p.parseBlock()
	}
	return fn
}

func (p *Parser) parseParams() []ast.Param {
	params := []ast.Param{}
	for !p.at(token.RParen) {
		pr := ast.Param{Pos: p.cur().Pos, Name: p.expectIdent("参数名").Text}
		p.expect(token.Colon, ":")
		pr.Type = p.parseType()
		params = append(params, pr)
		if p.at(token.Comma) {
			p.advance()
			continue
		}
		break
	}
	return params
}

// parseIntents 解析零个或多个意图子句，遇到其它 token 即停止。
func (p *Parser) parseIntents() []ast.Intent {
	out := []ast.Intent{}
	for {
		switch p.cur().Kind {
		case token.CtxRequires:
			pos := p.advance().Pos
			out = append(out, ast.Intent{Pos: pos, Kind: "requires", Expr: p.parseExpr()})
		case token.CtxInvariant:
			pos := p.advance().Pos
			s := p.expect(token.Str, "字符串字面量")
			out = append(out, ast.Intent{Pos: pos, Kind: "invariant", Text: s.Text})
		case token.CtxExample:
			pos := p.advance().Pos
			out = append(out, ast.Intent{Pos: pos, Kind: "example", Expr: p.parseExpr()})
		case token.CtxEffects:
			pos := p.advance().Pos
			p.expect(token.LBrace, "{")
			names := []string{}
			for !p.at(token.RBrace) {
				names = append(names, p.expectIdent("效果名").Text)
				if p.at(token.Comma) {
					p.advance()
					continue
				}
				break
			}
			p.expect(token.RBrace, "}")
			out = append(out, ast.Intent{Pos: pos, Kind: "effects", Names: names})
		case token.CtxCap:
			pos := p.advance().Pos
			p.expectIdent("mem")
			p.expect(token.Lt, "<")
			n := p.expect(token.Int, "整数")
			unit := p.expectIdent("单位（KiB/MiB）")
			p.expect(token.Gt, ">")
			out = append(out, ast.Intent{Pos: pos, Kind: "cap", Text: n.Text + unit.Text})
		case token.CtxIdempotency:
			pos := p.advance().Pos
			p.expect(token.CtxBy, "by")
			key := p.expectIdent("幂等键")
			out = append(out, ast.Intent{Pos: pos, Kind: "idempotent", Text: key.Text})
		default:
			return out
		}
	}
}

func (p *Parser) parseEntry() ast.Item {
	start := p.advance().Pos
	e := &ast.EntryDecl{Pos: start, Name: p.expectIdent("entry 名").Text}
	p.expect(token.LBrace, "{")
	for p.at(token.KwFn) {
		e.Fns = append(e.Fns, p.parseFn(true))
	}
	p.expect(token.RBrace, "}")
	return e
}

func (p *Parser) parseHost() ast.Item {
	start := p.advance().Pos
	h := &ast.HostDecl{Pos: start, Name: p.expectIdent("host 名").Text}
	p.expect(token.LBrace, "{")
	for p.at(token.KwFn) {
		h.Fns = append(h.Fns, p.parseFn(false))
	}
	p.expect(token.RBrace, "}")
	return h
}

func (p *Parser) parseEnv() ast.Item {
	start := p.advance().Pos
	e := &ast.EnvDecl{Pos: start, Name: p.expectIdent("env 名").Text}
	p.expect(token.LBrace, "{")
	for !p.at(token.RBrace) {
		f := ast.EnvField{Pos: p.cur().Pos, Name: p.expectIdent("配置键").Text}
		p.expect(token.Colon, ":")
		f.Type = p.parseType()
		if p.at(token.Assign) {
			p.advance()
			f.Default = p.parseExpr()
		}
		e.Fields = append(e.Fields, f)
		if p.at(token.Comma) {
			p.advance()
			continue
		}
		break
	}
	p.expect(token.RBrace, "}")
	return e
}

func (p *Parser) parseBoundary() ast.Item {
	start := p.advance().Pos
	b := &ast.BoundaryDecl{Pos: start, Name: p.expectIdent("boundary 名").Text}

	p.expect(token.At, "@")
	verTok := p.expectIdent("版本号（形如 v1）")
	if !strings.HasPrefix(verTok.Text, "v") {
		p.fail(verTok.Pos, "E0009: 版本号必须形如 v1，得到 %q", verTok.Text)
	}
	n, err := strconv.Atoi(verTok.Text[1:])
	if err != nil || n < 1 {
		p.fail(verTok.Pos, "E0009: 非法版本号 %q", verTok.Text)
	}
	b.Version = n

	p.expect(token.LBrace, "{")
	for !p.at(token.RBrace) {
		m := &ast.BndMethod{Pos: p.cur().Pos, Name: p.expectIdent("方法名").Text}
		p.expect(token.LParen, "(")
		m.Params = p.parseParams()
		p.expect(token.RParen, ")")
		p.expect(token.Arrow, "->")
		m.Ret = p.parseType()
		if p.at(token.CtxWith) {
			p.advance()
			p.expect(token.CtxIdempotency, "idempotency")
			m.Idempotent = true
		}
		m.Intents = p.parseIntents()
		b.Methods = append(b.Methods, m)
	}
	p.expect(token.RBrace, "}")
	return b
}

// ---------- 类型 ----------

func (p *Parser) parseType() ast.Type {
	start := p.cur().Pos
	first := p.parseAtomType()
	if !p.at(token.Pipe) {
		return first
	}
	items := []ast.Type{first}
	for p.at(token.Pipe) {
		p.advance()
		items = append(items, p.parseAtomType())
	}
	return &ast.Union{Pos: start, Items: items}
}

func (p *Parser) parseAtomType() ast.Type {
	t := p.expectIdent("类型名")
	return &ast.Named{Pos: t.Pos, Name: t.Text}
}

// ---------- 语句 ----------

func (p *Parser) parseBlock() *ast.Block {
	start := p.expect(token.LBrace, "{").Pos
	b := &ast.Block{Pos: start}
	for !p.at(token.RBrace) && !p.at(token.EOF) {
		b.Stmts = append(b.Stmts, p.parseStmt())
	}
	p.expect(token.RBrace, "}")
	return b
}

func (p *Parser) parseStmt() ast.Stmt {
	switch p.cur().Kind {
	case token.KwLet:
		pos := p.advance().Pos
		name := p.expectIdent("变量名").Text
		var typ ast.Type
		if p.at(token.Colon) {
			p.advance()
			typ = p.parseType()
		}
		p.expect(token.Assign, "=")
		return &ast.Let{Pos: pos, Name: name, Type: typ, Value: p.parseExpr()}

	case token.KwReturn:
		pos := p.advance().Pos
		if p.stmtEnd() {
			return &ast.Return{Pos: pos}
		}
		return &ast.Return{Pos: pos, Value: p.parseExpr()}

	case token.KwIf:
		return p.parseIf()

	case token.KwMatch:
		return p.parseMatch()

	default:
		return &ast.ExprStmt{Pos: p.cur().Pos, X: p.parseExpr()}
	}
}

// stmtEnd 判断当前位置是否已经是语句的天然结束处。
func (p *Parser) stmtEnd() bool {
	switch p.cur().Kind {
	case token.RBrace, token.EOF, token.KwLet, token.KwReturn, token.KwIf, token.KwMatch:
		return true
	}
	return false
}

func (p *Parser) parseIf() ast.Stmt {
	pos := p.advance().Pos // if
	n := &ast.If{Pos: pos, Cond: p.parseExpr(), Then: p.parseBlock()}
	if p.at(token.CtxElse) {
		p.advance()
		if p.at(token.KwIf) {
			n.Else = p.parseIf()
		} else {
			n.Else = p.parseBlock()
		}
	}
	return n
}

func (p *Parser) parseMatch() ast.Stmt {
	pos := p.advance().Pos // match
	m := &ast.Match{Pos: pos, Subject: p.parseExpr()}
	p.expect(token.LBrace, "{")
	for !p.at(token.RBrace) {
		pat := p.parsePattern()
		p.expect(token.FatArrow, "=>")
		var body ast.Node
		if p.at(token.LBrace) {
			body = p.parseBlock()
		} else {
			body = p.parseExpr()
		}
		m.Arms = append(m.Arms, ast.Arm{Pos: pat.Pos, Pattern: pat, Body: body})
		if p.at(token.Comma) {
			p.advance()
			continue
		}
		break
	}
	p.expect(token.RBrace, "}")
	return m
}

func (p *Parser) parsePattern() ast.Pattern {
	if p.at(token.Ident) && p.cur().Text == "_" {
		return ast.Pattern{Pos: p.advance().Pos, Wildcard: true}
	}
	pat := ast.Pattern{Pos: p.cur().Pos, Name: p.expectIdent("模式名").Text}
	if p.at(token.LBrace) {
		p.advance()
		for !p.at(token.RBrace) {
			pat.Fields = append(pat.Fields, p.expectIdent("绑定名").Text)
			if p.at(token.Comma) {
				p.advance()
				continue
			}
			break
		}
		p.expect(token.RBrace, "}")
	}
	return pat
}

// ---------- 表达式 ----------

func (p *Parser) parseExpr() ast.Expr {
	left := p.parseUnary()
	for {
		op, ok := binOpOf(p.cur().Kind)
		if !ok {
			return left
		}
		pos := p.advance().Pos
		right := p.parseUnary()
		left = &ast.Binary{Pos: pos, Op: op, Left: left, Right: right}
	}
}

func (p *Parser) parseUnary() ast.Expr {
	e := p.parsePrimary()
	for p.at(token.Bang) {
		e = &ast.Propagate{Pos: p.advance().Pos, Inner: e}
	}
	return e
}

func (p *Parser) parsePrimary() ast.Expr {
	tok := p.cur()
	var e ast.Expr

	switch tok.Kind {
	case token.Int:
		p.advance()
		e = &ast.IntLit{Pos: tok.Pos, Value: tok.Text}
	case token.Str:
		p.advance()
		e = &ast.StrLit{Pos: tok.Pos, Value: tok.Text}
	case token.Bytes:
		p.advance()
		e = &ast.BytesLit{Pos: tok.Pos, Value: []byte(tok.Text)}
	case token.LParen:
		p.advance()
		e = p.parseExpr()
		p.expect(token.RParen, ")")
	case token.Ident:
		p.advance()
		switch tok.Text {
		case "true":
			e = &ast.BoolLit{Pos: tok.Pos, Value: true}
		case "false":
			e = &ast.BoolLit{Pos: tok.Pos, Value: false}
		case "Unit":
			e = &ast.UnitLit{Pos: tok.Pos}
		default:
			e = &ast.Ident{Pos: tok.Pos, Name: tok.Text}
		}
	default:
		p.fail(tok.Pos, "E0008: 期望表达式，得到 %q", tok.Text)
		return nil
	}

	return p.parsePostfix(e)
}

// parsePostfix 处理 `.field`、`(args)`、`Name { fields }` 三类后缀。
func (p *Parser) parsePostfix(e ast.Expr) ast.Expr {
	for {
		switch p.cur().Kind {
		case token.Dot:
			p.advance()
			e = &ast.Select{Pos: p.cur().Pos, Target: e, Field: p.expectIdent("字段名").Text}

		case token.LParen:
			pos := p.cur().Pos
			p.advance()
			args := []ast.Expr{}
			for !p.at(token.RParen) {
				args = append(args, p.parseExpr())
				if p.at(token.Comma) {
					p.advance()
					continue
				}
				break
			}
			p.expect(token.RParen, ")")
			e = &ast.Call{Pos: pos, Callee: e, Args: args}

		case token.LBrace:
			// 仅当形如 `Name { field: ... }` 时才当作构造，
			// 避免把 `if x {` 的 block 误判为构造。
			id, ok := e.(*ast.Ident)
			if !ok || !p.looksLikeFields() {
				return e
			}
			p.advance() // {
			fields := []ast.NamedExpr{}
			for !p.at(token.RBrace) {
				f := ast.NamedExpr{Pos: p.cur().Pos, Name: p.expectIdent("字段名").Text}
				p.expect(token.Colon, ":")
				f.Value = p.parseExpr()
				fields = append(fields, f)
				if p.at(token.Comma) {
					p.advance()
					continue
				}
				break
			}
			p.expect(token.RBrace, "}")
			e = &ast.Construct{Pos: id.Pos, TypeName: id.Name, Fields: fields}

		default:
			return e
		}
	}
}

// looksLikeFields 在当前位置为 `{` 时，判断其后是否形如 `Ident :`。
func (p *Parser) looksLikeFields() bool {
	return p.peekKind(1) == token.Ident && p.peekKind(2) == token.Colon
}

func binOpOf(k token.Kind) (string, bool) {
	switch k {
	case token.EqEq:
		return "==", true
	case token.NotEq:
		return "!=", true
	case token.Lt:
		return "<", true
	case token.Le:
		return "<=", true
	case token.Gt:
		return ">", true
	case token.Ge:
		return ">=", true
	case token.And:
		return "&&", true
	case token.Or:
		return "||", true
	case token.Plus:
		return "+", true
	case token.Minus:
		return "-", true
	case token.Star:
		return "*", true
	case token.Slash:
		return "/", true
	case token.Percent:
		return "%", true
	}
	return "", false
}
