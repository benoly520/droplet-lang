package check

import (
	"os"
	"strings"
	"testing"

	"droplet/internal/ast"
	"droplet/internal/parse"
)

func checkSrc(t *testing.T, src string) *Result {
	t.Helper()
	prog, err := parse.File("t.dlt", src)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	return Program(prog)
}

func checkFiles(t *testing.T, files ...string) *Result {
	t.Helper()
	prog := &ast.Program{File: "<unit>"}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读取 %s 失败：%v", f, err)
		}
		p, err := parse.File(f, string(src))
		if err != nil {
			t.Fatalf("解析 %s 失败：%v", f, err)
		}
		prog.Items = append(prog.Items, p.Items...)
	}
	return Program(prog)
}

// ---------- 正向：三个样例必须零错误 ----------

func TestHelloPasses(t *testing.T) {
	res := checkFiles(t, "../../examples/hello.dlt")
	if len(res.Errors) != 0 {
		t.Fatalf("hello.dlt 应有 0 个错误，实际：%v", res.Errors)
	}
	if res.EntryPoint != "greet" {
		t.Errorf("入口为 %q，期望 greet", res.EntryPoint)
	}
}

func TestWebapiPasses(t *testing.T) {
	res := checkFiles(t,
		"../../../webapi/src/contract.dlt",
		"../../../webapi/src/main.dlt")
	if len(res.Errors) != 0 {
		t.Fatalf("示例服务应有 0 个错误，实际：%v", res.Errors)
	}
	if res.EntryPoint != "handle" {
		t.Errorf("入口为 %q，期望 handle", res.EntryPoint)
	}
}

// ---------- tag 编号 ----------

func TestErrorVariantTags(t *testing.T) {
	res := checkFiles(t,
		"../../../webapi/src/contract.dlt",
		"../../../webapi/src/main.dlt")
	if res.Tags["<ok>"] != 0 {
		t.Errorf("成功 tag 应为 0，得到 %d", res.Tags["<ok>"])
	}
	// ApiError 的四个变体进入过 `!` 传播，应从 100 起顺序编号。
	want := map[string]int{"NotFound": 100, "BadRequest": 101, "IoError": 102, "Conflict": 103}
	for name, tag := range want {
		if res.Tags[name] != tag {
			t.Errorf("%s 的 tag 为 %d，期望 %d", name, res.Tags[name], tag)
		}
	}
}

// ---------- 反向：每类错误各命中一次，且不级联 ----------

const errPrelude = `
type Bad = Nope
host BlobFs {
  fn read(str, str) -> bytes | Bad
}
`

func TestUndefinedType(t *testing.T) {
	res := checkSrc(t, `type A { x: Nope }`)
	expectOne(t, res, "E1", "未定义类型")
}

func TestUndefinedFunction(t *testing.T) {
	res := checkSrc(t, `entry E {
  fn f() -> Unit
    invariant "无"
  {
    nope()

  }
}`)
	expectOne(t, res, "E1", "未定义函数")
}

func TestWrongArgCount(t *testing.T) {
	res := checkSrc(t, `fn g(a: u64) -> Unit {}

entry E {
  fn f() -> Unit
    invariant "无"
  {
    g()

  }
}`)
	expectOne(t, res, "E1", "需要 1 个参数")
}

func TestMissingField(t *testing.T) {
	res := checkSrc(t, `type R { a: u64, b: u64 }

entry E {
  fn f() -> Unit
    invariant "无"
  {
    let r = R{a: 1}

  }
}`)
	expectOne(t, res, "E1", "缺少字段 b")
}

func TestMatchNotExhaustive(t *testing.T) {
	res := checkSrc(t, `type S = A | B

entry E {
  fn f(x: S) -> Unit
    invariant "无"
  {
    match x {
      A => Unit
    }

  }
}`)
	expectOne(t, res, "E1", "未穷尽")
}

func TestPropagateWithoutUnionReturn(t *testing.T) {
	res := checkSrc(t, errPrelude+`
entry E {
  fn f(fs: BlobFs) -> bytes
    invariant "无"
  {
    let b = fs.read("r", "k")!
    return b
  }
}`)
	expectOne(t, res, "E1", "不是联合")
}

func TestPropagateWithMissingError(t *testing.T) {
	res := checkSrc(t, errPrelude+`
type Other = Oops

entry E {
  fn f(fs: BlobFs) -> bytes | Other
    invariant "无"
  {
    let b = fs.read("r", "k")!
    return b
  }
}`)
	expectOne(t, res, "E1", "缺少错误")
}

