package gen

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// H8（SPEC §9）：单个实现文件 ≤800 行，编译器 + 运行时合计 ≤8000 行。
//
// 这条约束此前一直靠"记得去数"维持 —— 数到本轮已经 97%，余量只剩两百来行。
// 所以把它变成机械判定：越界就在这里红，而不是等下一次有人想加功能时才发现
// 预算已经没了。计数口径与 docs/NEXT.md 一致：`code/lang` 下全部**非测试** Go
// （含工具）+ 运行时的 C 源码。测试文件不计入，否则约束会惩罚证据。
const (
	h8PerFile = 800
	h8Total   = 8000
)

func TestH8Budget(t *testing.T) {
	lang := filepath.Join("..", "..", "..", "..", "code", "lang")
	var goFiles []string
	err := filepath.WalkDir(lang, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		goFiles = append(goFiles, p)
		return nil
	})
	if err != nil {
		t.Fatalf("走不动 %s：%v", lang, err)
	}
	if len(goFiles) == 0 {
		t.Fatalf("%s 下一个实现文件都没有，路径算错了", lang)
	}

	total := 0
	for _, p := range goFiles {
		n := countLines(t, p)
		total += n
		if n > h8PerFile {
			t.Errorf("%s 有 %d 行，超过单文件上限 %d", shortPath(p), n, h8PerFile)
		}
	}

	assets := filepath.Join(lang, "internal", "runtime", "assets")
	entries, err := os.ReadDir(assets)
	if err != nil {
		t.Fatalf("读不了运行时目录：%v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		p := filepath.Join(assets, e.Name())
		n := countLines(t, p)
		total += n
		if n > h8PerFile {
			t.Errorf("运行时文件 %s 有 %d 行，超过单文件上限 %d", e.Name(), n, h8PerFile)
		}
	}

	// 数字每次都打出来：只剩多少余量，是下一次"想加个功能"时唯一要看的数。
	t.Logf("H8：%d / %d 行（%d%%），余量 %d 行", total, h8Total, total*100/h8Total, h8Total-total)
	if total > h8Total {
		t.Errorf("实现总量 %d 行，超过 H8 上限 %d —— 先砍，别抬上限（SPEC §9）", total, h8Total)
	}
}

func countLines(t *testing.T, p string) int {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读不了 %s：%v", p, err)
	}
	n := bytes.Count(data, []byte{'\n'})
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++
	}
	return n
}

// shortPath 把绝对路径压成 code/lang 之后的相对路径，报错时看得清是哪个文件。
func shortPath(p string) string {
	const marker = "code" + string(filepath.Separator) + "lang" + string(filepath.Separator)
	if i := strings.Index(p, marker); i >= 0 {
		return p[i+len(marker):]
	}
	return p
}
