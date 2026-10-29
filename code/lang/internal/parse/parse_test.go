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

	entry, ok := prog.Items[1].(*ast.EntryDecl)
	if !ok || entry.Name != "Demo" || len(entry.Fns) != 1 {
		t.Fatalf("第二个声明不是 entry Demo：%#v", prog.Items[1])
	}

	fn := entry.Fns[0]
	if fn.Name != "greet" || len(fn.Params) != 1 || len(fn.Intents) != 1 {
		t.Fatalf("greet 签名不对：%+v", fn)
	}
	if fn.Intents[0].Kind != "invariant" {
		t.Errorf("intent 类型 %q，期望 invariant", fn.Intents[0].Kind)
	}
	if fn.Body == nil || len(fn.Body.Stmts) != 2 {
		t.Fatalf("greet 函数体应为 2 条语句，得到 %+v", fn.Body)
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

	entry, ok := prog.Items[0].(*ast.EntryDecl)
	if !ok || entry.Name != "Serve" {
		t.Fatalf("首个声明不是 entry Serve：%#v", prog.Items[0])
	}

	fn := entry.Fns[0]
	if fn.Name != "handle" || len(fn.Params) != 5 {
		t.Fatalf("handle 签名不对：%+v", fn)
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

func TestIfBlockNotParsedAsConstruct(t *testing.T) {
	src := `entry E {
  fn f(x: bool) -> Unit {
    if x {
      return Unit
    }
    return Unit
  }
}`
	prog, err := File("t.dlt", src)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	entry := prog.Items[0].(*ast.EntryDecl)
	stmts := entry.Fns[0].Body.Stmts
	if len(stmts) != 2 {
		t.Fatalf("语句数 %d，期望 2", len(stmts))
	}
	if _, ok := stmts[0].(*ast.If); !ok {
		t.Errorf("首条语句应为 if，得到 %T", stmts[0])
	}
}

func TestPropagationBinding(t *testing.T) {
	src := `entry E {
  fn f(sys: Sys) -> u64 {
    let h = sys.hash64(b"x")!
    return h
  }
}`
	prog, err := File("t.dlt", src)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	entry := prog.Items[0].(*ast.EntryDecl)
	stmt := entry.Fns[0].Body.Stmts[0].(*ast.Let)
	if _, ok := stmt.Value.(*ast.Propagate); !ok {
		t.Errorf("`!` 应绑定到整个调用，得到 %T", stmt.Value)
	}
}