func TestDroppedError(t *testing.T) {
	res := checkSrc(t, errPrelude+`
entry E {
  fn f(fs: BlobFs) -> Unit
    invariant "无"
  {
    fs.read("r", "k")

  }
}`)
	expectOne(t, res, "E1", "被静默丢弃")
}

func TestEntryWithoutIntent(t *testing.T) {
	res := checkSrc(t, `entry E {
  fn f() -> Unit {

  }
}`)
	expectOne(t, res, "E11", "缺少 example 或 invariant")
}

// 返回 Unit 的函数不写 return：`return Unit` 是零信息的重复书写。
func TestReturnUnitRejected(t *testing.T) {
	res := checkSrc(t, `entry E {
  fn f() -> Unit
    invariant "无"
  {
    return Unit
  }
}`)
	expectOne(t, res, "E1", "`return Unit` 是多余的")
}

// Unit 函数不写 return 是合法的（hello.dlt 就是这么写的）。
func TestUnitWithoutReturnAccepted(t *testing.T) {
	res := checkSrc(t, `entry E {
  fn f() -> Unit
    invariant "无"
  {
  }
}`)
	if len(res.Errors) != 0 {
		t.Fatalf("Unit 函数不写 return 应当通过，得到 %v", res.Errors)
	}
}

// 非 Unit 函数里写 `return Unit` 仍然是类型错误，不是"多余"。
func TestReturnUnitInNonUnitFunction(t *testing.T) {
	res := checkSrc(t, `entry E {
  fn f() -> u64
    invariant "无"
  {
    return Unit
  }
}`)
	expectOne(t, res, "E1", "返回 Unit")
}

func TestBoundaryUnserializable(t *testing.T) {
	// 用真实能力（Sys.log）做参数，免得跟 E2「运行时不提供能力」撞在一起。
	res := checkSrc(t, `host Sys { fn log(str) -> Unit }

boundary B @v1 {
  m() -> Sys
    invariant "返回 host 不能跨边界"
}`)
	expectOne(t, res, "E3", "不可跨边界序列化")
}

func TestHostCapabilityNotInRuntime(t *testing.T) {
	res := checkSrc(t, `host Sys { fn nope(u64) -> Unit }

entry E {
  fn f(sys: Sys) -> Unit
    invariant "无"
  {
    sys.nope(1)

  }
}`)
	expectOne(t, res, "E2", "运行时不提供能力 Sys.nope")
}

func TestHostCapabilityArity(t *testing.T) {
	// 能力名对、参数个数不对：声明了 2 个，运行时原型是 1 个。
	res := checkSrc(t, `host Sys { fn log(str, str) -> Unit }

entry E {
  fn f(sys: Sys) -> Unit
    invariant "无"
  {
    sys.log("a", "b")

  }
}`)
	expectOne(t, res, "E2", "需要 1 个参数，声明了 2")
}

func TestHostCapabilityParamType(t *testing.T) {
	// 声明谎报参数类型：以前 check 返回 ok，要等 cc 报 incompatible type。
	res := checkSrc(t, `host Sys { fn log(u64) -> Unit }

entry E {
  fn f(sys: Sys) -> Unit
    invariant "无"
  {
    sys.log(1)

  }
}`)
	expectOne(t, res, "E2", "Sys.log 的第 1 个参数运行时是 str，声明为 u64")
}

func TestHostCapabilityRetType(t *testing.T) {
	res := checkSrc(t, `host Sys { fn now_unix() -> str }

entry E {
  fn f(sys: Sys) -> Unit
    invariant "无"
  {
    let t = sys.now_unix()

  }
}`)
	expectOne(t, res, "E2", "Sys.now_unix 运行时返回 i64，声明为 str")
}

func TestHostCapabilityRecordParam(t *testing.T) {
	// 能力参数只能是标量：record 传不进运行时原型。
	res := checkSrc(t, `type ETag {
  hash: u64
}

host BlobFs {
  fn read(ETag, str) -> bytes
}

entry E {
  fn f(fs: BlobFs) -> Unit
    invariant "无"
  {

  }
}`)
	expectOne(t, res, "E2", "BlobFs.read 的第 1 个参数不能是 ETag")
}

