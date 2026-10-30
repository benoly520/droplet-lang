package print

import (
	"testing"

	"droplet/internal/ast"
	"droplet/internal/parse"
)

// parseRequires 把表达式源码塞进一条 requires 里解析，取出表达式节点。
// 意图子句是表达式进入 AST 的唯一入口，用它当解析夹具，测的就是真实路径。
func parseRequires(t *testing.T, src string) ast.Expr {
	t.Helper()
	prog, err := parse.File("probe.dlt", "fn probe() -> Unit requires "+src+"\n")
	if err != nil {
		t.Fatalf("解析 %q 失败：%v", src, err)
	}
	fn, ok := prog.Items[0].(*ast.FnDecl)
	if !ok || len(fn.Intents) != 1 {
		t.Fatalf("解析 %q 没拿到 requires", src)
	}
	return fn.Intents[0].Expr
}

// TestExprCanonical 钉住规范形：括号只在必需处出现，其余位置一律摊平。
func TestExprCanonical(t *testing.T) {
	cases := []struct{ src, want string }{
		{`x`, `x`},
		{`1 + 2 * 3`, `1 + 2 * 3`}, // 无优先级：等价于 (1+2)*3，左结合不用括号
		{`(a + b) * c`, `a + b * c`},
		{`a + (b + c)`, `a + (b + c)`}, // 右操作数是二元式，必须括
		{`a - (b - c)`, `a - (b - c)`},
		{`a < b && b < c`, `a < b && b < c`}, // 无优先级：整条链都左结合，一律摊平
		{`a && (b < c)`, `a && (b < c)`},
		{`f(a, b + c)`, `f(a, b + c)`},
		{`f(g(x))!`, `f(g(x))!`},
		{`(a + b)!`, `(a + b)!`},
		{`(a!).b`, `(a!).b`},
		{`Point { x: 1, y: 2 }`, `Point { x: 1, y: 2 }`},
		{`T.V { n: 1 }`, `T.V { n: 1 }`},
		{`"a\nb"`, `"a\nb"`},
		{`b"\x00\xff"`, `b"\0\xff"`}, // 字节串优先写 \0，其余非可打印走 \xNN
		{`Unit`, `Unit`},
		{`true && false`, `true && false`},
	}
	for _, c := range cases {
		got := Expr(parseRequires(t, c.src))
		if got != c.want {
			t.Errorf("Expr(%s) = %s，想要 %s", c.src, got, c.want)
		}
	}
}

// TestExprRoundTrip 是打印器的定理：打印 → 重新解析 → 再打印，结果不变。
// 只要 AST 同构，这条就该对任意深度成立。
func TestExprRoundTrip(t *testing.T) {
	srcs := []string{
		`x`,
		`1 + 2 * 3`,
		`a + (b + c)`,
		`a - (b - c)`,
		`(a + b) * (c - d)`,
		`a < b && b < c`,
		`f(a, b + c)`,
		`f(g(x), h(y)!)!`,
		`(a + b)!`,
		`(a!).b`,
		`(a + b).f`,
		`Point { x: 1, y: 2 }`,
		`T.V { n: 1 }`,
		`T.V { n: f(1, 2) }`,
		`"a\nb\tc\rd\0e"`,
		`b"\x00\x01\xff"`,
		`"héllo"`,
	}
	for _, src := range srcs {
		first := Expr(parseRequires(t, src))
		second := Expr(parseRequires(t, first))
		if first != second {
			t.Errorf("往返不稳：%s → %s → %s", src, first, second)
		}
	}
}

// TestQuoteEscapes 保证打印出的字面量与 lexer 的转义表完全对齐：
// 打印器认识的写法，lexer 一定认识；否则往返在词法层就断了。
func TestQuoteEscapes(t *testing.T) {
	cases := []struct {
		value ast.Expr
		want  string
	}{
		{&ast.StrLit{Value: `a"b`}, `"a\"b"`},
		{&ast.StrLit{Value: `a\b`}, `"a\\b"`},
		{&ast.StrLit{Value: "a\nb\tc\rd\x00e"}, `"a\nb\tc\rd\0e"`},
		{&ast.StrLit{Value: "héllo"}, `"héllo"`},
		{&ast.BytesLit{Value: []byte{0, 1, 0xff, '"', '\\'}}, `b"\0\x01\xff\"\\"`},
	}
	for _, c := range cases {
		got := Expr(c.value)
		if got != c.want {
			t.Errorf("Expr = %s，想要 %s", got, c.want)
		}
		// 打印结果必须能被 lexer 读回同一个值。
		back := parseRequires(t, got)
		switch v := c.value.(type) {
		case *ast.StrLit:
			if s, ok := back.(*ast.StrLit); !ok || s.Value != v.Value {
				t.Errorf("%s 回读成了 %#v", got, back)
			}
		case *ast.BytesLit:
			if bb, ok := back.(*ast.BytesLit); !ok || string(bb.Value) != string(v.Value) {
				t.Errorf("%s 回读成了 %#v", got, back)
			}
		}
	}
}

func TestType(t *testing.T) {
	if got := Type(nil); got != "" {
		t.Errorf("Type(nil) = %q，想要空串", got)
	}
	tp := &ast.Union{Items: []ast.Type{
		&ast.Named{Name: "A"},
		&ast.Named{Name: "B"},
	}}
	if got, want := Type(tp), "A | B"; got != want {
		t.Errorf("Type = %s，想要 %s", got, want)
	}
}

func TestNilExpr(t *testing.T) {
	if got := Expr(nil); got != "" {
		t.Errorf("Expr(nil) = %q，想要空串", got)
	}
}
