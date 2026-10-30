package check

import (
	"strings"
	"testing"
)

// ---------- D3：路由表即契约（boundary 方法尾部的 @verb / impl / precondition）----------

func TestImplExtraParamUnbound(t *testing.T) {
	// 锁住 v0.11 消灭的那条位置魔法：boundary 参数之后只接 host 能力与 env 类型，
	// 其余形状没有绑定规则 —— 静默给零值就是「起得来、全读空目录」。
	res := checkSrc(t, fullEnv+`type ApiError = NotFound

boundary Api @v1 {
  get(key: str) -> bytes @get("/v1/x") impl h with idempotency
    invariant "x"
}

fn h(key: str, root: str) -> bytes | ApiError {
  return b""
}`)
	expectOne(t, res, "E1", "无法绑定")
}

func TestBoundaryParamMustBeSlot(t *testing.T) {
	// 只有 key 与 body 能从请求里取到值：写 id 之类的名字没有绑定规则，
	// 与其静默给零值，不如在声明处报错并说清可选面。
	res := checkSrc(t, fullEnv+`type ApiError = NotFound

boundary Api @v1 {
  get(id: str) -> bytes @get("/v1/x") impl h with idempotency
    invariant "x"
}

fn h(id: str) -> bytes | ApiError {
  return b""
}`)
	expectOne(t, res, "E1", "只接受 key 与 body")
}

func TestWriteRouteNeedsPrecondition(t *testing.T) {
	// 合流的前提：把「并发保护」从运行时搬到生成代码之后，"忘了声明"必须是编译错误。
	res := checkSrc(t, fullEnv+`type ApiError = NotFound

boundary Api @v1 {
  put(key: str, body: bytes) -> Unit @put("/v1/x") impl h with idempotency
    invariant "x"
}

fn h(key: str, body: bytes) -> Unit | ApiError {
  return
}`)
	expectOne(t, res, "E1", "必须显式表态")
}

func TestWithoutPreconditionReasonAccepted(t *testing.T) {
	// 显式写理由就放行：与 ignore(reason) 同构 —— 不可沉默，但可以留痕。
	res := checkSrc(t, fullEnv+`type ApiError = NotFound

boundary Api @v1 {
  create(key: str) -> Unit @post("/v1/x") impl h with idempotency
    without precondition: "新建 key 没有既有版本可比"
    invariant "x"
}

fn h(key: str) -> Unit | ApiError {
  return
}`)
	if len(res.Errors) != 0 {
		t.Fatalf("期望 0 个错误，实际：%v", res.Errors)
	}
}

func TestPreconditionOnReadRejected(t *testing.T) {
	res := checkSrc(t, fullEnv+`type ApiError = NotFound

boundary Api @v1 {
  get(key: str) -> bytes @get("/v1/x") impl h with idempotency, precondition
    invariant "x"
}

fn h(key: str) -> bytes | ApiError {
  return b""
}`)
	expectOne(t, res, "E1", "只有写路由能声明 precondition")
}

func TestRouteVerbUnknown(t *testing.T) {
	res := checkSrc(t, fullEnv+`type ApiError = NotFound

boundary Api @v1 {
  fetch(key: str) -> bytes @fetch("/v1/x") impl h with idempotency
    invariant "x"
}

fn h(key: str) -> bytes | ApiError {
  return b""
}`)
	expectOne(t, res, "E1", "不认识")
}

func TestDuplicateRouteRejected(t *testing.T) {
	res := checkSrc(t, fullEnv+`type ApiError = NotFound

boundary Api @v1 {
  a(key: str) -> bytes @get("/v1/x") impl h with idempotency
    invariant "x"

  b(key: str) -> bytes @get("/v1/x") impl h with idempotency
    invariant "x"
}

fn h(key: str) -> bytes | ApiError {
  return b""
}`)
	expectOne(t, res, "E1", "重复声明")
}

func TestRouteWithoutImplRejected(t *testing.T) {
	res := checkSrc(t, fullEnv+`boundary Api @v1 {
  get(key: str) -> bytes @get("/v1/x")
    invariant "x"
}`)
	expectOne(t, res, "E1", "却没有实现")
}

func TestImplWithoutRouteRejected(t *testing.T) {
	res := checkSrc(t, fullEnv+`type ApiError = NotFound

boundary Api @v1 {
  get(key: str) -> bytes impl h with idempotency
    invariant "x"
}

fn h(key: str) -> bytes | ApiError {
  return b""
}`)
	expectOne(t, res, "E1", "没有路由")
}

