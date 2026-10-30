package check

import (
	"strings"
	"testing"
)

// intentSrc 用一条 requires 包住表达式：意图是表达式进入检查的唯一入口。
func intentSrc(expr string) string {
	return "fn f(x: u64) -> Unit\n  requires " + expr + "\n{\n  return\n}\n"
}

// withEntry 补一个满足 E11 的入口，让被测程序只剩目标错误。
func withEntry(src string) string {
	return src + "\nentry main() -> Unit invariant \"无\" {\n  return\n}\n"
}

// TestIntentExprMustBeBool 锁住最基本的一条：意图表达式以前完全没检查，
// `requires 1` 能一路通过。它必须是 bool，否则"抓 mutation"无从谈起。
func TestIntentExprMustBeBool(t *testing.T) {
	res := checkSrc(t, withEntry(intentSrc("1")))
	expectOne(t, res, "E1", "必须是 bool")
}

// TestIntentConstDecisions 是 E10 里"常量能判"的几种结局：
// 恒真 = 空声明（无约束力），恒假 = 永远无法满足，判不出来就放过。
func TestIntentConstDecisions(t *testing.T) {
	cases := []struct{ expr, code, contains string }{
		{`1 + 1 == 2`, "E10", "恒为 true"},
		{`2 * 3 == 7`, "E10", "恒为 false"},
		{`"a" < "b"`, "E10", "恒为 true"},
		{`b"x" == b"x"`, "E10", "恒为 true"},
		{`false && (x > 0)`, "E10", "恒为 false"},   // 无优先级：要短路就得写括号
		{`x > 0`, "", ""},                         // 参数，判不出来
		{`(2 > 1) && (x > 0)`, "", ""},            // 左侧为真仍要看右侧
		{`18446744073709551615 + 1 == 0`, "", ""}, // 溢出不算数，不静默回绕
		{`1 / 0 == 0`, "", ""},                    // 除零判不出来
	}
	for _, c := range cases {
		res := checkSrc(t, withEntry(intentSrc(c.expr)))
		if c.code == "" {
			if len(res.Errors) != 0 {
				t.Errorf("%s 不该报错，实际：%v", c.expr, res.Errors)
			}
			continue
		}
		expectOne(t, res, c.code, c.contains)
	}
}

// TestExampleConstDecisions 同一套判定对 example 也成立：样例恒真就是空样例。
func TestExampleConstDecisions(t *testing.T) {
	src := `fn f(x: u64) -> Unit
  example 1 == 1
{
  return
}
`
	expectOne(t, checkSrc(t, withEntry(src)), "E10", "恒为 true")
}

// TestRequiresAtCallStaticFalsification 是 §7.2 里 E10 的原文：requires 在调用点
// 可静态判否。实参全是常量时就必须判，报错要指到调用点与具体实参。
func TestRequiresAtCallStaticFalsification(t *testing.T) {
	src := `fn f(x: u64) -> Unit
  requires x > 0
{
  return
}

entry main() -> Unit invariant "无" {
  let u: Unit = f(0)
  return
}
`
	res := checkSrc(t, src)
	expectOne(t, res, "E10", "恒假")
	if !strings.Contains(res.Errors[0].Msg, "x = 0") {
		t.Errorf("报错没指到实参：%s", res.Errors[0].Msg)
	}
	if !strings.Contains(res.Errors[0].Msg, "x > 0") {
		t.Errorf("报错没带上前置条件原文：%s", res.Errors[0].Msg)
	}
}

// TestRequiresAtCallPasses 覆盖三个不该报的情形：实参满足前置条件、
// 实参定不出值（调用方参数）、被调函数根本没有 requires。
func TestRequiresAtCallPasses(t *testing.T) {
	cases := []string{
		// 常量满足前置条件
		`fn f(x: u64) -> Unit
  requires x > 0
{
  return
}
entry main() -> Unit invariant "无" {
  let u: Unit = f(1)
  return
}
`,
		// 实参是调用方的参数：定不出值就放过，不猜
		`fn f(x: u64) -> Unit
  requires x > 0
{
  return
}
entry main(y: u64) -> Unit invariant "无" {
  let u: Unit = f(y)
  return
}
`,
		// 没有 requires 的函数不受影响
		`fn f(x: u64) -> Unit {
  return
}
entry main() -> Unit invariant "无" {
  let u: Unit = f(0)
  return
}
`,
	}
	for i, src := range cases {
		if res := checkSrc(t, src); len(res.Errors) != 0 {
			t.Errorf("第 %d 个用例不该报错，实际：%v", i+1, res.Errors)
		}
	}
}

// TestBoundaryIntentChecked 锁住 boundary 这一侧：方法的意图和参数在同一个 scope 里查，
// 常量判定同样生效（契约方法没有函数体，但意图照样要有牙齿）。
func TestBoundaryIntentChecked(t *testing.T) {
	okSrc := `boundary B @v1 {
  m(x: u64) -> bytes invariant "无" requires x > 0
}
`
	if res := checkSrc(t, okSrc); len(res.Errors) != 0 {
		t.Fatalf("参数可见的 requires 不该报错：%v", res.Errors)
	}

	badSrc := `boundary B @v1 {
  m(x: u64) -> bytes invariant "无" requires 1 == 2
}
`
	expectOne(t, checkSrc(t, badSrc), "E10", "恒为 false")

	typeSrc := `boundary B @v1 {
  m(x: u64) -> bytes invariant "无" requires x
}
`
	expectOne(t, checkSrc(t, typeSrc), "E1", "必须是 bool")
}
