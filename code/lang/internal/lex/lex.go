// Package lex 把 Droplet 源码切分为词法单元。
package lex

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"droplet/internal/token"
)

// Lexer 是单文件、单遍扫描的词法分析器。
type Lexer struct {
	file string
	src  string
	off  int
	line int
	col  int
}

// New 创建词法分析器，行列从 1 开始。
func New(file, src string) *Lexer {
	return &Lexer{file: file, src: src, line: 1, col: 1}
}

// Tokens 扫描全部词法单元，末尾总有一个 EOF。
func (l *Lexer) Tokens() ([]token.Token, error) {
	var out []token.Token
	for {
		l.skipSpace()
		p := l.pos()
		r := l.peek()
		if r == 0 {
			out = append(out, token.Token{Kind: token.EOF, Pos: p})
			return out, nil
		}

		switch {
		case r == 'b' && l.peekAt(1) == '"':
			// 字节字面量 b"..."，必须先于标识符分支判断。
			l.next()
			s, err := l.scanQuoted(true)
			if err != nil {
				return nil, err
			}
			out = append(out, token.Token{Kind: token.Bytes, Text: s, Pos: p})

		case isIdentStart(r):
			var sb strings.Builder
			for isIdentPart(l.peek()) {
				sb.WriteRune(l.next())
			}
			text := sb.String()
			if k, ok := token.Lookup(text); ok {
				out = append(out, token.Token{Kind: k, Text: text, Pos: p})
			} else {
				out = append(out, token.Token{Kind: token.Ident, Text: text, Pos: p})
			}

		case r >= '0' && r <= '9':
			var sb strings.Builder
			for c := l.peek(); c >= '0' && c <= '9'; c = l.peek() {
				sb.WriteRune(l.next())
			}
			out = append(out, token.Token{Kind: token.Int, Text: sb.String(), Pos: p})

		case r == '"':
			s, err := l.scanQuoted(false)
			if err != nil {
				return nil, err
			}
			out = append(out, token.Token{Kind: token.Str, Text: s, Pos: p})

		default:
			tok, err := l.scanSymbol(p)
			if err != nil {
				return nil, err
			}
			out = append(out, tok)
		}
	}
}

func (l *Lexer) pos() token.Pos {
	return token.Pos{File: l.file, Line: l.line, Col: l.col}
}