func TestEntryWithRoutesRejected(t *testing.T) {
	// 服务形态的入口是路由表：再留一个 entry 就等于「路由由谁定义」又有两处。
	res := checkSrc(t, fullEnv+svcTypes+`entry handle(key: str) -> bytes | ApiError
  invariant "x"
{
  return b""
}`)
	expectOne(t, res, "E1", "不能并存")
}

func TestImplReturnMismatch(t *testing.T) {
	// 方法声明 bytes，实现返回 Unit：这条路由的响应形状就对不上。
	res := checkSrc(t, fullEnv+`type ApiError = NotFound

boundary Api @v1 {
  get(key: str) -> bytes @get("/v1/x") impl h with idempotency
    invariant "x"
}

fn h(key: str) -> Unit | ApiError {
  return
}`)
	expectOne(t, res, "E1", "不一致")
}

// ---------- D2：成功即值（R1 / R3）----------

func TestRouteSuccessMustBeValue(t *testing.T) {
	// R1：路由的成功类型只能是 bytes / Unit。Response 记录不再是「可选写法」，
	// 而是 check 期错误；提示要说清它在语言里等于什么。
	res := checkSrc(t, fullEnv+`type ApiError = NotFound

type Response {
  status: u64,
  body: bytes
}

boundary Api @v1 {
  get(key: str) -> Response @get("/v1/x") impl h with idempotency
    invariant "x"
}

fn h(key: str) -> bytes | ApiError {
  return b""
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

func TestImplResultMustBeUnion(t *testing.T) {
	// 没有错误项的实现无法表达失败：错误必须显式，不能靠运行时兜底。
	res := checkSrc(t, fullEnv+`type ApiError = NotFound

boundary Api @v1 {
  get(key: str) -> bytes @get("/v1/x") impl h with idempotency
    invariant "x"
}

fn h(key: str) -> bytes {
  return b"ok"
}`)
	expectOne(t, res, "E1", "必须返回 `bytes | 错误`")
}

// ---------- B3：成功码投影（@created / @no_content）----------

func TestCreatedOnWriteAccepted(t *testing.T) {
	// 成功码是声明的投影：写 @created，路由表里就是 201，源码里没有数字。
	res := checkSrc(t, fullEnv+`type ApiError = Conflict

boundary Api @v1 {
  create(key: str) -> Unit @post("/v1/x") @created impl h with idempotency
    without precondition: "创建没有既有版本可比"
    invariant "x"
}

fn h(key: str, cfg: App) -> Unit | ApiError {
  return
}`)
	if len(res.Errors) != 0 {
		t.Fatalf("期望 0 个错误，实际：%v", res.Errors)
	}
	if len(res.Routes) != 1 || res.Routes[0].Status != 201 {
		t.Fatalf("路由表里的成功码应是 201，得到 %+v", res.Routes)
	}
}

func TestNoContentNeedsUnit(t *testing.T) {
	// 204 的定义是「没有表示」：有 body 的声明投不出这个码。
	res := checkSrc(t, fullEnv+`type ApiError = NotFound

boundary Api @v1 {
  put(key: str, body: bytes) -> bytes @put("/v1/x") @no_content impl h with idempotency, precondition
    invariant "x"
}

fn h(key: str, body: bytes, cfg: App) -> bytes | ApiError {
  return b""
}`)
	expectOne(t, res, "E1", "204 不能有 body")
}

func TestStatusOnReadRejected(t *testing.T) {
	// 读路由成功恒 200：@created / @no_content 都是写给"改变了什么"的。
	res := checkSrc(t, fullEnv+`type ApiError = NotFound

boundary Api @v1 {
  get(key: str) -> bytes @get("/v1/x") @created impl h with idempotency
    invariant "x"
}

fn h(key: str, cfg: App) -> bytes | ApiError {
  return b""
}`)
	expectOne(t, res, "E1", "只有写路由能投影成功码")
}

func TestDefaultSuccessIs200(t *testing.T) {
	// 没写注解就是 200：不新增"必须表态成功码"的门禁，沉默在这里有唯一自然值。
	res := checkSrc(t, fullEnv+`type ApiError = NotFound

boundary Api @v1 {
  get(key: str) -> bytes @get("/v1/x") impl h with idempotency
    invariant "x"
}

fn h(key: str, cfg: App) -> bytes | ApiError {
  return b""
}`)
	if len(res.Errors) != 0 {
		t.Fatalf("期望 0 个错误，实际：%v", res.Errors)
	}
	if len(res.Routes) != 1 || res.Routes[0].Status != 200 {
		t.Fatalf("默认成功码应是 200，得到 %+v", res.Routes)
	}
}
