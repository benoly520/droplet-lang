package host

import (
	"os"
	"strings"
	"testing"
)

// TestCapsExistInRuntimeHeader 锁定「能力表 ↔ 运行时声明」的一致性：
// 表里每一项生成的 C 调用，必须在 rt.h 里有对应原型。
//
// 这条测试替代了以前靠人眼核对的步骤——加能力时忘了改 rt.h，这里直接红。
func TestCapsExistInRuntimeHeader(t *testing.T) {
	b, err := os.ReadFile("../runtime/assets/rt.h")
	if err != nil {
		t.Fatalf("读 rt.h 失败：%v", err)
	}
	hdr := string(b)

	if len(Caps) != 10 {
		t.Fatalf("能力表应为 10 项（Sys 4 + BlobFs 5 + Versioning 1），实际 %d", len(Caps))
	}

	for name, c := range Caps {
		call := c.Emit(make([]string, c.Arity), "_e")
		fn := call
		if i := strings.Index(call, "("); i > 0 {
			fn = call[:i]
		}
		if !strings.HasPrefix(fn, "dp_") {
			t.Fatalf("能力 %s 的 emit 应以 dp_ 函数开头，得到 %q", name, call)
		}
		if !strings.Contains(hdr, fn+"(") {
			t.Errorf("能力 %s 生成的调用 %s 在 rt.h 里没有原型", name, fn)
		}
	}
}
