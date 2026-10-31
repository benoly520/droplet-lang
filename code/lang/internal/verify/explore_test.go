package verify

import (
	"strings"
	"testing"

	"droplet/internal/check"
	"droplet/internal/parse"
)

// cand 造一个候选：同一份实现 + entry 上的 example 由调用方给。
func cand(t *testing.T, name, body string, examples ...string) Source {
	t.Helper()
	var ex strings.Builder
	for _, e := range examples {
		ex.WriteString("  example " + e + "\n")
	}
	text := `fn add(a: u64, b: u64) -> u64
  invariant "加法"
{
` + body + `
}

entry main() -> Unit
` + ex.String() + `  invariant "无"
{
  let n = add(1, 2)
  if n == 3 {
    return
  }
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

// TestExplorePicksUniqueSurvivor 是 explore 的主用途：同一条意图，两个候选一过一个不过。
func TestExplorePicksUniqueSurvivor(t *testing.T) {
	good := cand(t, "good.dlt", "  return a + b", "add(1, 2) == 3")
	bad := cand(t, "bad.dlt", "  return a + b + 1", "add(1, 2) == 3")

	r := Explore([]Source{good, bad})
	if len(r.Alive) != 1 || r.Alive[0] != "good.dlt" {
		t.Fatalf("期望只留 good.dlt，得到 %v", r.Alive)
	}
	if len(r.Divergent) != 1 || r.Divergent[0] != "add(1, 2) == 3" {
		t.Fatalf("期望一条分歧，得到 %v", r.Divergent)
	}
	if v := r.Cands[1].Cases["add(1, 2) == 3"]; v != "FAIL" {
		t.Fatalf("bad.dlt 在这条样例上应为 FAIL，得到 %q", v)
	}
}

// TestExploreUndetermined 锁住 §7.8.3 A：多个候选同时存活时不许自动挑一个。
func TestExploreUndetermined(t *testing.T) {
	a := cand(t, "a.dlt", "  return a + b", "add(1, 2) == 3")
	c := cand(t, "c.dlt", "  return a + b", "add(1, 2) == 3", "add(2, 2) == 4")

	r := Explore([]Source{a, c})
	if len(r.Alive) != 2 {
		t.Fatalf("期望两个候选都存活（欠定），得到 %v", r.Alive)
	}
	// a 没声明 c 的第二条样例：这是意图集不一致，不是候选好坏。
	if who := r.OnlyIn["add(2, 2) == 4"]; len(who) != 1 || who[0] != "a.dlt" {
		t.Fatalf("期望只有 a.dlt 缺 add(2, 2) == 4，得到 %v", r.OnlyIn)
	}
	if len(r.Divergent) != 0 {
		t.Fatalf("两边结论一致，不该有分歧：%v", r.Divergent)
	}
}

// TestExploreZeroSurvivors 零存活时要能逐条说出谁被哪条样例否掉（反例对的骨架）。
func TestExploreZeroSurvivors(t *testing.T) {
	b := cand(t, "b.dlt", "  return a + b + 1", "add(1, 2) == 3")
	d := cand(t, "d.dlt", "  return a + b", "add(1, 2) == 4")

	r := Explore([]Source{b, d})
	if len(r.Alive) != 0 {
		t.Fatalf("两个候选都不满足自己的意图，不该有存活：%v", r.Alive)
	}
	if v := r.Cands[0].Cases["add(1, 2) == 3"]; v != "FAIL" {
		t.Fatalf("b.dlt 应被 add(1, 2) == 3 否掉，得到 %q", v)
	}
	if v := r.Cands[1].Cases["add(1, 2) == 4"]; v != "FAIL" {
		t.Fatalf("d.dlt 应被 add(1, 2) == 4 否掉，得到 %q", v)
	}
}

// TestExploreCandidateThatCannotRun 跑不起来的候选只影响它自己，不能被算成存活。
func TestExploreCandidateThatCannotRun(t *testing.T) {
	good := cand(t, "good.dlt", "  return a + b", "add(1, 2) == 3")
	// 服务形态：Run 明确拒绝（实现依赖 env 与 host），explore 要如实记成 Err。
	prog, err := parse.File("svc.dlt", `type ApiError = NotFound

env App {
  port: u64 = 8787,
  bind: str = "0.0.0.0",
  root: str,
  token: str = "",
  max_body: u64 = 8388608
}

boundary Blob @v1 {
  get(key: str) -> bytes
    @get("/v1/blob") impl get with idempotency
    invariant "读"
}

fn get(key: str) -> bytes | ApiError
  invariant "读"
{
  return b""
}
`)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	res := check.Program(prog)
	if len(res.Errors) > 0 {
		t.Fatalf("检查未通过：%v", res.Errors)
	}
	svc := Source{Name: "svc.dlt", Prog: prog, Res: res}

	r := Explore([]Source{good, svc})
	if len(r.Alive) != 1 || r.Alive[0] != "good.dlt" {
		t.Fatalf("只有 good.dlt 该存活，得到 %v", r.Alive)
	}
	if r.Cands[1].Err == nil {
		t.Fatalf("服务形态的候选应记成跑不起来：%+v", r.Cands[1])
	}
}
