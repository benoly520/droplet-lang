package contract

import (
	"os"
	"testing"

	"droplet/internal/ast"
	"droplet/internal/check"
	"droplet/internal/parse"
)

// load 按命令行同样的顺序合并多个文件，再跑一遍检查。
func load(t *testing.T, files ...string) (*ast.Program, *check.Result) {
	t.Helper()
	prog := &ast.Program{}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读取 %s 失败：%v", f, err)
		}
		p, err := parse.File(f, string(src))
		if err != nil {
			t.Fatalf("解析 %s 失败：%v", f, err)
		}
		prog.Items = append(prog.Items, p.Items...)
	}
	res := check.Program(prog)
	if len(res.Errors) > 0 {
		t.Fatalf("期望零错误，得到 %d 个：%v", len(res.Errors), res.Errors[0])
	}
	return prog, res
}

// TestWebapiContract 用真实契约确认导出形状：方法、意图、host、tag 号码。
func TestWebapiContract(t *testing.T) {
	prog, res := load(t, "../../../webapi/src/contract.dlt", "../../../webapi/src/main.dlt")
	doc := Build(prog, res)

	if len(doc.Boundaries) != 1 {
		t.Fatalf("期望 1 个 boundary，得到 %d", len(doc.Boundaries))
	}
	b := doc.Boundaries[0]
	if b.Name != "BlobApi" || b.Version != 1 {
		t.Fatalf("boundary 身份错：%s @v%d", b.Name, b.Version)
	}
	if len(b.Methods) != 3 {
		t.Fatalf("期望 3 个方法，得到 %d", len(b.Methods))
	}
	get := b.Methods[0]
	if get.Name != "get" || get.Ret != "bytes" || !get.Idempotent {
		t.Fatalf("get 方法导出错：%+v", get)
	}
	if len(get.Params) != 1 || get.Params[0].Name != "key" || get.Params[0].Type != "str" {
		t.Fatalf("get 参数导出错：%+v", get.Params)
	}
	if len(get.Intents) != 1 || get.Intents[0].Kind != "invariant" || get.Intents[0].Text == "" {
		t.Fatalf("get 意图导出错：%+v", get.Intents)
	}

	var entry *Function
	for i := range doc.Functions {
		if doc.Functions[i].Entry {
			entry = &doc.Functions[i]
		}
	}
	if entry == nil || entry.Name != "handle" {
		t.Fatalf("没找到入口函数 handle：%+v", doc.Functions)
	}
	// 参数里的 BlobFs / Versioning 是 host 组，str 不是。v0.10 起入口不再要 Sys：
	// 它原本只为算 ETag 而存在，而 ETag 现在由运行时从 body 推导。
	if len(entry.Hosts) != 2 || entry.Hosts[0] != "BlobFs" ||
		entry.Hosts[1] != "Versioning" {
		t.Fatalf("host 列表错：%v", entry.Hosts)
	}
}

// TestTagNumbers 锁定 tag 分配规则：错误变体从 100 起，其余从 1 起，成功不占位。
func TestTagNumbers(t *testing.T) {
	prog, res := load(t, "../../../webapi/src/contract.dlt", "../../../webapi/src/main.dlt")
	doc := Build(prog, res)

	got := map[string]int{}
	for _, tag := range doc.Tags {
		got[tag.Name] = tag.Value
	}
	want := map[string]int{
		"NotFound":   100,
		"BadRequest": 101,
		"IoError":    102,
		"Conflict":   103,
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s 的 tag 应为 %d，得到 %d", name, value, got[name])
		}
	}
	if _, ok := got[okTag]; ok {
		t.Errorf("成功值 %q 不应出现在 tag 表里", okTag)
	}
}

// TestTagNumbersNotPropagated 锁定另一半规则：没进过 `!` 的变体从 1 起。
// 契约样例里的变体全部参与了错误传播，这条只能在合成程序上验。
func TestTagNumbersNotPropagated(t *testing.T) {
	src := `type Plain = Alpha | Beta

entry f() -> Unit
  invariant "无"
{

}`
	prog, err := parse.File("t.dlt", src)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	res := check.Program(prog)
	if len(res.Errors) > 0 {
		t.Fatalf("检查未通过：%v", res.Errors)
	}

	got := map[string]int{}
	for _, tag := range Build(prog, res).Tags {
		got[tag.Name] = tag.Value
	}
	if got["Alpha"] != 1 || got["Beta"] != 2 {
		t.Fatalf("非传播变体应为 1、2，得到 Alpha=%d Beta=%d", got["Alpha"], got["Beta"])
	}
}

// TestNilResult 允许只给语法树：此时没有 tag 表，但签名照旧导出。
func TestNilResult(t *testing.T) {
	prog, _ := load(t, "../../../webapi/src/contract.dlt")
	doc := Build(prog, nil)
	if len(doc.Tags) != 0 {
		t.Fatalf("res 为 nil 时不该有 tag，得到 %d 条", len(doc.Tags))
	}
	if len(doc.Boundaries) != 1 {
		t.Fatalf("boundary 应照旧导出，得到 %d", len(doc.Boundaries))
	}
}
