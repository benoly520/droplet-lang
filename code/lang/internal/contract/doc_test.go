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
	// D3：契约里现在是 5 条路由（含以前根本没声明的 /health 与 POST /v1/keys）。
	if len(b.Methods) != 5 {
		t.Fatalf("期望 5 个方法，得到 %d", len(b.Methods))
	}
	var get Method
	for _, m := range b.Methods {
		if m.Name == "get" {
			get = m
		}
	}
	if get.Name != "get" || get.Ret != "bytes" || !get.Idempotent {
		t.Fatalf("get 方法导出错：%+v", get)
	}
	// 路由三件事必须进契约：动词、路径、预条件。
	if get.Verb != "GET" || get.Path != "/v1/blob" || get.Precondition || get.Impl != "blob_get" {
		t.Fatalf("get 的路由没导出：%+v", get)
	}
	// 成功码也要进契约（B3）：默认 200 省略，写了注解才有值。
	if get.Success != 0 {
		t.Fatalf("没写成功码注解的方法不该导出 success：%+v", get)
	}
	status := map[string]int{}
	for _, m := range b.Methods {
		status[m.Name] = m.Success
	}
	if status["put"] != 204 || status["create"] != 201 {
		t.Fatalf("成功码没导出：%v", status)
	}
	if len(get.Params) != 1 || get.Params[0].Name != "key" || get.Params[0].Type != "str" {
		t.Fatalf("get 参数导出错：%+v", get.Params)
	}
	if len(get.Intents) != 1 || get.Intents[0].Kind != "invariant" || get.Intents[0].Text == "" {
		t.Fatalf("get 意图导出错：%+v", get.Intents)
	}

	// 服务形态没有入口函数（D3）：入口是路由表，函数只是实现。
	for i := range doc.Functions {
		if doc.Functions[i].Entry {
			t.Fatalf("服务形态不该导出 entry：%+v", doc.Functions[i])
		}
	}
	// 实现函数的 host 组要照样导出：blob_get 要 BlobFs，str / App 都不是 host。
	var impl *Function
	for i := range doc.Functions {
		if doc.Functions[i].Name == "blob_get" {
			impl = &doc.Functions[i]
		}
	}
	if impl == nil {
		t.Fatal("契约里找不到实现函数 blob_get")
	}
	if len(impl.Hosts) != 1 || impl.Hosts[0] != "BlobFs" {
		t.Fatalf("host 列表错：%v", impl.Hosts)
	}
}

// TestEnvExport 锁定配置面的导出：运维拿到这份 JSON 就能拼出环境变量，
// 而不需要去猜哪个键是必填的（SPEC §5.7 的「缺键即起不来」要能被预先查）。
func TestEnvExport(t *testing.T) {
	prog, res := load(t, "../../../webapi/src/contract.dlt", "../../../webapi/src/main.dlt")
	doc := Build(prog, res)

	if len(doc.Envs) != 1 {
		t.Fatalf("期望 1 个 env 块，得到 %d", len(doc.Envs))
	}
	g := doc.Envs[0]
	if g.Name != "App" || g.Prefix != "APP" {
		t.Fatalf("块名与前缀错：%s / %s", g.Name, g.Prefix)
	}
	if len(g.Keys) != 5 {
		t.Fatalf("期望 5 个配置键，得到 %d", len(g.Keys))
	}
	want := map[string]Env{
		"port":     {Name: "port", Type: "u64", Var: "APP_PORT", Default: "8787"},
		"bind":     {Name: "bind", Type: "str", Var: "APP_BIND", Default: `"0.0.0.0"`},
		"root":     {Name: "root", Type: "str", Var: "APP_ROOT", Required: true},
		"token":    {Name: "token", Type: "str", Var: "APP_TOKEN", Default: `""`},
		"max_body": {Name: "max_body", Type: "u64", Var: "APP_MAX_BODY", Default: "8388608"},
	}
	for _, k := range g.Keys {
		w, ok := want[k.Name]
		if !ok {
			t.Fatalf("多了一个配置键：%s", k.Name)
		}
		if k.Type != w.Type || k.Var != w.Var || k.Default != w.Default || k.Required != w.Required {
			t.Fatalf("配置键 %s 导出错：得到 %+v，期望 %+v", k.Name, k, w)
		}
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
	prog, _ := load(t, "../../../webapi/src/contract.dlt", "../../../webapi/src/main.dlt")
	doc := Build(prog, nil)
	if len(doc.Tags) != 0 {
		t.Fatalf("res 为 nil 时不该有 tag，得到 %d 条", len(doc.Tags))
	}
	if len(doc.Boundaries) != 1 {
		t.Fatalf("boundary 应照旧导出，得到 %d", len(doc.Boundaries))
	}
	// env 也只靠语法树就能导出：变量名与必填性都是声明里的事实。
	if len(doc.Envs) != 1 || len(doc.Envs[0].Keys) != 5 {
		t.Fatalf("env 应照旧导出，得到 %+v", doc.Envs)
	}
}

// TestIntentExprExport 锁定 requires / example 的表达式导出：契约消费方要看到
// 规范形（括号按需、转义按词法表），而不是一段无法定位的原文。
func TestIntentExprExport(t *testing.T) {
	const src = `boundary Api @v1 {
  probe(x: u64) -> bool requires x > 0 && (x + 1) < 10 example check(x!)
}`
	prog, err := parse.File("intent.dlt", src)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	doc := Build(prog, nil)
	if len(doc.Boundaries) != 1 || len(doc.Boundaries[0].Methods) != 1 {
		t.Fatalf("boundary 导出错：%+v", doc.Boundaries)
	}
	intents := doc.Boundaries[0].Methods[0].Intents
	if len(intents) != 2 {
		t.Fatalf("期望 2 条意图，得到 %d", len(intents))
	}
	if intents[0].Kind != "requires" || intents[0].Expr != "x > 0 && (x + 1) < 10" {
		t.Fatalf("requires 表达式导出错：%+v", intents[0])
	}
	if intents[1].Kind != "example" || intents[1].Expr != "check(x!)" {
		t.Fatalf("example 表达式导出错：%+v", intents[1])
	}
}
