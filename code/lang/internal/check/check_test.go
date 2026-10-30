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
	res := checkSrc(t, `entry f() -> Unit
  invariant "无"
{
  nope()

}`)
	expectOne(t, res, "E1", "未定义函数")
}

func TestWrongArgCount(t *testing.T) {
	res := checkSrc(t, `fn g(a: u64) -> Unit {}

entry f() -> Unit
  invariant "无"
{
  g()

}`)
	expectOne(t, res, "E1", "需要 1 个参数")
}

func TestMissingField(t *testing.T) {
	res := checkSrc(t, `type R { a: u64, b: u64 }

entry f() -> Unit
  invariant "无"
{
  let r = R{a: 1}

}`)
	expectOne(t, res, "E1", "缺少字段 b")
}

func TestMatchNotExhaustive(t *testing.T) {
	res := checkSrc(t, `type S = A | B

entry f(x: S) -> Unit
  invariant "无"
{
  match x {
    A => Unit
  }

}`)
	expectOne(t, res, "E1", "未穷尽")
}

func TestPropagateWithoutUnionReturn(t *testing.T) {
	res := checkSrc(t, errPrelude+`
entry f(fs: BlobFs) -> bytes
  invariant "无"
{
  let b = fs.read("r", "k")!
  return b
}`)
	expectOne(t, res, "E1", "不是联合")
}

func TestPropagateWithMissingError(t *testing.T) {
	res := checkSrc(t, errPrelude+`
type Other = Oops

entry f(fs: BlobFs) -> bytes | Other
  invariant "无"
{
  let b = fs.read("r", "k")!
  return b
}`)
	expectOne(t, res, "E1", "缺少错误")
}

func TestDroppedError(t *testing.T) {
	res := checkSrc(t, errPrelude+`
entry f(fs: BlobFs) -> Unit
  invariant "无"
{
  fs.read("r", "k")

}`)
	expectOne(t, res, "E1", "被静默丢弃")
}

func TestEntryWithoutIntent(t *testing.T) {
	res := checkSrc(t, `entry f() -> Unit {

}`)
	expectOne(t, res, "E11", "缺少 example 或 invariant")
}

// 返回 Unit 的函数不写 return：`return Unit` 是零信息的重复书写。
func TestReturnUnitRejected(t *testing.T) {
	res := checkSrc(t, `entry f() -> Unit
  invariant "无"
{
  return Unit
}`)
	expectOne(t, res, "E1", "`return Unit` 是多余的")
}

// Unit 函数不写 return 是合法的（hello.dlt 就是这么写的）。
func TestUnitWithoutReturnAccepted(t *testing.T) {
	res := checkSrc(t, `entry f() -> Unit
  invariant "无"
{
}`)
	if len(res.Errors) != 0 {
		t.Fatalf("Unit 函数不写 return 应当通过，得到 %v", res.Errors)
	}
}

// 非 Unit 函数里写 `return Unit` 仍然是类型错误，不是"多余"。
func TestReturnUnitInNonUnitFunction(t *testing.T) {
	res := checkSrc(t, `entry f() -> u64
  invariant "无"
{
  return Unit
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

entry f(sys: Sys) -> Unit
  invariant "无"
{
  sys.nope(1)

}`)
	expectOne(t, res, "E2", "运行时不提供能力 Sys.nope")
}

func TestHostCapabilityArity(t *testing.T) {
	// 能力名对、参数个数不对：声明了 2 个，运行时原型是 1 个。
	res := checkSrc(t, `host Sys { fn log(str, str) -> Unit }

entry f(sys: Sys) -> Unit
  invariant "无"
{
  sys.log("a", "b")

}`)
	expectOne(t, res, "E2", "需要 1 个参数，声明了 2")
}

