package lex

import (
	"strings"
	"testing"

	"droplet/internal/token"
)

func kinds(t *testing.T, src string) []token.Kind {
	t.Helper()
	toks, err := New("t.dlt", src).Tokens()
	if err != nil {
		t.Fatalf("扫描失败：%v", err)
	}
	out := make([]token.Kind, len(toks))
	for i, tk := range toks {
		out[i] = tk.Kind
	}
	return out
}

func TestRecordTokens(t *testing.T) {
	got := kinds(t, `type A { x: str }`)
	want := []token.Kind{
		token.KwType, token.Ident, token.LBrace, token.Ident, token.Colon,
		token.Ident, token.RBrace, token.EOF,
	}
	if len(got) != len(want) {
		t.Fatalf("token 数量 %d，期望 %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("#%d 得到 %v，期望 %v", i, got[i], want[i])
		}
	}
}

func TestBytesLiteralAndEscapes(t *testing.T) {
	toks, err := New("t.dlt", `b"{\"ok\":\x21}" "a\nb"`).Tokens()
	if err != nil {
		t.Fatalf("扫描失败：%v", err)
	}
	if toks[0].Kind != token.Bytes || toks[0].Text != `{"ok":!}` {
		t.Errorf("字节字面量解析错误：%v %q", toks[0].Kind, toks[0].Text)
	}
	if toks[1].Kind != token.Str || toks[1].Text != "a\nb" {
		t.Errorf("字符串转义解析错误：%v %q", toks[1].Kind, toks[1].Text)
	}
}

func TestSymbolLongestMatch(t *testing.T) {
	got := kinds(t, `-> => == != <= >= && || @ | !`)
	want := []token.Kind{
		token.Arrow, token.FatArrow, token.EqEq, token.NotEq, token.Le, token.Ge,
		token.And, token.Or, token.At, token.Pipe, token.Bang, token.EOF,
	}
	if len(got) != len(want) {
		t.Fatalf("token 数量 %d，期望 %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("#%d 得到 %v，期望 %v", i, got[i], want[i])
		}
	}
}

func TestPositions(t *testing.T) {
	toks, err := New("t.dlt", "type A {\n  x: str\n}").Tokens()
	if err != nil {
		t.Fatal(err)
	}
	// `{` 在第 1 行第 8 列，`x` 在第 2 行第 3 列。
	if p := toks[2].Pos; p.Line != 1 || p.Col != 8 {
		t.Errorf("`{` 位置 %d:%d，期望 1:8", p.Line, p.Col)
	}
	if p := toks[3].Pos; p.Line != 2 || p.Col != 3 {
		t.Errorf("`x` 位置 %d:%d，期望 2:3", p.Line, p.Col)
	}
}

func TestUnterminatedString(t *testing.T) {
	_, err := New("t.dlt", `"abc`).Tokens()
	if err == nil {
		t.Fatal("未闭合字符串应当报错")
	}
	if !strings.Contains(err.Error(), "E0003") {
		t.Errorf("错误码不对：%v", err)
	}
}

func TestIllegalChar(t *testing.T) {
	_, err := New("t.dlt", `#`).Tokens()
	if err == nil {
		t.Fatal("非法字符应当报错")
	}
	if !strings.Contains(err.Error(), "t.dlt:1:1") {
		t.Errorf("错误位置不对：%v", err)
	}
}
