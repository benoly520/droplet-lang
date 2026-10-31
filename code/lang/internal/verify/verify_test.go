package verify

import (
	"strings"
	"testing"

	"droplet/internal/ast"
	"droplet/internal/check"
	"droplet/internal/parse"
)

// load 走 parse → check，返回语法树与检查结果。
func load(t *testing.T, src string) (*ast.Program, *check.Result) {
	t.Helper()
	prog, err := parse.File("t.dlt", src)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	res := check.Program(prog)
	if len(res.Errors) > 0 {
		t.Fatalf("检查失败：%v", res.Errors)
	}
	return prog, res
}

// TestRunReportsPassAndFail 是最核心的一条：样例必须真的跑在实现上，
// 不成立的要被点名（示例实现故意写错一条）。
func TestRunReportsPassAndFail(t *testing.T) {
	const src = `fn double(x: u64) -> u64 {
  return x * 2
}

entry main() -> Unit
  example double(3) == 6
  example double(4) == 9
  invariant "无"
{
  return
}
`
	out, err := Run(load(t, src))
	if err != nil {
		t.Fatalf("运行失败：%v", err)
	}
	if out.Ran != 2 || len(out.Cases) != 2 {
		t.Fatalf("应为 2 条可执行样例，得到 ran=%d cases=%d", out.Ran, len(out.Cases))
	}
	if !out.Cases[0].OK {
		t.Errorf("double(3) == 6 应成立：%+v", out.Cases[0])
	}
	if out.Cases[1].OK {
		t.Errorf("double(4) == 9 不该成立：%+v", out.Cases[1])
	}
	if out.Passed() {
		t.Error("有失败样例时 Passed() 必须为 false")
	}
}

// TestRunSkipsOpenExample 锁住"没牙齿的样例要记账"：引用参数的样例跑不了，
// 必须带着原因被跳过，而不是悄悄消失。
func TestRunSkipsOpenExample(t *testing.T) {
	const src = `fn double(x: u64) -> u64
  example double(x) == 8
{
  return x * 2
}

entry main() -> Unit
  example double(3) == 6
  invariant "无"
{
  return
}
`
	out, err := Run(load(t, src))
	if err != nil {
		t.Fatalf("运行失败：%v", err)
	}
	if out.Ran != 1 || len(out.Cases) != 2 {
		t.Fatalf("应只执行 1 条，得到 ran=%d cases=%d", out.Ran, len(out.Cases))
	}
	if out.Cases[0].Skip == "" || !strings.Contains(out.Cases[0].Skip, "x") {
		t.Errorf("开放样例应被跳过并说明原因：%+v", out.Cases[0])
	}
	if !out.Passed() {
		t.Error("剩下的样例全部成立时 Passed() 应为 true")
	}
}

// TestRunWithoutRunnableExamples 说明"零可执行样例 = 没有证据"：
// 这是 E10 的输入，所以按未通过处理。
func TestRunWithoutRunnableExamples(t *testing.T) {
	const src = `fn double(x: u64) -> u64
  example double(x) == 8
{
  return x * 2
}

entry main() -> Unit invariant "无" {
  return
}
`
	out, err := Run(load(t, src))
	if err != nil {
		t.Fatalf("运行失败：%v", err)
	}
	if out.Ran != 0 || out.Passed() {
		t.Fatalf("零可执行样例不该算通过：ran=%d passed=%v", out.Ran, out.Passed())
	}
}

// TestRunNoExamples 空样例表的程序不编译、不报错，只是没有结论。
func TestRunNoExamples(t *testing.T) {
	const src = `fn double(x: u64) -> u64 {
  return x * 2
}

entry main() -> Unit invariant "无" {
  return
}
`
	out, err := Run(load(t, src))
	if err != nil {
		t.Fatalf("运行失败：%v", err)
	}
	if len(out.Cases) != 0 || out.Ran != 0 || out.Passed() {
		t.Fatalf("没有样例时应为空且不通过：%+v", out)
	}
}
