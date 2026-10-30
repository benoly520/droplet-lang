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
		call := c.Emit(make([]string, c.Arity()), "_e")
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

// TestEmitKeepsArguments 兜住 Emit 的两个易错点：漏掉参数、包装与返回类型脱节。
func TestEmitKeepsArguments(t *testing.T) {
	argv := []string{"A0", "A1", "A2"}
	for name, c := range Caps {
		call := c.Emit(argv[:c.Arity()], "_e")
		for i := 0; i < c.Arity(); i++ {
			if !strings.Contains(call, argv[i]) {
				t.Errorf("%s 的生成代码 %q 漏掉了第 %d 个参数", name, call, i+1)
			}
		}
		if (c.RetName != "") != (c.Wrap != nil) {
			t.Errorf("%s：RetName=%q 与“是否有 Wrap”必须同时成立", name, c.RetName)
		}
		if c.Arity() != len(c.Params) {
			t.Errorf("%s：Arity() 与 Params 长度不一致", name)
		}
	}
}

// TestTyString 运行时类型名会直接出现在 E2 的报错里，写错会误导用户。
func TestTyString(t *testing.T) {
	for ty, want := range map[Ty]string{
		Str: "str", Bytes: "bytes", U64: "u64", I64: "i64", Bool: "bool", Unit: "Unit",
	} {
		if got := ty.String(); got != want {
			t.Errorf("Ty(%d).String() = %q，期望 %q", ty, got, want)
		}
	}
}

func TestLookup(t *testing.T) {
	if _, ok := Lookup("Sys.log"); !ok {
		t.Error("Sys.log 应在表里")
	}
	if _, ok := Lookup("Sys.nope"); ok {
		t.Error("Sys.nope 不应在表里")
	}
}
