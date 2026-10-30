package gen

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"droplet/internal/check"
)

var dpFnRe = regexp.MustCompile(`dp_[a-z0-9_]+\(`)

// TestRuntimeHasNoDeadFunctions 运行时里每个 dp_* 函数都必须有调用点。
//
// 判据与 IMPL-PLAN §8.1 一致：只在 rt.h 的原型和 rt_*.c 的定义里出现、没有任何
// 调用点的就是死代码——D0 据此删过 dp_sys_getenv 与 dp_arena_reset。
//
// 参与统计的 C 有三份：hello、webapi 示例、以及下面的探针。探针负责触达「只有
// 生成器叫得到、现有样例又恰好没写」的几个函数——`==` 对 str / bytes / u64 分别
// 落到 dp_str_eq / dp_bytes_eq / dp_val_eq。
func TestRuntimeHasNoDeadFunctions(t *testing.T) {
	probe := genC(t, source{"probe.dlt", `entry f(s1: str, s2: str, b1: bytes, b2: bytes, u1: u64, u2: u64) -> Unit
  invariant "触达 == 与 bytes 字面量的生成目标"
{
  let a = s1 == s2
  let b = b"probe" == b2
  let c = u1 == u2

}`})
	hello := genC(t, source{"hello.dlt", genSource(t, "../../examples/hello.dlt")})
	webapi := genC(t,
		source{"contract.dlt", genSource(t, "../../../webapi/src/contract.dlt")},
		source{"main.dlt", genSource(t, "../../../webapi/src/main.dlt")})

	hdr, err := os.ReadFile("../runtime/assets/rt.h")
	if err != nil {
		t.Fatal(err)
	}
	srcs, err := filepath.Glob("../runtime/assets/*.c")
	if err != nil {
		t.Fatal(err)
	}

	var all strings.Builder
	all.WriteString(probe)
	all.WriteString(hello)
	all.WriteString(webapi)
	all.Write(hdr)
	for _, f := range srcs {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		all.Write(b)
	}
	body := all.String()

	for _, fn := range declaredFns(string(hdr)) {
		// 至少三次：rt.h 里的原型、rt_*.c 里的定义、一处调用。
		if n := strings.Count(body, fn+"("); n < 3 {
			t.Errorf("运行时函数 %s 只出现 %d 次（原型 + 定义），没有调用点", fn, n)
		}
	}
}

// TestErrTagsMatchRuntime 锁住「变体声明顺序 → tag → HTTP 状态码」这条唯一通道。
// checker 按联合里的错误项从 100 编号，运行时按同一个号映射状态码；两边漂移时，
// handler 返回的错误会静默变成 500 或别的状态码 —— 所以两处都在这里逐项钉死。
func TestErrTagsMatchRuntime(t *testing.T) {
	prog := parseAll(t,
		source{"contract.dlt", genSource(t, "../../../webapi/src/contract.dlt")},
		source{"main.dlt", genSource(t, "../../../webapi/src/main.dlt")})
	res := check.Program(prog)
	if len(res.Errors) > 0 {
		t.Fatalf("契约有 %d 个检查错误：%v", len(res.Errors), res.Errors)
	}
	hdr, err := os.ReadFile("../runtime/assets/rt.h")
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		variant, macro string
		tag            int
	}{
		{"NotFound", "DP_ERR_NOT_FOUND", 100},
		{"BadRequest", "DP_ERR_BAD_REQUEST", 101},
		{"IoError", "DP_ERR_IO", 102},
		{"Conflict", "DP_ERR_CONFLICT", 103},
		{"NotAllowed", "DP_ERR_NOT_ALLOWED", 104},
		{"PreconditionFailed", "DP_ERR_PRECONDITION_FAILED", 105},
	}
	for _, w := range want {
		if got := res.Tags[w.variant]; got != w.tag {
			t.Errorf("变体 %s 的 tag 是 %d，期望 %d（声明顺序变了？）", w.variant, got, w.tag)
		}
		pat := "#define " + w.macro + " " + strconv.Itoa(w.tag) + "u"
		if !strings.Contains(string(hdr), pat) {
			t.Errorf("rt.h 缺少 %q", pat)
		}
	}
}

// genC 只走到 C 文本，不编不跑——这条测试关心的是「谁被调用」，不是行为。
func genC(t *testing.T, srcs ...source) string {
	t.Helper()
	prog := parseAll(t, srcs...)
	res := check.Program(prog)
	if len(res.Errors) > 0 {
		t.Fatalf("源码有 %d 个检查错误：%v", len(res.Errors), res.Errors)
	}
	src, err := C(prog, res)
	if err != nil {
		t.Fatalf("生成失败：%v", err)
	}
	return src
}

// declaredFns 列出 rt.h 里声明的 dp_ 函数名。`#define` 那几行是宏，不参与审计。
func declaredFns(hdr string) []string {
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(hdr, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, m := range dpFnRe.FindAllString(line, -1) {
			name := strings.TrimSuffix(m, "(")
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
