package parse

import (
	"os"
	"strings"
	"testing"

	"droplet/internal/ast"
)

func mustFile(t *testing.T, path string) *ast.Program {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", path, err)
	}
	prog, err := File(path, string(src))
	if err != nil {
		t.Fatalf("解析 %s 失败：%v", path, err)
	}
	return prog
}

func TestParseHello(t *testing.T) {
	prog := mustFile(t, "../../examples/hello.dlt")
	if len(prog.Items) != 2 {
		t.Fatalf("顶层声明 %d 个，期望 2 个", len(prog.Items))
	}

	host, ok := prog.Items[0].(*ast.HostDecl)
	if !ok || host.Name != "Sys" || len(host.Fns) != 1 {
		t.Fatalf("第一个声明不是 host Sys：%#v", prog.Items[0])
	}
	if host.Fns[0].Body != nil {
		t.Error("host 函数不应有函数体")
	}

	// 拆平之后入口就是一个带 Entry 标记的 FnDecl，不再有 EntryDecl 这层壳。
	fn, ok := prog.Items[1].(*ast.FnDecl)
	if !ok || fn.Name != "greet" || !fn.Entry {
		t.Fatalf("第二个声明不是 entry greet：%#v", prog.Items[1])
	}
	if len(fn.Params) != 1 || len(fn.Intents) != 1 {
		t.Fatalf("greet 签名不对：%+v", fn)
	}
	if fn.Intents[0].Kind != "invariant" {
		t.Errorf("intent 类型 %q，期望 invariant", fn.Intents[0].Kind)
	}
	if fn.Body == nil || len(fn.Body.Stmts) != 1 {
		t.Fatalf("greet 函数体应为 1 条语句（Unit 函数不写 return），得到 %+v", fn.Body)
	}
}

func TestParseWebapiContract(t *testing.T) {
	prog := mustFile(t, "../../../webapi/src/contract.dlt")

	var sawSum, sawRecord, sawBoundary bool
	for _, it := range prog.Items {
		switch n := it.(type) {
		case *ast.SumType:
			if n.Name == "ApiError" && len(n.Variants) == 4 {
				sawSum = true
			}
		case *ast.RecordType:
			if n.Name == "Request" && len(n.Fields) == 5 {
				sawRecord = true
			}
		case *ast.BoundaryDecl:
			if n.Name == "BlobApi" && n.Version == 1 && len(n.Methods) == 3 {
				sawBoundary = true
			}
		}
	}
	if !sawSum || !sawRecord || !sawBoundary {
		t.Errorf("契约解析不完整：sum=%v record=%v boundary=%v", sawSum, sawRecord, sawBoundary)
	}
}

func TestParseWebapiMain(t *testing.T) {
	prog := mustFile(t, "../../../webapi/src/main.dlt")

	fn, ok := prog.Items[0].(*ast.FnDecl)
	if !ok || fn.Name != "handle" || !fn.Entry {
		t.Fatalf("首个声明不是 entry handle：%#v", prog.Items[0])
	}
	var names []string
	for _, p := range fn.Params {
		names = append(names, p.Name)
	}
	// 断言名字而不只是个数：v0.10 删 sys、v0.11 把 root 换成 cfg，只数个数的断言两次都漏过去了。
	if got := strings.Join(names, ","); got != "req,fs,ver,cfg" {
		t.Fatalf("handle 参数应为 req,fs,ver,cfg，得到 %s", got)
	}
	if fn.Ret == nil {
		t.Fatal("handle 缺少返回类型")
	}
	if u, ok := fn.Ret.(*ast.Union); !ok || len(u.Items) != 2 {
		t.Errorf("handle 返回类型应为二元联合，得到 %#v", fn.Ret)
	}
	if fn.Body == nil || len(fn.Body.Stmts) < 4 {
		t.Fatalf("handle 函数体语句过少：%+v", fn.Body)
	}

	// 第一条语句应是 if，而不是被误判为构造的表达式语句。
	if _, ok := fn.Body.Stmts[0].(*ast.If); !ok {
		t.Errorf("首条语句应为 if，得到 %T", fn.Body.Stmts[0])
	}
}

func TestParseErrorReportsPosition(t *testing.T) {
	_, err := File("bad.dlt", "type A {\n  x: str\n")
	if err == nil {
		t.Fatal("缺少右花括号应当报错")
	}
	msg := err.Error()
	if !strings.Contains(msg, "bad.dlt:") {
		t.Errorf("错误信息缺少文件名与位置：%v", err)
	}
	if !strings.Contains(msg, "E0006") {
		t.Errorf("错误信息缺少错误码：%v", err)
	}
}

// host 能力参数不写名字：名字的权威是 internal/host 的运行时原型，
// 声明处再写一遍是零信息的重复书写。
func TestHostParamNameRejected(t *testing.T) {
	src := `host Sys {
  fn log(msg: str) -> Unit
}`
	_, err := File("t.dlt", src)
	if err == nil {
		t.Fatal("host 能力参数带名字应当报错")
	}
	if !strings.Contains(err.Error(), "不写名字") {
		t.Errorf("错误信息缺少改写提示：%v", err)
	}
}