func TestHostCapabilityParamType(t *testing.T) {
	// 声明谎报参数类型：以前 check 返回 ok，要等 cc 报 incompatible type。
	res := checkSrc(t, `host Sys { fn log(u64) -> Unit }

entry f(sys: Sys) -> Unit
  invariant "无"
{
  sys.log(1)

}`)
	expectOne(t, res, "E2", "Sys.log 的第 1 个参数运行时是 str，声明为 u64")
}

func TestHostCapabilityRetType(t *testing.T) {
	res := checkSrc(t, `host Sys { fn now_unix() -> str }

entry f(sys: Sys) -> Unit
  invariant "无"
{
  let t = sys.now_unix()

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

entry f(fs: BlobFs) -> Unit
  invariant "无"
{

}`)
	expectOne(t, res, "E2", "BlobFs.read 的第 1 个参数不能是 ETag")
}

// 错误变体也能带载荷（以前 checker 直接拒）。
func TestVariantWithPayloadAccepted(t *testing.T) {
	res := checkSrc(t, `type Bad = Err { code: u64 }

entry f() -> u64 | Bad
  invariant "无"
{
  return Bad.Err { code: 1 }
}`)
	if len(res.Errors) != 0 {
		t.Fatalf("错误变体带字段构造应当通过，得到 %v", res.Errors)
	}
}

// 变体构造：`T.V` 零字段、`T.V { … }` 带载荷，都要通过。
func TestVariantConstructAccepted(t *testing.T) {
	res := checkSrc(t, `type Shape = Circle | Square { side: u64 }

entry f(n: u64) -> Shape
  invariant "宽为 0 时回退成圆"
{
  if n == 0 {
    return Shape.Circle
  }
  return Shape.Square { side: n }
}`)
	if len(res.Errors) != 0 {
		t.Fatalf("变体构造应当通过，得到 %v", res.Errors)
	}
}

func TestVariantWithoutPayload(t *testing.T) {
	// 带字段的变体裸写：告诉你怎么写，而不是只说类型不对。
	res := checkSrc(t, `type Shape = Circle | Square { side: u64 }

entry f() -> Shape
  invariant "无"
{
  return Shape.Square
}`)
	expectOne(t, res, "E1", "要写成 `Shape.Square { … }`")
}

func TestVariantUnknown(t *testing.T) {
	res := checkSrc(t, `type Shape = Circle

entry f() -> Shape
  invariant "无"
{
  return Shape.Triangle
}`)
	expectOne(t, res, "E1", "Shape 没有变体 Triangle")
}

func TestVariantPayloadType(t *testing.T) {
	res := checkSrc(t, `type Shape = Circle | Square { side: u64 }

entry f() -> Shape
  invariant "无"
{
  return Shape.Square { side: "x" }
}`)
	expectOne(t, res, "E1", "Shape.Square.side 应为 u64")
}

// tag 按变体名全局分配，同名变体跨 sum 会让 tag 撞车。
func TestVariantNameGloballyUnique(t *testing.T) {
	res := checkSrc(t, `type A = X | XOnly { a: u64 }
type B = X | BOnly { b: u64 }

entry f() -> A
  invariant "无"
{
  return A.X
}`)
	expectOne(t, res, "E1", "变体名 X 已被 A.X 使用")
}

// sum 值取不出字段：要用 match 绑定。
func TestSumFieldAccessRejected(t *testing.T) {
	res := checkSrc(t, `type Shape = Circle | Square { side: u64 }

entry f() -> u64
  invariant "无"
{
  let s = Shape.Circle
  return s.side
}`)
	expectOne(t, res, "E1", "用 match 绑定变体字段")
}

func TestNoCascade(t *testing.T) {
	// 两个独立错误应报 2 条，而不是因级联膨胀。
	res := checkSrc(t, `type R { a: u64, b: u64 }

entry f() -> Unit
  invariant "无"
{
  let r = R{a: 1}
  let s = R{a: "文本"}

}`)
	if len(res.Errors) < 2 {
		t.Fatalf("应至少报 2 条错误，实际 %d 条：%v", len(res.Errors), res.Errors)
	}
	if len(res.Errors) > 4 {
		t.Fatalf("错误数 %d 过多，疑似级联：%v", len(res.Errors), res.Errors)
	}
}