// peek 返回当前 rune，0 表示文件结束。
func (l *Lexer) peek() rune {
	if l.off >= len(l.src) {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(l.src[l.off:])
	return r
}

// peekAt 返回相对当前位置第 n 个 rune（0 为当前）。
func (l *Lexer) peekAt(n int) rune {
	off := l.off
	for i := 0; i < n; i++ {
		if off >= len(l.src) {
			return 0
		}
		_, size := utf8.DecodeRuneInString(l.src[off:])
		off += size
	}
	if off >= len(l.src) {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(l.src[off:])
	return r
}

func (l *Lexer) next() rune {
	if l.off >= len(l.src) {
		return 0
	}
	r, size := utf8.DecodeRuneInString(l.src[l.off:])
	l.off += size
	if r == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return r
}

// skipSpace 跳过空白与行注释。
func (l *Lexer) skipSpace() {
	for {
		r := l.peek()
		switch {
		case r == ' ' || r == '\t' || r == '\r' || r == '\n':
			l.next()
		case r == '/' && l.peekAt(1) == '/':
			for l.peek() != '\n' && l.peek() != 0 {
				l.next()
			}
		default:
			return
		}
	}
}

// scanQuoted 扫描字符串或字节字面量，进入时当前字符必须是 '"'。
func (l *Lexer) scanQuoted(bytesMode bool) (string, error) {
	start := l.pos()
	l.next() // 吃掉起始引号
	var sb strings.Builder
	for {
		r := l.peek()
		if r == 0 {
			return "", fmt.Errorf("%s: E0003: 字面量未闭合", start)
		}
		if r == '"' {
			l.next()
			return sb.String(), nil
		}
		if r == '\\' {
			l.next()
			escPos := l.pos()
			e := l.next()
			switch e {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			case '"':
				sb.WriteByte('"')
			case '\\':
				sb.WriteByte('\\')
			case '0':
				sb.WriteByte(0)
			case 'x':
				if !bytesMode {
					return "", fmt.Errorf("%s: E0004: \\x 转义只允许出现在字节字面量中", escPos)
				}
				h1, h2 := l.next(), l.next()
				v, err := hex2(h1, h2)
				if err != nil {
					return "", fmt.Errorf("%s: E0004: 非法十六进制转义 \\x%c%c", escPos, h1, h2)
				}
				sb.WriteByte(byte(v))
			default:
				return "", fmt.Errorf("%s: E0004: 未知转义 \\%c", escPos, e)
			}
			continue
		}
		sb.WriteRune(l.next())
	}
}

// scanSymbol 扫描符号与运算符，取最长匹配。
func (l *Lexer) scanSymbol(p token.Pos) (token.Token, error) {
	c := l.next()
	mk := func(k token.Kind, text string) (token.Token, error) {
		return token.Token{Kind: k, Text: text, Pos: p}, nil
	}
	switch c {
	case '{':
		return mk(token.LBrace, "{")
	case '}':
		return mk(token.RBrace, "}")
	case '(':
		return mk(token.LParen, "(")
	case ')':
		return mk(token.RParen, ")")
	case '[':
		return mk(token.LBrack, "[")
	case ']':
		return mk(token.RBrack, "]")
	case ',':
		return mk(token.Comma, ",")
	case ':':
		return mk(token.Colon, ":")
	case ';':
		return mk(token.Semi, ";")
	case '.':
		return mk(token.Dot, ".")
	case '@':
		return mk(token.At, "@")
	case '+':
		return mk(token.Plus, "+")
	case '*':
		return mk(token.Star, "*")
	case '/':
		return mk(token.Slash, "/")
	case '%':
		return mk(token.Percent, "%")
	case '-':
		if l.peek() == '>' {
			l.next()
			return mk(token.Arrow, "->")
		}
		return mk(token.Minus, "-")
	case '=':
		if l.peek() == '=' {
			l.next()
			return mk(token.EqEq, "==")
		}
		if l.peek() == '>' {
			l.next()
			return mk(token.FatArrow, "=>")
		}
		return mk(token.Assign, "=")
	case '!':
		if l.peek() == '=' {
			l.next()
			return mk(token.NotEq, "!=")
		}
		return mk(token.Bang, "!")
	case '<':
		if l.peek() == '=' {
			l.next()
			return mk(token.Le, "<=")
		}
		return mk(token.Lt, "<")
	case '>':
		if l.peek() == '=' {
			l.next()
			return mk(token.Ge, ">=")
		}
		return mk(token.Gt, ">")
	case '|':
		if l.peek() == '|' {
			l.next()
			return mk(token.Or, "||")
		}
		return mk(token.Pipe, "|")
	case '&':
		if l.peek() == '&' {
			l.next()
			return mk(token.And, "&&")
		}
		return token.Token{}, fmt.Errorf("%s: E0005: 单个 & 非法，逻辑与请写 &&", p)
	default:
		return token.Token{}, fmt.Errorf("%s: E0005: 非法字符 %q", p, c)
	}
}

func isIdentStart(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isIdentPart(r rune) bool {
	return isIdentStart(r) || (r >= '0' && r <= '9')
}

func hex2(h1, h2 rune) (int, error) {
	v1, ok1 := hexVal(h1)
	v2, ok2 := hexVal(h2)
	if !ok1 || !ok2 {
		return 0, fmt.Errorf("bad hex")
	}
	return v1<<4 | v2, nil
}

func hexVal(r rune) (int, bool) {
	switch {
	case r >= '0' && r <= '9':
		return int(r - '0'), true
	case r >= 'a' && r <= 'f':
		return int(r-'a') + 10, true
	case r >= 'A' && r <= 'F':
		return int(r-'A') + 10, true
	}
	return 0, false
}
