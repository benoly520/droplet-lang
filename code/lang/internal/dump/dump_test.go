package dump

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"droplet/internal/ast"
	"droplet/internal/parse"
)

// load 按命令行顺序把多个文件合并成一个编译单元（与 droplet parse 一致）。
func load(t *testing.T, files ...string) *ast.Program {
	t.Helper()
	prog := &ast.Program{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		p, err := parse.File(f, string(b))
		if err != nil {
			t.Fatalf("解析 %s 失败：%v", f, err)
		}
		prog.Items = append(prog.Items, p.Items...)
	}
	return prog
}

// TestSamplesHaveNoUnknownNodes 三个样例是 D0 的真实语法，dump 必须全部认得。
//
// dump 遇到没见过的节点会静默输出 Unknown*；`droplet parse` 是 M0 的验收出口，
// 静默降级在这里同样不可接受。
func TestSamplesHaveNoUnknownNodes(t *testing.T) {
	checkNoUnknown(t, "webapi", load(t,
		"../../../webapi/src/contract.dlt",
		"../../../webapi/src/main.dlt"))
	checkNoUnknown(t, "hello", load(t, "../../examples/hello.dlt"))
}

// TestExtraSyntaxHasNoUnknownNodes 样例没写的几种节点：env 声明、match 分支、
// 三种字面量（int / bool / Unit）。
func TestExtraSyntaxHasNoUnknownNodes(t *testing.T) {
	src := `env App {
  port: u64
}

entry M {
  fn g() -> u64
    invariant "探针：覆盖 env / match / 三种字面量"
  {
    let a = 1
    let b = true
    let c = Unit
    match a {
      a => { return a },
      _ => { return a }
    }
    return a
  }
}`
	p, err := parse.File("probe.dlt", src)
	if err != nil {
		t.Fatalf("探针源码解析失败：%v", err)
	}
	checkNoUnknown(t, "probe", p)
}

func checkNoUnknown(t *testing.T, name string, prog *ast.Program) {
	t.Helper()
	root, ok := Program(prog).(map[string]any)
	if !ok || root["kind"] != "Program" {
		t.Fatalf("%s：顶层应为 Program，得到 %v", name, root["kind"])
	}
	b, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	if i := strings.Index(out, `"kind":"Unknown`); i >= 0 {
		t.Errorf("%s：dump 里有未识别的节点：…%s…", name, out[i:i+60])
	}
	// Intent 的规则名放在 rule 字段：kind 只属于节点类型。
	if !strings.Contains(out, `"kind":"Intent"`) {
		t.Errorf("%s：找不到 Intent 节点（rule 字段是否又写成了 kind？）", name)
	}
}
