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

	var sawSum, sawBoundary bool
	var put *ast.BndMethod
	for _, it := range prog.Items {
		switch n := it.(type) {
		case *ast.SumType:
			if n.Name == "ApiError" && len(n.Variants) == 6 {
				sawSum = true
			}
		case *ast.BoundaryDecl:
			if n.Name == "BlobApi" && n.Version == 1 && len(n.Methods) == 5 {
				sawBoundary = true
			}
			for i := range n.Methods {
				if n.Methods[i].Name == "put" {
					put = n.Methods[i]
				}
			}
		}
	}
	if !sawSum || !sawBoundary {
		t.Errorf("契约解析不完整：sum=%v boundary=%v", sawSum, sawBoundary)
	}
	if put == nil {
		t.Fatal("契约里没有 put 方法")
	}
	// D3 的路由三件事 + 预条件声明都要落到 AST 上。
	if put.Verb != "put" || put.Path != "/v1/blob" || put.Impl != "blob_put" {
		t.Fatalf("put 的路由没解析：%+v", put)
	}
	if !put.Idempotent || !put.Precondition || put.NoPrecondition {
		t.Fatalf("put 的 idempotency / precondition 没解析：%+v", put)
	}
	// B3：成功码是声明的投影 —— put 写 @no_content（204），create 写 @created（201）。
	if put.Status != 204 {
		t.Fatalf("put 的成功码应为 204，得到 %d", put.Status)
	}
	// 读路由不该带 precondition，写路由必须显式表态（这里用 without 声明）。
	var gotGet, gotCreate bool
	for _, m := range prog.Items {
		b, ok := m.(*ast.BoundaryDecl)
		if !ok {
			continue
		}
		for _, x := range b.Methods {
			switch x.Name {
			case "get":
				gotGet = x.Verb == "get" && !x.Precondition && !x.NoPrecondition
			case "create":
				gotCreate = x.Verb == "post" && x.NoPrecondition && x.NoPreconditionReason != "" && x.Status == 201
			}
		}
	}
	if !gotGet || !gotCreate {
		t.Errorf("verb / without precondition 解析错：get=%v create=%v", gotGet, gotCreate)
	}
}

func TestParseWebapiMain(t *testing.T) {
	prog := mustFile(t, "../../../webapi/src/main.dlt")

	// D3 起服务形态没有入口函数：main.dlt 就是几个实现函数，首个是 health。
	fn, ok := prog.Items[0].(*ast.FnDecl)
	if !ok || fn.Name != "health" || fn.Entry {
		t.Fatalf("首个声明应为普通函数 health：%#v", prog.Items[0])
	}
	var put *ast.FnDecl
	for _, it := range prog.Items {
		if f, ok := it.(*ast.FnDecl); ok && f.Name == "blob_put" {
			put = f
		}
	}
	if put == nil {
		t.Fatal("main.dlt 里没有 blob_put")
	}
	var names []string
	for _, p := range put.Params {
		names = append(names, p.Name)
	}
	// 断言名字而不只是个数：v0.10 删 sys、v0.11 把 root 换成 cfg，只数个数的断言两次都漏过去了。
	if got := strings.Join(names, ","); got != "key,body,fs,ver,cfg" {
		t.Fatalf("blob_put 参数应为 key,body,fs,ver,cfg，得到 %s", got)
	}
	if u, ok := put.Ret.(*ast.Union); !ok || len(u.Items) != 2 {
		t.Errorf("blob_put 返回类型应为二元联合，得到 %#v", put.Ret)
	}
	if put.Body == nil || len(put.Body.Stmts) < 3 {
		t.Fatalf("blob_put 函数体语句过少：%+v", put.Body)
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

// 成功码注解（B3）：@created / @no_content 是普通标识符后面不带括号，
// 解析只负责落到 AST，合法性（能不能投、投给谁）留给 check。
func TestParseSuccessAnnotations(t *testing.T) {
	src := `boundary Api @v1 {
  make(key: str) -> Unit @post("/v1/x") @created impl h with idempotency
    invariant "x"

  drop(key: str) -> Unit @delete("/v1/x") @no_content impl h with idempotency
    invariant "y"
}`
	prog, err := File("t.dlt", src)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	b := prog.Items[0].(*ast.BoundaryDecl)
	if b.Methods[0].Status != 201 || b.Methods[1].Status != 204 {
		t.Fatalf("成功码没解析出来：%d / %d", b.Methods[0].Status, b.Methods[1].Status)
	}
}

func TestParseTwoSuccessAnnotationsRejected(t *testing.T) {
	src := `boundary Api @v1 {
  make(key: str) -> Unit @post("/v1/x") @created @no_content impl h
    invariant "x"
}`
	if _, err := File("t.dlt", src); err == nil {
		t.Fatal("两个成功码注解应当报错")
	} else if !strings.Contains(err.Error(), "只能写一次") {
		t.Errorf("错误信息缺少说明：%v", err)
	}
}

func TestParseUnknownAnnotationRejected(t *testing.T) {
	src := `boundary Api @v1 {
  make(key: str) -> Unit @post("/v1/x") @accepted impl h
    invariant "x"
}`
	if _, err := File("t.dlt", src); err == nil {
		t.Fatal("不认识的注解应当报错")
	} else if !strings.Contains(err.Error(), "@created") {
		t.Errorf("错误信息应当列出可用注解：%v", err)
	}
}