// ---------- env：声明完整性与服务形态接线（D1）----------

// fullEnv 是补齐了运行时五键的声明，多条用例共用它。
const fullEnv = `env App {
  port: u64 = 8787,
  bind: str = "0.0.0.0",
  root: str,
  token: str = "",
  max_body: u64 = 8388608
}

`

const svcTypes = `type ApiError = NotFound

type Request { method: str }

`

func TestEnvAccepted(t *testing.T) {
	// 正例：补齐五键 + 入口拿 cfg，零错误，且配置面进了 Result.Env 供生成器与契约用。
	res := checkSrc(t, fullEnv+svcTypes+`entry handle(req: Request, cfg: App) -> bytes | ApiError
  invariant "一律 405"
{
  return b""
}`)
	if len(res.Errors) != 0 {
		t.Fatalf("期望 0 个错误，实际：%v", res.Errors)
	}
	if res.Env == nil || len(res.Env.Keys) != 5 || res.Env.Prefix != "APP" {
		t.Fatalf("Result.Env 不完整：%+v", res.Env)
	}
}

func TestEnvOnCliEntryRejected(t *testing.T) {
	// 命令行程序的配置走 argv：一个没人读的 env 块就是死声明。
	res := checkSrc(t, fullEnv+`entry f() -> Unit
  invariant "无"
{

}`)
	expectOne(t, res, "E1", "无人消费")
}

func TestServiceRequiresEnv(t *testing.T) {
	// 以前服务可以从内置默认值起服务（token 缺失 = 不鉴权），现在不行。
	res := checkSrc(t, svcTypes+`entry handle(req: Request) -> bytes | ApiError
  invariant "一律 405"
{
  return b""
}`)
	expectOne(t, res, "E1", "服务必须声明 env 块")
}

func TestEnvMissingRuntimeKey(t *testing.T) {
	res := checkSrc(t, `env App {
  port: u64 = 8787,
  bind: str = "0.0.0.0",
  token: str,
  max_body: u64 = 1024
}

`+svcTypes+`entry handle(req: Request, cfg: App) -> bytes | ApiError
  invariant "一律 405"
{
  return b""
}`)
	expectOne(t, res, "E1", "缺配置键 root")
}

func TestEnvBadKeyType(t *testing.T) {
	res := checkSrc(t, `env App {
  port: u64 = 8787,
  bind: str = "0.0.0.0",
  root: str,
  token: str = "",
  max_body: u64 = 1024,
  banner: bytes
}

`+svcTypes+`entry handle(req: Request, cfg: App) -> bytes | ApiError
  invariant "一律 405"
{
  return b""
}`)
	expectOne(t, res, "E1", "只接受 u64 与 str")
}

func TestEnvDefaultShapeMismatch(t *testing.T) {
	res := checkSrc(t, `env App {
  port: u64 = "8787",
  bind: str = "0.0.0.0",
  root: str,
  token: str = "",
  max_body: u64 = 1024
}

`+svcTypes+`entry handle(req: Request, cfg: App) -> bytes | ApiError
  invariant "一律 405"
{
  return b""
}`)
	expectOne(t, res, "E1", "默认值却是 字符串字面量")
}

func TestEnvRuntimeKeyType(t *testing.T) {
	// 端口写成 str：生成代码要把它填进 dp_cfg.port，类型不对就不接。
	res := checkSrc(t, `env App {
  port: str = "8787",
  bind: str = "0.0.0.0",
  root: str,
  token: str = "",
  max_body: u64 = 1024
}

`+svcTypes+`entry handle(req: Request, cfg: App) -> bytes | ApiError
  invariant "一律 405"
{
  return b""
}`)
	expectOne(t, res, "E1", "配置键 port 必须是 u64")
}

