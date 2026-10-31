package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"droplet/internal/ast"
	"droplet/internal/check"
	"droplet/internal/parse"
	rt "droplet/internal/runtime"
)

// source 是一个编译单元里的一个文件。顺序即声明顺序，不能打乱。
type source struct {
	name string
	text string
}

// build 走完 parse → check → gen → cc 全链路，返回可执行文件路径。
func build(t *testing.T, srcs ...source) string {
	t.Helper()

	prog := parseAll(t, srcs...)
	res := check.Program(prog)
	if len(res.Errors) > 0 {
		for _, e := range res.Errors {
			t.Log(e.Error())
		}
		t.Fatalf("%d 个检查错误", len(res.Errors))
	}

	src, err := C(prog, res)
	if err != nil {
		t.Fatalf("生成失败：%v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gen.c"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rt.WriteTo(dir); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(dir, "prog")
	ccArgs := []string{"-std=c11", "-O2", "-Wall", "-Wextra", "-Werror",
		"-o", bin, filepath.Join(dir, "gen.c"), "-I", dir}
	for _, name := range rt.Names {
		if strings.HasSuffix(name, ".c") {
			ccArgs = append(ccArgs, filepath.Join(dir, name))
		}
	}
	cc := exec.Command("cc", ccArgs...)
	out, err := cc.CombinedOutput()
	if err != nil {
		t.Fatalf("cc 失败：%v\n%s\n--- 源码 ---\n%s", err, out, src)
	}
	return bin
}

func genSource(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestHelloGeneratesLogCall(t *testing.T) {
	prog := parseAll(t, source{"hello.dlt", genSource(t, "../../examples/hello.dlt")})
	res := check.Program(prog)
	if len(res.Errors) > 0 {
		t.Fatalf("检查未通过：%v", res.Errors)
	}

	src, err := C(prog, res)
	if err != nil {
		t.Fatalf("生成失败：%v", err)
	}
	for _, want := range []string{
		`dp_sys_log(dp_str_lit("world"))`,
		"static dp_unit dp_f_greet(dp_arena* a)",
		"dp_f_greet(a);",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("生成的 C 缺少 %q\n%s", want, src)
		}
	}
}

func TestHelloBuildsAndPrintsWorld(t *testing.T) {
	bin := build(t, source{"hello.dlt", genSource(t, "../../examples/hello.dlt")})

	out, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatalf("运行失败：%v", err)
	}
	if got := string(out); got != "world\n" {
		t.Fatalf("输出 %q，期望 %q", got, "world\n")
	}
}

func TestContractTypeMapping(t *testing.T) {
	// 契约与实现要一起过检查：`impl <fn>` 的实现函数住在 main.dlt。
	prog := parseAll(t,
		source{"contract.dlt", genSource(t, "../../../webapi/src/contract.dlt")},
		source{"main.dlt", genSource(t, "../../../webapi/src/main.dlt")})
	res := check.Program(prog)
	if len(res.Errors) > 0 {
		t.Fatalf("检查未通过：%v", res.Errors)
	}

	out, err := C(prog, res)
	if err != nil {
		t.Fatalf("生成失败：%v", err)
	}

	for _, want := range []string{
		"struct dp_T_ETag {",
		"uint64_t m_hash;",
		"  uint16_t tag;",
		"struct { uint8_t _pad; } NotFound;",
		"struct { uint8_t _pad; } Conflict;",
		"dp_R_blob_get {",
		"dp_unit ok;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("生成的 C 缺少 %q", want)
		}
	}
}

// TestPropagateCompiles 验证 `!` 生成 tag 检查与提前返回，且能通过 -Werror 编译。
func TestPropagateCompiles(t *testing.T) {
	src := `type Boom = Bad | Worse

fn step(x: u64) -> u64 | Boom
  invariant "step 原样返回 x"
{
  return x
}

fn twice(x: u64) -> u64 | Boom
  invariant "twice 等于连续两次 step"
{
  let a = step(x)!
  let b = step(a)!
  return b
}
`
	bin := build(t, source{"prop.dlt", src})
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("可执行文件不存在：%v", err)
	}
}

// shapeSrc 是一个带载荷的变体：构造 + match 绑定字段。
const shapeSrc = `host Sys {
  fn log(str) -> Unit
}

type Shape = Circle | Square { side: u64 }

entry greet(sys: Sys) -> Unit
  invariant "构造 Square 后 match 绑定 side，宽为 7 时打印 wide"
{
  let s = Shape.Square { side: 7 }
  match s {
    Circle => {
      sys.log("round")
    },
    Square { side } => {
      if side == 7 {
        sys.log("wide")
      }
    }
  }
}
`

// TestVariantConstructShape 验证变体在 C 里的形状：union 成员带真字段，构造写 tag + 载荷。
func TestVariantConstructShape(t *testing.T) {
	prog := parseAll(t, source{"shape.dlt", shapeSrc})
	res := check.Program(prog)
	if len(res.Errors) > 0 {
		t.Fatalf("检查未通过：%v", res.Errors)
	}
	out, err := C(prog, res)
	if err != nil {
		t.Fatalf("生成失败：%v", err)
	}
	for _, want := range []string{
		"struct { uint64_t m_side; } Square;",
		"struct { uint8_t _pad; } Circle;",
		".u.Square = { .m_side = 7ull }",
		".u.Square.m_side;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("生成的 C 缺少 %q\n%s", want, out)
		}
	}
}

// TestVariantConstructRuns 跑完整链路：.dlt → C → cc → 二进制 → stdout。
func TestVariantConstructRuns(t *testing.T) {
	bin := build(t, source{"shape.dlt", shapeSrc})
	out, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatalf("运行失败：%v", err)
	}
	if got := string(out); got != "wide\n" {
		t.Fatalf("输出 %q，期望 %q", got, "wide\n")
	}
}

// TestExhaustiveMatchGetsFallbackReturn 锁住一个真被踩到的坑：非 Unit 返回的函数，
// 函数体只有一条穷尽 match、每条分支都 return 时，生成的是不带 default 的 switch，
// C 编译器证明不了它覆盖了所有 tag —— 于是缺兜底 return 就是 -Werror=return-type。
// 判据取保守的一边：match 不参与"必定离开"的推断，宁可多一条不可达 return。
func TestExhaustiveMatchGetsFallbackReturn(t *testing.T) {
	const src = `type Shape = Circle | Square

fn sides(s: Shape) -> u64
  invariant "sides 返回 4 或 3"
{
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
  invariant "无"
{
  let n = sides(Shape.Circle)
  if n == 4 {
    return
  }
  return
}
`
	bin := build(t, source{"sides.dlt", src})
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("可执行文件不存在：%v", err)
	}
}

func parseAll(t *testing.T, srcs ...source) *ast.Program {
	t.Helper()
	prog := &ast.Program{}
	for _, s := range srcs {
		p, err := parse.File(s.name, s.text)
		if err != nil {
			t.Fatalf("解析 %s 失败：%v", s.name, err)
		}
		prog.Items = append(prog.Items, p.Items...)
	}
	return prog
}
