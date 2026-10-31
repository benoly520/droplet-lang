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

// runOpt 是 Run 的测试便利入口：parse/check 与选项分开传。
func runOpt(t *testing.T, src string, opt Options) (*Result, error) {
	t.Helper()
	prog, res := load(t, src)
	return Run(prog, res, opt)
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
	out, err := runOpt(t, src, Options{})
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
	out, err := runOpt(t, src, Options{})
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
	out, err := runOpt(t, src, Options{})
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
	out, err := runOpt(t, src, Options{})
	if err != nil {
		t.Fatalf("运行失败：%v", err)
	}
	if len(out.Cases) != 0 || out.Ran != 0 || out.Passed() {
		t.Fatalf("没有样例时应为空且不通过：%+v", out)
	}
}

// TestMutateCatchesAndMisses 是 E10 证据面的核心：同一条实现里，
// 被样例覆盖到的常量/边界要被抓住，没被覆盖到的要如实记成 MISS。
func TestMutateCatchesAndMisses(t *testing.T) {
	const src = `fn sign(x: u64) -> u64 {
  if x < 10 {
    return 1
  }
  return 2
}

entry main() -> Unit
  example sign(9) == 1
  invariant "无"
{
  return
}
`
	out, err := runOpt(t, src, Options{Mutate: true})
	if err != nil {
		t.Fatalf("运行失败：%v", err)
	}
	// 四处变异：x < 10 的两个常量与边界，以及 return 1 / return 2 的常量。
	if len(out.Mutants) != 4 {
		t.Fatalf("期望 4 处变异，得到 %d：%+v", len(out.Mutants), out.Mutants)
	}
	caught, missed := 0, 0
	for _, m := range out.Mutants {
		if m.CaughtBy == "" {
			missed++
		} else {
			caught++
		}
	}
	if caught != 1 || missed != 3 {
		t.Fatalf("期望 1 抓住 / 3 存活，得到 %d / %d：%+v", caught, missed, out.Mutants)
	}
	for _, m := range out.Mutants {
		if m.CaughtBy != "" && m.CaughtBy != "sign(9) == 1" {
			t.Errorf("抓住它的样例应为 sign(9) == 1，得到 %q", m.CaughtBy)
		}
	}
}

// TestMutateOffByDefault 锁住：不开选项就不编译变异，别让 verify 悄悄变慢。
func TestMutateOffByDefault(t *testing.T) {
	const src = `fn double(x: u64) -> u64 {
  return x * 2
}

entry main() -> Unit
  example double(3) == 6
  invariant "无"
{
  return
}
`
	out, err := runOpt(t, src, Options{})
	if err != nil {
		t.Fatalf("运行失败：%v", err)
	}
	if len(out.Mutants) != 0 {
		t.Fatalf("没开 Mutate 时不该有变异报告：%+v", out.Mutants)
	}
}
