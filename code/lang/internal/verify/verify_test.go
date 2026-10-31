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
	// 五处变异，覆盖两条轴：表达式轴（x < 10 的两个常量与边界、两个 return 的常量）
	// 与语句轴（清空 then、删掉守卫里的 return）。
	if len(out.Mutants) != 5 {
		t.Fatalf("期望 5 处变异，得到 %d：%+v", len(out.Mutants), out.Mutants)
	}
	caught, missed := 0, 0
	for _, m := range out.Mutants {
		if m.CaughtBy == "" {
			missed++
		} else {
			caught++
		}
	}
	// sign(9) == 1 只看得见"x=9 走哪条路"，所以边界收紧与 return 2 的常量都活着。
	if caught != 3 || missed != 2 {
		t.Fatalf("期望 3 抓住 / 2 存活，得到 %d / %d：%+v", caught, missed, out.Mutants)
	}
	// 语句轴的两处必须被抓住：删掉 then 或删掉 return，sign(9) 都变成 2。
	var stmtAxis int
	for _, m := range out.Mutants {
		if strings.HasPrefix(m.Desc, "分支删除") || strings.HasPrefix(m.Desc, "提前返回") {
			stmtAxis++
			if m.CaughtBy != "sign(9) == 1" {
				t.Errorf("语句轴变异 %s 应被 sign(9) == 1 抓住，得到 %q", m.Desc, m.CaughtBy)
			}
		}
	}
	if stmtAxis != 2 {
		t.Fatalf("语句轴应有两处变异（分支删除、提前返回），得到 %d：%+v", stmtAxis, out.Mutants)
	}
	for _, m := range out.Mutants {
		if m.CaughtBy != "" && m.CaughtBy != "sign(9) == 1" {
			t.Errorf("抓住它的样例应为 sign(9) == 1，得到 %q", m.CaughtBy)
		}
	}
}

// TestMutateMatchArms 把语句轴压到 §7.8.1 的原文上：删掉一条 match 分支的处理体。
// 一条样例只走一条分支，所以另一条分支的两处变异必须如实活着 —— 这就是
// "意图只覆盖了半个程序"的可执行证据。
func TestMutateMatchArms(t *testing.T) {
	const src = `type Shape = Circle | Square

fn sides(s: Shape) -> u64 {
  match s {
    Circle => {
      return 4
    },
    Square => {
      return 3
    }
  }
}

entry main() -> Unit
  example sides(Shape.Circle) == 4
  invariant "无"
{
  let n = sides(Shape.Circle)
  if n == 4 {
    return
  }
  return
}
`
	out, err := runOpt(t, src, Options{Mutate: true})
	if err != nil {
		t.Fatalf("运行失败：%v", err)
	}
	var arms, caughtCircle, missSquare int
	for _, m := range out.Mutants {
		if !strings.HasPrefix(m.Desc, "分支删除 清空 match 分支") {
			continue
		}
		arms++
		switch m.CaughtBy {
		case "sides(Shape.Circle) == 4":
			caughtCircle++
		case "":
			missSquare++
		}
	}
	if arms != 2 {
		t.Fatalf("每个 match 分支应各有一处清空变异，得到 %d：%+v", arms, out.Mutants)
	}
	if caughtCircle != 1 || missSquare != 1 {
		t.Fatalf("期望走到的分支被抓住、没走到的分支存活，得到 caught=%d miss=%d：%+v",
			caughtCircle, missSquare, out.Mutants)
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
