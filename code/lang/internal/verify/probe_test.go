package verify

import (
	"strings"
	"testing"

	"droplet/internal/check"
	"droplet/internal/parse"
)

// probeCand 造一个只有 `big(n: u64) -> bool` 的候选，意图由调用方给。
func probeCand(t *testing.T, name, body string, examples ...string) Source {
	t.Helper()
	var ex strings.Builder
	for _, e := range examples {
		ex.WriteString("  example " + e + "\n")
	}
	text := `fn big(n: u64) -> bool
  invariant "阈值"
{
` + body + `
}

entry main() -> Unit
` + ex.String() + `  invariant "无"
{
  return
}
`
	prog, err := parse.File(name, text)
	if err != nil {
		t.Fatalf("%s 解析失败：%v", name, err)
	}
	res := check.Program(prog)
	if len(res.Errors) > 0 {
		t.Fatalf("%s 检查未通过：%v", name, res.Errors)
	}
	return Source{Name: name, Prog: prog, Res: res}
}

// TestProbeSetCoversBoundaries 是这一半的存在理由：边界两侧必须都在池子里，
// 否则 `>` 与 `>=` 这种差异永远抓不到。
func TestProbeSetCoversBoundaries(t *testing.T) {
	srcs := []Source{
		probeCand(t, "a", "  if n > 8 {\n    return true\n  }\n  return false", "big(3) == false"),
		probeCand(t, "b", "  if n >= 8 {\n    return true\n  }\n  return false", "big(3) == false"),
	}
	probes := ProbeSet(srcs)
	if len(probes) == 0 {
		t.Fatal("没有生成任何输入")
	}
	want := "big(8)"
	for _, p := range probes {
		if p.Label == want {
			return
		}
	}
	t.Fatalf("生成的输入里没有 %s：%v", want, labels(probes))
}

// TestRunProbesSeesValueDifference 跑真二进制：两条实现只在 n=8 上不同。
func TestRunProbesSeesValueDifference(t *testing.T) {
	a := probeCand(t, "a", "  if n > 8 {\n    return true\n  }\n  return false", "big(3) == false")
	b := probeCand(t, "b", "  if n >= 8 {\n    return true\n  }\n  return false", "big(3) == false")
	probes := ProbeSet([]Source{a, b})

	av, err := RunProbes(a.Prog, a.Res, probes)
	if err != nil {
		t.Fatalf("a 跑不起来：%v", err)
	}
	bv, err := RunProbes(b.Prog, b.Res, probes)
	if err != nil {
		t.Fatalf("b 跑不起来：%v", err)
	}
	if len(av) != len(probes) || len(bv) != len(probes) {
		t.Fatalf("结果条数不对：%d / %d / %d", len(av), len(bv), len(probes))
	}

	diffs := 0
	for i := range probes {
		if av[i] != bv[i] {
			diffs++
			if probes[i].Label != "big(8)" {
				t.Errorf("分歧出现在意料之外的输入上：%s（a=%s b=%s）", probes[i].Label, av[i], bv[i])
			}
		}
	}
	if diffs != 1 {
		t.Fatalf("应当只在 big(8) 上分歧，实际 %d 条", diffs)
	}
}

// TestExploreReportsProbeDivergence explore 要把这条分歧报到报告里。
func TestExploreReportsProbeDivergence(t *testing.T) {
	r := Explore([]Source{
		probeCand(t, "a", "  if n > 8 {\n    return true\n  }\n  return false", "big(3) == false"),
		probeCand(t, "b", "  if n >= 8 {\n    return true\n  }\n  return false", "big(3) == false"),
	})
	if r.Probes == 0 {
		t.Fatal("报告里没有自动输入条数")
	}
	if len(r.ProbeDiff) != 1 || r.ProbeDiff[0].Label != "big(8)" {
		t.Fatalf("应当报一条 big(8) 的分歧，实际 %v", r.ProbeDiff)
	}
	if r.ProbeDiff[0].Vals["a"] == r.ProbeDiff[0].Vals["b"] {
		t.Errorf("两条候选的值不该相同：%v", r.ProbeDiff[0].Vals)
	}
}

// TestExploreNoProbeForStrParams 参数含 str 时不造输入（这一批的边界），
// 但样例那一半照常工作 —— 缺口要说清楚，而不是假装覆盖了。
func TestExploreNoProbeForStrParams(t *testing.T) {
	mk := func(name string) Source {
		text := `fn pick(s: str) -> u64
  invariant "长度"
{
  if s == "x" {
    return 1
  }
  return 0
}

entry main() -> Unit
  example pick("x") == 1
  invariant "无"
{
  return
}
`
		prog, err := parse.File(name, text)
		if err != nil {
			t.Fatalf("%s 解析失败：%v", name, err)
		}
		res := check.Program(prog)
		if len(res.Errors) > 0 {
			t.Fatalf("%s 检查未通过：%v", name, res.Errors)
		}
		return Source{Name: name, Prog: prog, Res: res}
	}
	r := Explore([]Source{mk("a"), mk("b")})
	if r.Probes != 0 || len(r.ProbeDiff) != 0 {
		t.Fatalf("str 参数不该生成输入，实际 Probes=%d diff=%d", r.Probes, len(r.ProbeDiff))
	}
	if len(r.Alive) != 2 {
		t.Fatalf("样例那一半应照常判定，存活 %v", r.Alive)
	}
}

// TestProbeCrashIsIsolated 是"一条输入一个进程"的理由：除零会杀掉进程，
// 隔离让崩溃只丢这一条，并且**崩溃本身是一条可比较的结果**。
//
// 它还钉住了一个反直觉的点：探针必须用 -O0 编译。参数是字面量，-O2 会把函数体
// 折进 switch 再按"除零是 UB"优化成任意值而不 trap —— 那样崩溃永远看不见。
func TestProbeCrashIsIsolated(t *testing.T) {
	mk := func(name, body string) Source {
		text := `fn pct(part: u64, whole: u64) -> u64
  invariant "百分比"
{
` + body + `
}

entry main() -> Unit
  example pct(1, 4) == 25
  invariant "无"
{
  return
}
`
		prog, err := parse.File(name, text)
		if err != nil {
			t.Fatalf("%s 解析失败：%v", name, err)
		}
		res := check.Program(prog)
		if len(res.Errors) > 0 {
			t.Fatalf("%s 检查未通过：%v", name, res.Errors)
		}
		return Source{Name: name, Prog: prog, Res: res}
	}
	a := mk("a", "  return part * 100 / whole")
	b := mk("b", "  if whole == 0 {\n    return 0\n  }\n  return part * 100 / whole")

	r := Explore([]Source{a, b})
	if len(r.ProbeDiff) == 0 {
		t.Fatal("除零应当被报成分歧")
	}
	d := r.ProbeDiff[0]
	if d.Label != "pct(1, 0)" {
		t.Errorf("分歧应当出现在 pct(1, 0)，实际 %s", d.Label)
	}
	if d.Vals["a"] != "崩溃" || d.Vals["b"] != "0" {
		t.Errorf("崩溃要被记成结果：%v", d.Vals)
	}
}

func labels(ps []Probe) []string {
	out := make([]string, len(ps))
	for i := range ps {
		out[i] = ps[i].Label
	}
	return out
}
