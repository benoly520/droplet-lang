// Package print 把 AST 节点渲染回能重新解析的源码。
//
// 它存在的理由只有一个：报错与契约要能**指到具体子表达式**，而不是"你的
// invariant 有问题"。E10（意图抓不到 mutation 即硬错）没有它就写不出来 ——
// 说得出"哪一条不成立"的前提是说得出"那一条长什么样"。
//
// 渲染出来的是**规范形**，不是源码原样：AST 不记录原来有没有括号，所以打印器
// 按自己的规则加最少的括号（右操作数是二元式、`!` 的操作数是二元式、调用的被调
// 是复合式）。定理很简单：把打印结果重新解析，得到的 AST 与原来同构。
package print

import (
	"strconv"
	"strings"

	"droplet/internal/ast"
)

// Expr 渲染表达式；nil 渲染成空串（意图里允许没有表达式）。
func Expr(e ast.Expr) string {
	return expr(e, false)
}

// Type 渲染类型表达式。
func Type(t ast.Type) string {
	switch n := t.(type) {
	case nil:
		return ""
	case *ast.Named:
		return n.Name
	case *ast.Union:
		parts := make([]string, 0, len(n.Items))
		for _, it := range n.Items {
			parts = append(parts, Type(it))
		}
		return strings.Join(parts, " | ")
	}
	return ""
}

func expr(e ast.Expr, paren bool) string {
	s := raw(e)
	if paren && s != "" {
		return "(" + s + ")"
	}
	return s
}

func raw(e ast.Expr) string {
	switch n := e.(type) {
	case nil:
		return ""
	case *ast.Ident:
		return n.Name
	case *ast.IntLit:
		return n.Value
	case *ast.StrLit:
		return quote(n.Value, false)
	case *ast.BytesLit:
		return "b" + quote(string(n.Value), true)
	case *ast.BoolLit:
		if n.Value {
			return "true"
		}
		return "false"
	case *ast.UnitLit:
		return "Unit"
	case *ast.Call:
		args := make([]string, 0, len(n.Args))
		for _, a := range n.Args {
			// 实参列表天然有边界（`,` 与 `)`），二元式不必再包一层。
			args = append(args, Expr(a))
		}
		return expr(n.Callee, compound(n.Callee)) + "(" + strings.Join(args, ", ") + ")"
	case *ast.Construct:
		head := n.TypeName
		if n.Variant != "" {
			head += "." + n.Variant
		}
		fields := make([]string, 0, len(n.Fields))
		for _, f := range n.Fields {
			fields = append(fields, f.Name+": "+Expr(f.Value))
		}
		return head + " { " + strings.Join(fields, ", ") + " }"
	case *ast.Select:
		return expr(n.Target, compound(n.Target)) + "." + n.Field
	case *ast.Binary:
		// 左结合链不需要括号：`a + b - c` 解析出来就是 ((a+b)-c)。
		// 右操作数是二元式时必须加，否则 `a - (b - c)` 会被解析成 ((a-b)-c)。
		return expr(n.Left, false) + " " + n.Op + " " + expr(n.Right, isBinary(n.Right))
	case *ast.Propagate:
		return expr(n.Inner, isBinary(n.Inner)) + "!"
	}
	return ""
}

// compound 判定被调 / 被取的表达式是否必须先加括号（否则会被解析成别的东西）。
func compound(e ast.Expr) bool {
	switch e.(type) {
	case *ast.Ident, *ast.Select, *ast.Construct:
		return false
	}
	return true
}

func isBinary(e ast.Expr) bool {
	_, ok := e.(*ast.Binary)
	return ok
}

// quote 把字符串或字节串渲染成字面量。
//
// 转义表必须与 lexer 严格对齐：它只认 \n \t \r \" \\ \0（字符串与字节串）与 \xNN
// （仅字节串）。所以字符串里的非可打印字符**只能**来自这些转义 —— 写不出来的值也
// 解析不出来，两边同构。用 strconv.Quote 会生成 \u00e9 一类词法层不认识的转义。
func quote(s string, bytes bool) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		case 0:
			b.WriteString(`\0`)
		default:
			if c >= 0x20 && c < 0x7f {
				b.WriteByte(c)
			} else {
				// 非 ASCII 或控制字符：字节串用 \xNN；字符串没有这种写法，
				// 直接写原字节（UTF-8 源码允许），因为解析器只在转义处做解码。
				if bytes {
					b.WriteString(`\x`)
					b.WriteString(hexDigits(c))
				} else {
					b.WriteByte(c)
				}
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func hexDigits(c byte) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[c>>4], digits[c&0x0f]})
}

// StrLit 是打印器的反方向：把解码后的字面量再包成源码形式，供测试与契约复用。
func StrLit(s string) string { return quote(s, false) }

// IntLit 与 strconv.Itoa 同义，保留名字是为了让调用点读起来一致。
func IntLit(v int) string { return strconv.Itoa(v) }
