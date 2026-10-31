package verify

import (
	"os"
	"path/filepath"
	"testing"

	"droplet/internal/check"
	"droplet/internal/parse"
)

// repoRoot 是 code/lang 相对仓库根的位置，与 budget_test.go 同一口径。
func repoRoot() string { return filepath.Join("..", "..", "..", "..") }

// TestWebapiIntentsAreExecutable 是 SPEC 附录 D.9 / NEXT P11 路 (a) 的护栏：
// 服务本体带路由，`Run` 会直接拒绝，所以这个仓库里唯一能产出可执行证据的地方
// 就是 `code/webapi/intents.dlt`。它必须**真的跑出非零样例**，否则"人只审目标"
// 仍然停在散文层（全仓库 4 个 .dlt 里的 example 一度是 0 条）。
func TestWebapiIntentsAreExecutable(t *testing.T) {
	path := filepath.Join(repoRoot(), "code", "webapi", "intents.dlt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读不到意图模型 %s：%v", path, err)
	}
	prog, err := parse.File(path, string(data))
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	res := check.Program(prog)
	if len(res.Errors) > 0 {
		t.Fatalf("检查失败：%v", res.Errors)
	}
	if len(res.Routes) > 0 {
		t.Fatalf("意图模型不该有路由（有路由 verify 会拒绝）")
	}

	out, err := Run(prog, res, Options{Mutate: true})
	if err != nil {
		t.Fatalf("verify 失败：%v", err)
	}
	if out.Ran == 0 {
		t.Fatalf("一条可执行样例都没有 —— 意图没有牙齿（§7.8.1）")
	}
	if !out.Passed() {
		for _, c := range out.Cases {
			t.Logf("%s skip=%q ok=%v", c.Src, c.Skip, c.OK)
		}
		t.Fatalf("意图模型自己不成立")
	}
	// 真值表写全的意义就在这条：每处变异都得有一条样例翻面。
	for _, m := range out.Mutants {
		if m.CaughtBy == "" {
			t.Errorf("变异没被抓住（意图对该处为零捕获）：%s %s", m.Pos, m.Desc)
		}
	}
	if len(out.Mutants) == 0 {
		t.Errorf("一个变异都没生成 —— 实现体可能退化成单表达式了")
	}
}