// 错误变体也能带载荷（以前 checker 直接拒）。
func TestVariantWithPayloadAccepted(t *testing.T) {
	res := checkSrc(t, `type Bad = Err { code: u64 }

entry E {
  fn f() -> u64 | Bad
    invariant "无"
  {
    return Bad.Err { code: 1 }
  }
}`)
	if len(res.Errors) != 0 {
		t.Fatalf("错误变体带字段构造应当通过，得到 %v", res.Errors)
	}
}

// 变体构造：`T.V` 零字段、`T.V { … }` 带载荷，都要通过。
func TestVariantConstructAccepted(t *testing.T) {
	res := checkSrc(t, `type Shape = Circle | Square { side: u64 }

entry E {
  fn f(n: u64) -> Shape
    invariant "宽为 0 时回退成圆"
  {
    if n == 0 {
      return Shape.Circle
    }
    return Shape.Square { side: n }
  }
}`)
	if len(res.Errors) != 0 {
		t.Fatalf("变体构造应当通过，得到 %v", res.Errors)
	}
}

func TestVariantWithoutPayload(t *testing.T) {
	// 带字段的变体裸写：告诉你怎么写，而不是只说类型不对。
	res := checkSrc(t, `type Shape = Circle | Square { side: u64 }

entry E {
  fn f() -> Shape
    invariant "无"
  {
    return Shape.Square
  }
}`)
	expectOne(t, res, "E1", "要写成 `Shape.Square { … }`")
}

func TestVariantUnknown(t *testing.T) {
	res := checkSrc(t, `type Shape = Circle

entry E {
  fn f() -> Shape
    invariant "无"
  {
    return Shape.Triangle
  }
}`)
	expectOne(t, res, "E1", "Shape 没有变体 Triangle")
}

func TestVariantPayloadType(t *testing.T) {
	res := checkSrc(t, `type Shape = Circle | Square { side: u64 }

entry E {
  fn f() -> Shape
    invariant "无"
  {
    return Shape.Square { side: "x" }
  }
}`)
	expectOne(t, res, "E1", "Shape.Square.side 应为 u64")
}

// tag 按变体名全局分配，同名变体跨 sum 会让 tag 撞车。
func TestVariantNameGloballyUnique(t *testing.T) {
	res := checkSrc(t, `type A = X | XOnly { a: u64 }
type B = X | BOnly { b: u64 }

entry E {
  fn f() -> A
    invariant "无"
  {
    return A.X
  }
}`)
	expectOne(t, res, "E1", "变体名 X 已被 A.X 使用")
}

// sum 值取不出字段：要用 match 绑定。
func TestSumFieldAccessRejected(t *testing.T) {
	res := checkSrc(t, `type Shape = Circle | Square { side: u64 }

entry E {
  fn f() -> u64
    invariant "无"
  {
    let s = Shape.Circle
    return s.side
  }
}`)
	expectOne(t, res, "E1", "用 match 绑定变体字段")
}

func TestNoCascade(t *testing.T) {
	// 两个独立错误应报 2 条，而不是因级联膨胀。
	res := checkSrc(t, `type R { a: u64, b: u64 }

entry E {
  fn f() -> Unit
    invariant "无"
  {
    let r = R{a: 1}
    let s = R{a: "文本"}

  }
}`)
	if len(res.Errors) < 2 {
		t.Fatalf("应至少报 2 条错误，实际 %d 条：%v", len(res.Errors), res.Errors)
	}
	if len(res.Errors) > 4 {
		t.Fatalf("错误数 %d 过多，疑似级联：%v", len(res.Errors), res.Errors)
	}
}

func TestEnvDeclRejected(t *testing.T) {
	// SPEC §5.7 把 env 列为关键字，但 D0 不实现；不报错就是静默丢配置。
	res := checkSrc(t, `env App {
  port: u64
}

entry E {
  fn f() -> Unit
    invariant "无"
  {

  }
}`)
	expectOne(t, res, "E1", "不在 D0 子集")
}

// expectOne 断言恰好有一条指定错误码的错误，且消息包含关键字。
func expectOne(t *testing.T, res *Result, code, contains string) {
	t.Helper()
	if len(res.Errors) != 1 {
		t.Fatalf("期望恰好 1 条错误，实际 %d 条：%v", len(res.Errors), res.Errors)
	}
	e := res.Errors[0]
	if e.Code != code {
		t.Errorf("错误码为 %s，期望 %s（消息：%s）", e.Code, code, e.Msg)
	}
	if !strings.Contains(e.Msg, contains) {
		t.Errorf("消息 %q 不含 %q", e.Msg, contains)
	}
	if e.Pos.Line == 0 {
		t.Error("错误缺少行号")
	}
}