// 无名参数是 host 能力的正确写法。
func TestHostParamAnonAccepted(t *testing.T) {
	src := `host Sys {
  fn log(str) -> Unit
}`
	prog, err := File("t.dlt", src)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	h := prog.Items[0].(*ast.HostDecl)
	if len(h.Fns[0].Params) != 1 {
		t.Fatalf("参数个数 %d，期望 1", len(h.Fns[0].Params))
	}
	if h.Fns[0].Params[0].Name != "" {
		t.Errorf("host 能力参数不应有名字，得到 %q", h.Fns[0].Params[0].Name)
	}
}

func TestIfBlockNotParsedAsConstruct(t *testing.T) {
	src := `entry f(x: bool) -> Unit {
  if x {

  }

}`
	prog, err := File("t.dlt", src)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	fn := prog.Items[0].(*ast.FnDecl)
	stmts := fn.Body.Stmts
	if len(stmts) != 1 {
		t.Fatalf("语句数 %d，期望 1", len(stmts))
	}
	if _, ok := stmts[0].(*ast.If); !ok {
		t.Errorf("首条语句应为 if，得到 %T", stmts[0])
	}
}

func TestVariantConstructParsed(t *testing.T) {
	// 两种变体构造：`T.V { … }` 带载荷，`T.V` 不带（后者留在 Select 里由 checker 认）。
	src := `type Shape = Circle | Square { side: u64 }

entry f() -> Shape {
  let a = Shape.Circle
  let b = Shape.Square { side: 7 }
  return b
}`
	prog, err := File("t.dlt", src)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	stmts := prog.Items[1].(*ast.FnDecl).Body.Stmts

	// 零字段变体：`Shape.Circle` 是 Select，而不报错（checker 再认）。
	if sel, ok := stmts[0].(*ast.Let).Value.(*ast.Select); !ok {
		t.Errorf("零字段变体应是 Select，得到 %T", stmts[0].(*ast.Let).Value)
	} else if sel.Field != "Circle" {
		t.Errorf("变体名 %q，期望 Circle", sel.Field)
	}

	con, ok := stmts[1].(*ast.Let).Value.(*ast.Construct)
	if !ok {
		t.Fatalf("带载荷变体应是 Construct，得到 %T", stmts[1].(*ast.Let).Value)
	}
	if con.TypeName != "Shape" || con.Variant != "Square" || len(con.Fields) != 1 {
		t.Errorf("构造头不对：%+v", con)
	}
}

func TestPropagationBinding(t *testing.T) {
	src := `entry f(sys: Sys) -> u64 {
  let h = sys.hash64(b"x")!
  return h
}`
	prog, err := File("t.dlt", src)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	fn := prog.Items[0].(*ast.FnDecl)
	stmt := fn.Body.Stmts[0].(*ast.Let)
	if _, ok := stmt.Value.(*ast.Propagate); !ok {
		t.Errorf("`!` 应绑定到整个调用，得到 %T", stmt.Value)
	}
}

// TestEntryShellRejected 旧写法 `entry Name { fn … }` 必须报错并给改写提示。
//
// 减法一律取「必不写」而不是「可省略」：允许两种形状共存不是减法，只是多一个选项。
func TestEntryShellRejected(t *testing.T) {
	_, err := File("t.dlt", `entry Demo {
  fn f() -> Unit
    invariant "x"
  {
  }
}
`)
	if err == nil {
		t.Fatal("entry 套壳的旧写法应当报错")
	}
	if !strings.Contains(err.Error(), "entry 直接写签名") {
		t.Errorf("报错没给改写提示：%v", err)
	}
}

// TestEntryFlag 拆平后入口就是一个带 Entry 标记的 FnDecl，顶层普通 fn 的标记为假。
func TestEntryFlag(t *testing.T) {
	prog, err := File("t.dlt", `entry f() -> Unit
  invariant "x"
{
}

fn g() -> Unit
  invariant "y"
{
}
`)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	entry := prog.Items[0].(*ast.FnDecl)
	plain := prog.Items[1].(*ast.FnDecl)
	if !entry.Entry {
		t.Errorf("entry f 的 Entry 标记应为真：%+v", entry)
	}
	if plain.Entry {
		t.Errorf("普通 fn g 的 Entry 标记应为假：%+v", plain)
	}
}

// TestEntryNeedsBody 只写签名不写函数体的 entry 不能过解析。
func TestEntryNeedsBody(t *testing.T) {
	_, err := File("t.dlt", "entry f() -> Unit\n  invariant \"x\"\n")
	if err == nil || !strings.Contains(err.Error(), "必须有函数体") {
		t.Fatalf("entry 缺函数体应报错，得到 %v", err)
	}
}