func TestEnvNotConstructible(t *testing.T) {
	// 配置的值来自启动环境：源码里能写 App{…} 就等于把部署事实硬编码进二进制。
	res := checkSrc(t, fullEnv+svcTypes+`entry handle(req: Request) -> bytes | ApiError
  invariant "一律 405"
{
  return b""
}

fn mk() -> App
  invariant "无"
{
  return App{port: 1, bind: "x", root: "/tmp", token: "", max_body: 2}
}`)
	expectOne(t, res, "E1", "不能在源码里构造")
}

func TestEnvTwoBlocksRejected(t *testing.T) {
	res := checkSrc(t, fullEnv+`env Other {
  port: u64 = 1
}

`+svcTypes+`entry handle(req: Request, cfg: App) -> bytes | ApiError
  invariant "一律 405"
{
  return b""
}`)
	expectOne(t, res, "E1", "env 块只能有一个")
}

func TestServiceUnboundParamRejected(t *testing.T) {
	// 锁住 v0.11 消灭的那条位置魔法：以前 entry 里第一个 str 参数会被默默当 root。
	res := checkSrc(t, fullEnv+svcTypes+`entry handle(req: Request, cfg: App, root: str) -> bytes | ApiError
  invariant "一律 405"
{
  return b""
}`)
	expectOne(t, res, "E1", "无法绑定")
}

// ---------- D2：成功即值（R1 / R3）----------

func TestServiceSuccessMustBeValue(t *testing.T) {
	// R1：服务入口的成功类型只能是 bytes / Unit。Response 记录不再是「可选写法」，
	// 而是 check 期错误；提示要说清它在语言里等于什么。
	res := checkSrc(t, fullEnv+`type ApiError = NotFound

type Request { method: str }

type Response {
  status: u64,
  body: bytes
}

entry handle(req: Request, cfg: App) -> Response | ApiError
  invariant "一律 405"
{
  return Response{status: 405, body: b""}
}`)
	if len(res.Errors) != 1 {
		t.Fatalf("期望 1 条错误，实际 %d：%v", len(res.Errors), res.Errors)
	}
	msg := res.Errors[0].Msg
	for _, want := range []string{"成功类型必须是 bytes 或 Unit", "在本语言里就是"} {
		if !strings.Contains(msg, want) {
			t.Errorf("消息 %q 不含 %q", msg, want)
		}
	}
}

func TestServiceResultMustBeUnion(t *testing.T) {
	// 没有错误项的入口无法表达失败：错误必须显式，不能靠运行时兜底。
	res := checkSrc(t, fullEnv+`type Request { method: str }

entry handle(req: Request, cfg: App) -> bytes
  invariant "一律 405"
{
  return b"ok"
}`)
	expectOne(t, res, "E1", "必须返回 `bytes | 错误`")
}

func TestBareReturnUnitValues(t *testing.T) {
	// R3：Unit 与 bytes 的单位值都可以裸 `return`（后者是空 body）。
	res := checkSrc(t, `fn a() -> Unit
{
  return
}

fn b() -> bytes
{
  return
}`)
	if len(res.Errors) != 0 {
		t.Fatalf("Unit 与 bytes 的裸 return 应合法，得到：%v", res.Errors)
	}
}

func TestBareReturnOtherTypesRejected(t *testing.T) {
	res := checkSrc(t, `fn c() -> str
{
  return
}`)
	expectOne(t, res, "E1", "不能空 return")
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

// TestTwoEntriesRejected 一个程序只能有一个入口。
//
// 以前多入口会静默取 map 迭代顺序里的任意一个当 EntryPoint —— 生成哪个 main
// 由随机数决定。拆平之后入口是普通函数，多写一个 entry 当场硬错。
func TestTwoEntriesRejected(t *testing.T) {
	res := checkSrc(t, `entry a() -> Unit
  invariant "x"
{
}

entry b() -> Unit
  invariant "y"
{
}`)
	expectOne(t, res, "E1", "入口只能有一个：a 与 b 都标了 entry")
}
