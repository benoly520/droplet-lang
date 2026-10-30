# Droplet 实现计划（D0 子集 → C 后端 → 示例 Web API 服务）

> 状态：v0.2 / 2026-10-30 / 上位文档 `docs/SPEC.md` v0.7
> 目标：用最小语言子集跑通「`.dlt` → C 二进制 → 真实 HTTP 服务」闭环。
> 首个真实服务是一个**通用哑存储 Web API**：字节级 CRUD + 版本头 + 可插拔的版本化钩子。
>
> 本文档只描述 Droplet 与它的示例服务，不绑定任何具体消费方。任何需要
> 「按 key 存取一份不透明字节」的前端，都可以直接对接这份契约。

## 1. 范围

### 1.1 要做成的一件事

```
code/lang      编译器（选型见 §7.1）+ C 运行时标准库
code/webapi    用 .dlt 写的示例服务（boundary / entry / 路由）
```

闭环验收：

```sh
./droplet build code/webapi/src/contract.dlt code/webapi/src/main.dlt -o blobd
mkdir -p /tmp/blobd            # APP_ROOT 必须已存在，否则服务启动即退出
APP_TOKEN=dev APP_ROOT=/tmp/blobd ./blobd
curl -H 'Authorization: Bearer dev' 'localhost:8787/v1/blob/demo'
```

### 1.2 明确不做（D0 范围外）

| 不做 | 原因 | 何时补 |
| --- | --- | --- |
| 解析 payload 的结构 | 哑存储：body 原样透传，服务端不认识业务字段 | 永不需要（结构化服务另立项） |
| 仿射类型 + 区域推断 | 最大误报源，先把闭环跑通 | M3 之后，先只做「句柄未消费」一条 |
| 效果集检查 | 语法接受但不校验 | M3 |
| 并发（task/chan） | 单实例自用，串行足够 | 压测暴露后再议 |
| E10 变异检验 / E12 收敛判据 | 研究项，依赖候选集基础设施 | 长期 |
| `boundary` 生成路由 | 第一版路由手写，boundary 只导出契约 JSON | M3 |
| V1/V2 对照实验（状态机 / AST diff） | SPEC 附录 C 的待验项，与实现解耦 | 独立立项 |

> 这份「不做」清单是对 SPEC 的**显式偏差登记**，不是遗漏。更完整的偏差（含运行时、
> 工具链的缺口、原因、补齐时机与可执行复核命令）在 [`docs/D0-DEVIATIONS.md`](D0-DEVIATIONS.md)。

## 2. 验收契约：哑存储 Web API

服务端是哑存储：它不理解 payload 内部的任何字段，只做字节读写与元数据。

| 方法 | 路径 | 成功 | 失败 |
| --- | --- | --- | --- |
| GET | `/v1/blob/<key>` | 200 + 字节 + `ETag`（运行时从 body 推导） | 404 不存在 / 400 非法 key |
| PUT | `/v1/blob/<key>` | 200 + 空 body（**不带 `ETag`**，新版本号请 `GET`） | 412 版本不符 / 413 超限 / 400 |
| GET | `/v1/keys` | 200 + `["k1","k2"]` | 500 |
| POST | `/v1/keys/<key>` | 200 + 空 body | 400 非法 key / 500 |
| GET | `/health` | 200 + `{"ok":true}` | — |
| 其它 | 任意 | — | 405（handler 兜底） |

约束：

- 所有 `/v1/*` 需 `Authorization: Bearer <APP_TOKEN>`；`/health` 不校验，供探活用；
- `key` 只允许 `[a-z0-9_-]{1,32}`，其余 400（**这是安全边界**：防目录穿越，
  `../evil`、空串、超长、含 `/` 全部拒绝）；
- 路由解析规则：`path` 为去掉 query 的路径。`/v1/<group>/<key>` 按**第三个斜杠**拆，
  前半截归一到 `path`（`/v1/blob`），后半截作 `key`；`/v1/keys` 与 `/health` 无 `key`；
- key 校验与 `If-Match` 校验都在**进 handler 之前**由运行时做完（见 §5.4），handler 只看得到
  已经合法的 `key`、以及已经通过版本检查的写请求；
- `PUT` 顺序固定：**快照 → 原子写 → 调度版本化提交**，提交在响应发出后执行；
- `ETag` 不由 handler 书写（v0.10）：运行时按 `dp_sys_u64_str(dp_sys_hash64(body))` 从响应 body 推导，
  空 body 没有表示就不发这个头；`If-Match` 的比对走同一个函数（当前内容的 hash），所以 `GET` 拿到的
  tag 可以直接用于下一次 `PUT`，这条链与推导改造之前逐字节相同；
- `PUT` body 上限 8 MiB（`APP_MAX_BODY` 可调），header 总量上限 8 KiB，单行 4 KiB，
  读写超时各 30 s（超时回 408）；拒绝 `Transfer-Encoding`（防请求走私）；
- 每请求一个 arena，请求结束即 `dp_arena_free`。

## 3. D0 语言子集

### 3.1 设计取舍

D0 只实现「写这个示例服务所需的最小集合」。判据：**任何无法被 `code/webapi/src/*.dlt`
用到的语法，不进 D0。**

- 类型：`str` `bytes` `u64` `i64` `bool` `Unit` + record + sum + 联合返回；
- 无泛型、无闭包、无反序列化、无模块、无 import；编译单元 = 命令行给出的全部
  `.dlt` 文件，按顺序合并为一个程序（共享命名空间，重复定义报 E1）；
- 错误：`!` 传播 + `match` 穷尽，错误值不可丢弃（SPEC §5.6 的 C1 核心，必须做）；
- 内存：全部走 arena，`str`/`bytes` 非 NUL 结尾，带长度。

### 3.2 词法

```
IDENT      := [A-Za-z_][A-Za-z0-9_]*
INT        := [0-9]+
STRING     := "..."           支持 \" \\ \n \t
BYTES      := b"..."          字节字面量，支持 \xNN
COMMENT    := // ...          行注释
关键符号    := { } ( ) [ ] , : ; -> => = | ! . @ < >
```

固定关键字（10 个）：`type` `fn` `entry` `host` `env` `boundary` `let` `return` `match` `if`

上下文关键字（不占固定关键字预算，只在特定位置成立）：`else` `with` `idempotency`
`requires` `invariant` `example` `effects` `cap` `by`

> 偏差登记：SPEC §14.2 的 14 个关键字里没有 `else`；D0 为可写 `if/else` 暂按上下文关键字
> 处理，M3 复评（替代方案是用 `match bool` 取代 `if`）。

**关键字一律不可用作标识符**（含上下文关键字）。因此本计划的 host 能力名用 `getenv` 而非
`env`——`env` 是固定关键字，`fn env(...)` 会被词法层识别为 env 块声明的开头并报 E0006。

### 3.3 语法（EBNF，实现以本节为准）

```ebnf
program      := item*
item         := record_type | sum_type | fn_decl | host_decl | env_decl | boundary_decl

record_type  := 'type' IDENT '{' field (',' field)* ','? '}'
sum_type     := 'type' IDENT '=' variant ('|' variant)*
field        := IDENT ':' type
variant      := IDENT ('{' field (',' field)* '}')?

fn_decl      := entry_kw IDENT '(' params? ')' '->' type intent* block
entry_kw     := 'fn' | 'entry'      // 'entry' 前缀即入口，一个程序最多一个（多了报 E1）
                                   // D1 拆平了 `entry Name { fn … }` 那层壳（H7）：
                                   // 入口与普通函数同形，少一个 AST 节点
params       := param (',' param)*
param        := IDENT ':' type      // 普通函数与 boundary 方法：参数名要进函数体 / 契约 JSON
intent       := 'requires' expr
              | 'invariant' STRING
              | 'example' expr
              | 'effects' '{' IDENT (',' IDENT)* '}'
              | 'cap' 'mem' '<' INT IDENT '>'
              | 'idempotent' 'by' IDENT

host_decl    := 'host' IDENT '{' host_fn+ '}'
host_fn      := 'fn' IDENT '(' host_params? ')' '->' type  // 无函数体，实现由运行时提供
host_params  := type (',' type)*                  // 能力签名不写参数名（v0.7）：权威是运行时原型
env_decl      := 'env' IDENT '{' env_field+ '}'     // 只到语法层，D0 不实现（§5.4）
env_field     := IDENT ':' type ('=' literal)?
boundary_decl := 'boundary' IDENT '@' 'v' INT '{' bnd_method+ '}'
bnd_method    := IDENT '(' params? ')' '->' type ('with' 'idempotency')? intent*


block        := '{' stmt* '}'
stmt         := let_stmt | return_stmt | if_stmt | match_stmt | expr_stmt
let_stmt     := 'let' IDENT (':' type)? '=' expr
return_stmt  := 'return' expr?
if_stmt      := 'if' expr block ('else' (block | if_stmt))?
match_stmt   := 'match' expr '{' arm (',' arm)* ','? '}'
arm          := pattern '=>' (block | expr)
pattern      := IDENT ('{' IDENT (',' IDENT)* '}')? | '_'
expr_stmt    := expr

expr         := unary ( binop unary )*
unary        := primary '!'*
primary      := INT | STRING | BYTES | 'true' | 'false' | 'Unit'
              | IDENT
              | IDENT '{' (IDENT ':' expr) (',' IDENT ':' expr)* '}'                      // record 构造
              | IDENT '.' IDENT                                                          // 字段访问，或零字段变体 `T.V`
              | IDENT '.' IDENT '{' (IDENT ':' expr) (',' IDENT ':' expr)* '}'            // 变体构造 `T.V { … }`
              | IDENT '(' (expr (',' expr)*)? ')'                                         // 调用
              | '(' expr ')'
binop        := '==' | '!=' | '<' | '<=' | '>' | '>=' | '&&' | '||'
              | '+' | '-' | '*' | '/' | '%'

type         := 'str' | 'bytes' | 'u64' | 'i64' | 'bool' | 'Unit'
              | IDENT
              | type '|' type
```

约定：

- 无运算符优先级：`+ - * / %` 同级，**必须显式括号**（SPEC §5.1 要求「无优先级表」）；
- 无隐式转换；`str` 与 `bytes` 不互通；
- record 与 sum 用 `=` 区分（不引入 `record`/`sum` 关键字）。

### 3.4 类型与检查规则

| 规则 | 错误码 | 说明 |
| --- | --- | --- |
| 未定义类型 / 未定义函数 / 参数个数不符 | E1 | 报在真实位置，不级联 |
| record 字段缺失 / 多余 / 类型不符 | E1 | 构造点报错 |
| `match` 非穷尽 | E1 | 列出缺失变体 |
| `!` 所在函数的返回联合不包含该错误 | E1 | 提示应补哪个变体 |
| 错误值被丢弃（调用了带错误联合的函数却不用 `!`、`match` 或 `ignore`） | E1 | C1 的核心，D0 必做 |
| `host` 能力裸调（`log(x)` 而非 `sys.log(x)`） | E1 | 能力必须经 host 参数访问，保证能力来源可静态枚举 |
| 跨 `boundary` 使用非序列化类型 | E3 | D0 只有基础类型，恒通过 |
| `entry` / `boundary` 方法无 `example` 也无 `invariant` | E11 | 硬门禁 |
| 多个 `entry` | E1 | 一个程序只能有一个入口；D1 之前会静默取 map 迭代顺序里的任意一个当 EntryPoint，生成哪个 `main` 由随机数决定 |
| 服务形态的入口成功类型不是 `bytes` / `Unit` | E1 | v0.12（R1）：状态码不是能构造的值；写 `Response{status: 200, body: X}` 时报这条并提示「它就是 `X`」 |
| 裸 `return` 用在返回类型不是 `Unit` / `bytes`（或其 `| 错误` 联合）的函数上 | E1 | v0.12（R3）：裸 `return` 是**该返回类型的单位值**，不是零值构造 |
| 词法 / 语法错误 | E0 | 带行号列号 |

**D0 不检查**：效果集内容、`cap` 上界、`idempotent` 一致性、`requires` 静态可判性（只解析）。
这些位置在生成的 `contract.json` 里保留，供 M3 启用。

### 3.5 tag 编号约定（决定 `!` 的实现）

全局错误 tag 从 100 起顺序分配，成功 tag 固定 0：

```
0        成功值
1..99    record / sum 的普通变体（按声明顺序）
100+     出现在返回类型 union 非首项的错误变体
         （NotFound=100 … Conflict=103、NotAllowed=104、PreconditionFailed=105）
```

规则：**同一个变体名在所有联合里共享同一 tag**。因此 `!` 只需复制 tag，不做映射表。
反过来这也是一条硬规则：**变体名跨整个程序必须唯一** —— 同名会让两个 sum 拿到同一个 tag，
`match` 就分不开它们，所以 `check` 在声明处就报 `E1: 变体名 X 已被 A.X 使用`（D1 起）。

**v0.12（D2）改了「谁算错误」的口径**：以前只有用过 `!` 的变体才标 100+，于是
`return ApiError.NotAllowed` 这种直接返回变体的写法会被当成普通变体（tag 1..）而**静默变 500**。
现在按签名结构认：任何编码为 union 的返回类型，其**非首项一律是错误**（`check.markErrorTypes`，
函数与 host 方法都算）。`rt.h` 的 `DP_ERR_*` 与声明顺序由 `TestErrTagsMatchRuntime` 钉死。

## 4. 样例（必须全绿）

> §4.2 / §4.3 是 **D0 落地时的快照**，没有随后续里程碑重写：D1 把 `root: str` 参数换成 `cfg: App`
> （SPEC §5.7），D2 删掉 `Response`、把入口返回类型改成 `bytes | ApiError`（SPEC §5.1.2），
> D3 删掉 `Request`、把路由搬进 `boundary`（SPEC §5.1.3）。
> **当前样例以 `code/webapi/src/*.dlt` 为准**（95 行 = `contract.dlt` 64 + `main.dlt` 31，D3 起路由与实现分开写），
> 本节只作为管线说明的对照保留。

### 4.1 `code/lang/examples/hello.dlt`

```droplet
// 最小闭环：验证 lexer + parser + checker + codegen + cc 五层都能跑通。
// 判据：droplet build examples/hello.dlt && ./hello 打印 world。
host Sys {
  fn log(str) -> Unit
}

entry greet(sys: Sys) -> Unit
  invariant "greet 向标准输出写一行 world"
{
  sys.log("world")
}
```

### 4.2 `code/webapi/src/contract.dlt`

```droplet
// 通用哑存储 Web API 契约：服务端只做字节读写，不解构 payload。
type ETag {
  hash: u64
}

type ApiError = NotFound | BadRequest | IoError | Conflict

type Request {
  method: str,
  path: str,
  key: str,
  body: bytes,
  if_match: str
}

// 响应面只有 status 与 body：ETag 由运行时从 body 推导，handler 不写（SPEC §2.2 H7）。
type Response {
  status: u64,
  body: bytes
}

host BlobFs {
  fn read(str, str) -> bytes | ApiError
  fn write(str, str, bytes) -> ETag | ApiError
  fn backup(str, str) -> Unit
  fn create(str, str) -> bool | ApiError
  fn list_json(str) -> bytes | ApiError
}

host Versioning {
  fn commit_soon(str, str) -> Unit
}

boundary BlobApi @v1 {
  get(key: str) -> bytes with idempotency
    invariant "同一 key 的 GET 字节等于最近一次成功 PUT 的字节"

  put(key: str, body: bytes) -> ETag with idempotency
    invariant "PUT 前必须存在上一版本快照"

  list() -> bytes with idempotency
    invariant "清单来自目录，不来自任何注册表文件"
}
```

### 4.3 `code/webapi/src/main.dlt`

```droplet
// 示例服务入口：把 HTTP 请求映射到哑存储能力。
entry handle(req: Request, fs: BlobFs, ver: Versioning, root: str)
    -> Response | ApiError
  invariant "非 GET/PUT 的请求一律 405"
{
  if req.path == "/health" {
    return Response{status: 200, body: b"{\"ok\":true}"}
  }

  if req.path == "/v1/keys" {
    if req.method == "GET" {
      let body = fs.list_json(root)!
      return Response{status: 200, body: body}
    }
    if req.method == "POST" {
      fs.create(root, req.key)!
      return Response{status: 200, body: b""}
    }
  }

  if req.path == "/v1/blob" {
    if req.method == "GET" {
      let body = fs.read(root, req.key)!
      return Response{status: 200, body: body}
    }
    if req.method == "PUT" {
      fs.backup(root, req.key)
      // write 返回的新版本 ETag 这里用不上：响应头里的 ETag 由运行时从 body 推导，
      // 客户端要新版本号就 GET（v0.10，SPEC §2.2 H7）。
      fs.write(root, req.key, req.body)!
      ver.commit_soon(root, req.key)
      return Response{status: 200, body: b""}
    }
  }

  return Response{status: 405, body: b""}
}
```

> `handle` 的 `!` 把 `ApiError` 抛给运行时；运行时不认识业务语义，只按 tag 映射
> 404 / 400 / 409 / 500。**这是「错误是值」在边界处的唯一转换点。**

### 4.4 `code/lang/examples/shape.dlt`（D1 追加）

```droplet
// D1 变体构造：sum 的值以前构造不出来，只能 match 零值。
// 判据：droplet build examples/shape.dlt -o .build/shape && .build/shape 打印 wide
host Sys {
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
```

判据：`droplet build examples/shape.dlt -o .build/shape && .build/shape` 打印 `wide`。
它存在的唯一理由是把变体构造钉成一个**可跑的**样例：`hello.dlt` 与 webapi 都不构造 sum 值，
所以 D1 之前那条语法缺口没有任何端到端护栏（`gen` 的两个 shape 测试是后补的）。


## 5. 编译管线

```
.dlt ──lexer──> tokens ──parser──> AST ──checker──> C ──cc──> 静态二进制
                                        └─ contract.json（boundary/env/fn 签名）
```

### 5.1 AST 形状

编译器内部的 AST 必须满足两条：**每种节点可有穷举遍历**、**每个节点带源码位置（行/列）**。
选型语言若无 sum type（如 Go），用「接口 + 标签常量 + type switch」模拟，并要求
`match` 分支在 `default` 里 `panic`（编译期穷尽由测试保证，见 §7.1）。

设计约束：**AST 不做隐式归一化**，所有检查在 checker 里显式发生，便于错误定位到 token。

### 5.2 Droplet → C 映射

| Droplet | C |
| --- | --- |
| `str` | `dp_str { const char* p; size_t n; }` |
| `bytes` | `dp_bytes { const uint8_t* p; size_t n; }` |
| `u64` / `i64` / `bool` | `uint64_t` / `int64_t` / `bool` |
| `Unit` | `typedef struct { int _; } dp_unit;` |
| record `T` | `typedef struct dp_T_<T> { ... } dp_T_<T>;`，字段名加 `m_` 前缀 |
| sum `T` | `uint16_t tag` + `union { struct { uint8_t _pad; } <Variant>; ... } u;` |
| 返回 `A \| E1 \| E2` | `dp_R_<fn> { uint16_t tag; union { A ok; dp_error err; } u; }`，`tag==0` 成功、`100+` 错误 |
| `fn f(...)` | 联合返回 `static dp_R_<f> dp_f_<f>(dp_arena* a, <params>)`；否则 `static <T> dp_f_<f>(...)` |
| `let x = e` | 局部变量 + 立即求值（无惰性） |
| `match` | `switch (v.tag)`，`_` 编译为 `default:`（checker 已保证穷尽） |
| `!` | `if (r.tag != 0) { dp_R_out o = {0}; o.tag = r.tag; o.u.err = r.u.err; return o; }` |
| `"..."` | `dp_str_lit("p")`（宏：`(dp_str){ p, sizeof-1 }`） |
| `b"..."` | `dp_bytes_lit("p")`（同上，字节侧） |

字符串比较 `==` 生成 `dp_str_eq`，**禁止** `strcmp`（非 NUL 结尾）。

### 5.3 生成代码的骨架

入口函数没有 `Request` 参数时是 CLI 形态（M2，已完成）：`main` 直接调用 `entry` 的函数，
非 Host 参数从 `argv` 按位置取。

```c
/* 编译单元：每个 .dlt 程序一个 .c，符号前缀 dp_ */
#include "rt.h"

static dp_unit dp_f_greet(dp_arena* a);

static dp_unit dp_f_greet(dp_arena* a) {
  (void)a;
  (void)(dp_sys_log(dp_str_lit("world")));
  return ((dp_unit){0});
}

int main(int argc, char** argv) {
  dp_arena* a = dp_arena_new(1u << 20);
  if (!a) { return 2; }
  dp_f_greet(a);
  dp_arena_free(a);
  return 0;
}
```

入口函数的参数里出现 `Request` 时是服务形态（M3，已完成）：写一个 `dp_serve_handle`
适配器，`main` 交给 `dp_serve`。适配器做两件事：把 `dp_request` 拆成 `Request` 记录、
把 `Response` 记录抬回 `dp_response`。

> 下面这段骨架是 **D0 快照**。v0.10 起不再抬 `etag`，v0.12（D2）起 `Response` 这个记录被删掉，
> 成功分支只剩 `out->status = 200` 与（`bytes` 时）`out->body = _r.u.ok`；判定也从生成期
> （`E-GEN`）搬到检查期（`E1`）。当前生成规则见 SPEC §5.1.2。

```c
static int dp_serve_handle(dp_arena* a, dp_str root, const dp_request* rq, dp_response* out) {
  (void)root;
  dp_T_Request v_req = {0};
  v_req.m_method = rq->method;
  v_req.m_path = rq->path;
  v_req.m_key = rq->key;
  v_req.m_body = rq->body;
  v_req.m_if_match = rq->if_match;
  dp_R_handle _r = dp_f_handle(a, v_req, root);
  if (_r.tag != 0) { return (int)_r.tag; }          /* 运行时按 tag 映射状态码 */
  out->content_type = dp_str_lit("application/json");
  out->status = (uint16_t)_r.u.ok.m_status;
  out->body = _r.u.ok.m_body;
  return 0;
}

int main(void) {
  dp_cfg cfg = dp_cfg_from_env();
  return dp_serve(dp_serve_handle, &cfg);
}
```

`entry` 的函数签名由运行时逆向构造：`Request` 的字段按白名单从 `dp_request` 填充
（`method` / `path` / `key` / `query` / `authorization` / `if_match` / `body`，多出字段报
`E-GEN`），`root` 由配置注入，`Host` 参数消失（Host 是编译期能力集合，调用点直接生成
`dp_blob_read(...)`）。**这是 `host` 的实现方式：不传值，直接内联调用。**

服务形态的入口返回类型必须是 `bytes | 错误` 或 `Unit | 错误`，否则 `check` 报 `E1`（v0.12 起；
以前是「必须是带 `status` 的 `Response | 错误`，否则 `E-GEN`」）。运行时只认 tag，不猜状态码。

### 5.4 语言子集与生成期的已知限制

这些限制都**显式报错**（带行列号），不允许静默降级。报错时机统一在 `check`
阶段：`droplet check` 说 ok 就等价于能构建（能力表见 `internal/host`，与 gen 共用）。

| 限制 | 现状 | 补齐时机 |
| --- | --- | --- |
| `env` 声明 | 语法能解析，checker 报 `E1`「不在 D0 子集」；配置现走环境变量（§6），`contract` 也不导出 env 分组（字段已删） | D1 按 SPEC §5.7 实现 |
| record 字段按值嵌入 | 被引用类型必须在源文件里先出现；循环引用不支持 | D1 再考虑指针化 |
| host 能力白名单 | `Sys` / `BlobFs` / `Versioning` 共 10 个能力已全部接通；表外能力、参数个数、参数与返回类型不符都在 `check` 就报 `E2`（表在 `internal/host`，含运行时 ABI 类型与返回值包装，与 gen 共用） | 无（新增能力时扩表 + `rt.h` 加原型） |
| 无 `entry` 的程序 | 生成立即返回 0 的 `main`，并引用全部函数避免 `-Wunused-function` | 无需补 |
| `If-Match` / 412 归运行时 | 412 由运行时在进 handler 之前判定。以前是因为构造不出错误值（裸变体名被 `E1` 拒），handler 表达不了“条件失败”；D1 变体落地后这个理由消失了 | 改成 handler 返回 `ApiError.Conflict`，同时删掉 runtime 自带的 If-Match 比对（下一项） |
| 提交是 leading-edge 防抖 | 窗口内的后续写不单独提交，要等下一次写才带上；若进程之后再无写入，最后一次写只落盘、不进历史 | 要“每次写都进历史”就把 `DP_COMMIT_DEBOUNCE` 设 0 |
| `contract` 导出的意图 | 只导出文字（`invariant` / `cap` / `idempotent`）与 `effects` 的标识符；`requires` / `example` 的表达式不导出（没有表达式打印器） | 需要时补一个 `droplet fmt` 的前置能力 |

## 6. C 运行时 `code/lang/internal/runtime/assets/`

> 下面列出 `rt.h` 的全部对外声明（排版有压缩，内容与顺序一致）。两边漂移时以 `rt.h` 为准并同步回本文。
> 实现切成四个文件：`rt_core.c`（arena / 字符串 / 日志 / hash）→ `rt_http.c`（HTTP）→
> `rt_blob.c`（文件哑存储）→ `rt_ver.c`（版本化钩子）。

```c
typedef struct { const char* p; size_t n; } dp_str;
typedef struct { const uint8_t* p; size_t n; } dp_bytes;
typedef struct { uint16_t tag; } dp_error;
typedef struct { int _; } dp_unit;

/* 错误 tag：必须与契约里错误变体的声明顺序一致 */
#define DP_ERR_NOT_FOUND   100u
#define DP_ERR_BAD_REQUEST 101u
#define DP_ERR_IO          102u
#define DP_ERR_CONFLICT    103u
#define DP_ERR_NOT_ALLOWED 104u
#define DP_ERR_PRECONDITION_FAILED 105u

/* ---- arena：按块分配，整块释放 ---- */
typedef struct dp_arena dp_arena;
dp_arena* dp_arena_new(size_t cap);
void*     dp_alloc(dp_arena* a, size_t n);
void      dp_arena_free(dp_arena* a);

/* ---- 字符串与字节 ---- */
dp_str dp_str_from_cstr(const char* s);
dp_str dp_str_dup(dp_arena* a, dp_str s);
bool   dp_str_eq(dp_str a, dp_str b);
bool   dp_bytes_eq(dp_bytes a, dp_bytes b);
bool   dp_val_eq(uint64_t a, uint64_t b);

/* ---- host Sys 能力 ---- */
uint64_t dp_sys_hash64(dp_bytes body);
dp_str   dp_sys_u64_str(dp_arena* a, uint64_t x);
int64_t  dp_sys_now_unix(void); /* 仓内唯一调用点是快照文件名的时间戳 */
void     dp_sys_log(dp_str msg);

/* ---- HTTP ---- */
typedef struct {
  dp_str method, path, key, query, authorization, if_match;
  dp_bytes body;
} dp_request;

/* ETag 不在这里：它由运行时从 body 推导（SPEC §2.2 H7）。 */
typedef struct {
  uint16_t status;
  dp_str   content_type;
  dp_bytes body;
} dp_response;

typedef struct {
  uint16_t port;
  dp_str   bind;       /* 监听地址，默认 "0.0.0.0" */
  dp_str   root;
  dp_str   token;      /* 空串表示不校验 Authorization */
  size_t   max_body;
} dp_cfg;

/* 返回 0 表示已填 out；100+ 表示错误 tag，由运行时映射状态码。
 * 参数里没有 root：配置由 `env` 声明驱动，生成的代码把加载好的值放进 dp_cfg（v0.11 起）。 */
typedef int (*dp_handler)(dp_arena* a, const dp_request* rq, dp_response* out);

int    dp_serve(dp_handler h, const dp_cfg* cfg);

/* ---- 文件哑存储 ---- */
bool     dp_valid_key(dp_str key);              /* ^[a-z0-9_-]{1,32}$ */
dp_bytes dp_blob_read(dp_arena* a, dp_str root, dp_str key, int* err);
uint64_t dp_blob_write(dp_arena* a, dp_str root, dp_str key, dp_bytes body, int* err);
/* 写前备一份旧版本：.snapshots/<key>.<unix>.<seq>，每个 key 只留最近
 * DP_SNAPSHOT_KEEP 份（默认 8，0 = 不轮转）。 */
void     dp_blob_backup(dp_arena* a, dp_str root, dp_str key);
bool     dp_blob_create(dp_arena* a, dp_str root, dp_str key, int* err);
dp_bytes dp_blob_list(dp_arena* a, dp_str root, int* err);

/* ---- 版本化钩子：防抖 + fork/exec，只提交本次 key ---- */
void dp_ver_commit_soon(dp_str root, dp_str key);

#define dp_str_lit(s)   ((dp_str){ (s), sizeof(s) - 1u })
#define dp_bytes_lit(s) ((dp_bytes){ (const uint8_t*)(s), sizeof(s) - 1u })
```

实现要点（M3/M4 落地后的真实行为）：

1. **HTTP/1.1 最小集**：单线程、一连接一请求、响应后关闭（不发 keep-alive）；只认
   `Content-Length`，出现 `Transfer-Encoding` 直接 501；header 总量 8192、单行 4096、
   响应头 512，读/写各 30 s 超时（超时回 408 后关连接，防单线程被半截 header 钉死）。
2. **状态码映射**（`status_for_tag`，v0.12 起为完整表）：`100→404`、`101→400`、`102→500`、
   `103→409`、`104→405`、`105→412`，其余错误 `500`。tag 派生的错误 **body 为空**；
   运行时自身的协议错误（400/408/412/413/501）另走 `send_status`，带一句文本。
3. **原子写**：写 `.tmp.<pid>.<seq>` → `fsync` → `rename`。
4. **快照**：`<root>/.snapshots/<key>.<unix>.<seq>`，写前先备一份；每个 key 只留最近
   `DP_SNAPSHOT_KEEP` 份（默认 8，0 = 不轮转），按 `(unix, seq)` 从旧到新删。带 `seq`
   是因为秒级时间戳单独用会让同一秒内的多次写互相覆盖，轮转也就无从谈起。
5. **版本化钩子**：防抖窗口内只起一个子进程；只把 `$DP_COMMIT_KEY` 这一个路径交给 git
   （扫全树会撞上正在 `rename` 的临时文件）；父进程不 `wait`，子进程由 `SIGCHLD=SIG_IGN`
   自动收割。防抖是 **leading edge**：窗口内的后续写不会单独提交，等下一次写触发。
6. **配置由源码里的 `env App { … }` 声明驱动**（v0.11 起，SPEC §5.7）：变量名由块名推出
   （`App` + `root` → `APP_ROOT`），源码里不出现变量名。业务五键 `APP_BIND`（默认 `0.0.0.0`）/
   `APP_PORT` / `APP_ROOT` / `APP_TOKEN`（空 = 不校验）/ `APP_MAX_BODY` 由生成的加载器读取，
   **必填键缺失或整数键非数字即在启动瞬间退出**，没有静默回退。只剩三个运维钩子还带 `DP_`
   前缀（已登记偏差）：`DP_COMMIT_CMD`（空 = 不提交）/ `DP_COMMIT_DEBOUNCE`（60）/
   `DP_SNAPSHOT_KEEP`（8）。
   `APP_ROOT` 必须是**已存在**的目录：启动时 `stat` 一次，不满足就打印原因并退出——
   不能起一个每个写请求都 500 的服务（`TestMissingRootFailsFast` 固定这一点）。
7. **RSS 基线**（本机，`cc -std=c11 -O2`，产物 35 KB、其中 text+data 19.7 KB）：空转 1.83 MB；
   200 轮小 `PUT`+`GET` 峰值 1.91 MB；8 MB `PUT` 峰值 9.98 MB、8 MB `GET` 峰值 10.10 MB
   （arena 按 body 上限预留，请求结束即释放给 allocator，不一定归还 OS）。

## 7. 目录与构建

### 7.1 编译器实现语言（已定：Go，2026-10-30）

**决策**：2026-10-30 拍板选 Go。理由：单二进制 + 交叉编译一条命令 + 环境轻；
`go:embed` 能把 `rt.c` 收进二进制；代价是 AST 遍历用 type switch。

**先澄清边界**：Droplet 是 `.dlt`，产物是 C 二进制；`.go` / `.cs` / `.ts` 只是**编译器自身的
实现语言**，不进交付物、不影响语言语义。类比如下：`rustc` 早期用 OCaml 写，Go 早期用
C 写，`tsc` 用 TS 写。**没有人用 OCaml 写 Rust 程序。**

**D0 不做自举**：写编译器需要 `list<T>`、字符串索引/切片、文件 IO、哈希表、错误报告，
而 D0 只有 `str/bytes/record/sum/match/!`。自举顺序必然是：D0 跑通服务 → D1 补集合与
字符串库 → D2 才有资格重写编译器。SPEC §12 的 S2 判据也不要求自举。

**三条决策标准**（其余都是偏好）：

| # | 标准 | 依据 |
| --- | --- | --- |
| 1 | 分发形态 | SPEC §8.3：安装即确定，无 PATH 顺序 / 虚拟环境 / 运行时选择 |
| 2 | 迭代速度 | D0 期间每天要改 lexer/checker，反馈慢直接拖垮进度 |
| 3 | 与 C 后端的衔接 | 编译器是「文本进、C 文本出」，这一条几乎不影响选型 |

**候选对比**：

| 语言 | 语言顺手度 | 单二进制 | 交叉编译 | 环境成本 | 判断 |
| --- | --- | --- | --- | --- | --- |
| **Go** | 中（无 sum type，靠 interface + type switch，样板多两三成） | 原生 | `GOOS/GOARCH` 一条命令 | 轻（Go SDK） | 最均衡 |
| **C#** | 高（`record` + `switch` 表达式 + 属性模式 + `Span`） | Native AOT 可以 | 需在目标架构容器内构建 | 重（.NET SDK + clang + zlib） | 熟练则最优 |
| **Rust** | 最高（`enum` + `match` 就是为编译器设计的） | 原生 | 需配 target 与 linker | 中 | 想练 Rust 再选 |
| **TS/Node** | 高（判别联合天然适合 AST） | 需 Node 运行时 | 不适用 | 最轻 | 最快原型 |
| **OCaml/Haskell** | 最高 | 需打包运行时 | — | 高（无现成环境） | 排除 |
| **C** | 低（字符串与 AST 全手管理） | 原生 | 需自备工具链 | 低 | 排除 |

**各候选的具体情况**：

- **Go**：`go:embed` 把 `rt.c`/`rt.h` 打进二进制，`droplet build` 时释放到临时目录 ——
  「编译器 + 标准库」收敛成一个文件，分发问题归零；标准库自带 `encoding/json`
  （`contract.json`）与 `text/scanner`（词法起步）。代价是 AST 遍历要 type switch，
  且需用测试保证 `default: panic` 分支永不触发。
- **C#**：语言体验接近 Rust，`abstract record` + `switch` 表达式写 checker 最顺；
  `Span<char>` 做词法零分配。分发靠 `PublishAot=true`（需 clang + zlib1g-dev），
  跨 OS/架构要在 `--platform` 容器里构建；AOT 下 `System.Text.Json` 必须用
  `JsonSerializerContext`（反射式序列化会失败）。
- **Rust**：`enum` + `match` 写 AST 是本命，但父子引用要 arena + index 或 `Rc<RefCell>`，
  迭代摩擦明显，编译等待也在小步修改时拖节奏。
- **TS/Node**：判别联合写 AST 最省事、AI 生成准确率最高，但装 `droplet` 就得装 Node，
  与 SPEC §8.3 的价值观冲突；适合「只想两周看到 hello」的场景。
- **OCaml/Haskell**：类型系统最适合编译器，但本机无环境、AI 语料少，不作为候选。
- **C**：D0 的 3 周会变成 2 个月，排除。

**当前排序**：Go（均衡）≈ C#（若熟练则并列第一）> Rust > TS > OCaml > C。

**决定性因素是长期维护者的熟练度**：编译器是接下来几个月反复修改的东西，
成本由「你读得最顺的那个」决定，而不是特性表。M0 开工后本项冻结，D0 期间不换语言
（换语言等于把已验证的逻辑重翻一遍）。

**工具链下限（2026-10-30 修正：1.24 → 1.22）**：`code/lang/go.mod` 从第一个提交起写的就是
`go 1.24`，但那只是开工那天的最新稳定版，不是需求 —— 全仓没有用到任何 1.24 专属 API
（`os.Root` / `strings.Lines` / `runtime.AddCleanup` / 泛型类型别名），也没有 1.23 的 `iter` / `unique`、
没有 1.22 的 `range int`；import 只有 `strings` / `os` / `fmt` / `sort` / `path/filepath` / `bytes`。
下限取 **1.22** 而不是更低，是因为**循环变量语义按 `go` 指令切换**：写 1.21 及以下会回到
「整个循环共享一个变量」的旧语义，闭包捕获会出现查不出来的别名 bug。证据（Go 1.22.5 实测）：
`go build` 通过、`go test ./...` 退出码 0、91 个用例全绿（v0.12 复测：95 个）、
`-print-c` 两次逐字相同、`check` 输出 `ok: 9 项声明，1 个函数，入口 handle`
（v0.12 删掉 `Response` → 8 项）。生成侧两处 map 遍历（`sortedFnNames`、
`contract` 的 tag 表）都先排序，输出顺序不依赖 Go 版本。

**不要在 `go.mod` 里补 `toolchain go1.24.0`** —— 那会让任何 1.22 环境在 `GOTOOLCHAIN=auto` 下去
联网下载工具链，把刚拿掉的网络依赖装回来（缺网时报的是"连接超时"，不是"版本不对"）。
开发用哪个版本写在文档里就够。护栏（**已建**，`.github/workflows/ci.yml`，2026-10-30）：跑两档 ——
`floor` 用 1.22 守下限（任何 1.23+ 的 API 混进来、或有人把 `go` 指令抬高，立刻红），
`latest` 用最新稳定版守前向兼容。两档都设 `GOTOOLCHAIN=local`，防止它偷偷下载新工具链
把问题"修好"。CI 只做 build / vet / test；`accept.sh`、`soak.sh` 要端口与常开机器，不进去。

### 7.2 目录结构（Go）

```
droplet-lang/code/lang/
  go.mod                         # module droplet
  cmd/droplet/main.go            # 动词表：parse / check / build / contract
  internal/token/token.go        # 词法单元与源码位置
  internal/lex/lex.go
  internal/ast/ast.go
  internal/parse/parse.go
  internal/check/               # 两遍式检查，导出类型/Tag/Sigs 供 codegen 复用（9 个文件，同包）
    check.go  types.go  decls.go  hostcap.go  boundary.go  stmts.go  exprs.go  calls.go  construct.go
  internal/host/host.go          # host 能力表：名字 + 参数/返回的 ABI 类型 + 生成 C 调用的方式
  internal/dump/dump.go          # AST → JSON（M0 的验收出口）
  internal/gen/                  # AST → C11（4 个文件，同包）
    c.go  stmts.go  exprs.go  main.go
  internal/gen/http_test.go      # 端到端：编出服务、拉进程、发真请求
  internal/contract/doc.go       # AST + 检查结果 → 契约 JSON
  internal/runtime/embed.go      # go:embed 打包运行时
  internal/runtime/assets/       # rt.h + 四个 .c（包目录里不能放 .c，所以放子目录）
    rt.h  rt_core.c  rt_http.c  rt_blob.c  rt_ver.c
  examples/hello.dlt
  examples/shape.dlt         # D1：变体构造 + match 绑字段
droplet-lang/code/webapi/
  src/{contract.dlt,main.dlt}
  deploy/{blobd.service,blobd.env.example,accept.sh,soak.sh}
droplet-lang/docs/
  SPEC.md  IMPL-PLAN.md  D0-DEVIATIONS.md   # 设计 / 实现计划 / 已实现与设计的偏差登记
```

构建：`go build ./cmd/droplet`。运行时由 `go:embed` 收进编译器，`droplet build` 把它释放到
临时目录后交给 `cc -std=c11 -O2 -Wall -Wextra` 直编（是否 `-static` 由部署方决定）。
当前 Web API 的构建与拉起（`APP_TOKEN` 空时跳过鉴权）：

```sh
droplet build code/webapi/src/contract.dlt code/webapi/src/main.dlt -o blobd
APP_BIND=127.0.0.1 APP_PORT=8787 APP_ROOT=./data APP_TOKEN=secret ./blobd
```

### 7.3 部署形态（M4）

服务只有一个二进制 + 一个数据目录，没有其它运行时依赖。systemd 是最短的路径：

```sh
sudo install -m755 blobd /usr/local/bin/blobd
sudo useradd --system --home /var/lib/blobd --shell /usr/sbin/nologin blobd
sudo install -d -o blobd -g blobd /var/lib/blobd/data
sudo install -d /etc/blobd
sudo install -m600 code/webapi/deploy/blobd.env.example /etc/blobd/blobd.env
sudo install -m644 code/webapi/deploy/blobd.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now blobd
```

要点：

- 对外只暴露反代：`APP_BIND=127.0.0.1`，行级访问控制交给 nginx / caddy；
- 数据目录必须**先存在**（`install -d` 那一步）：服务启动时 `stat` 一次，缺目录直接退出，
  不会起一个每个写请求都 500 的进程；
- `APP_TOKEN` 必填，否则 `/v1/*` 对所有人开放（`/health` 不需要）；
- 升级二进制：`systemctl stop blobd` → 拷新二进制 → `systemctl start blobd`；
  数据目录（含 `.snapshots`）不需迁移；
- 回滚判据（与 SPEC 一致）：契约测试全通 + 长稳无异常，否则停服务、换回旧二进制。
- 上线前后都跑 `sh code/webapi/deploy/accept.sh`：它自己建临时 git 仓库、拉一个监听
  `127.0.0.1` 的实例，不会碰到 `/var/lib/blobd`。

## 8. 里程碑

| # | 交付 | 通过判据（不过不进下一步） | 预估 | 状态 |
| --- | --- | --- | --- | --- |
| M0 | 骨架 + lexer + parser + AST dump | 三个样例 `.dlt` 全部 parse 成功；`droplet parse` 输出合法 JSON；错误带行列号与 E 码 | 3–4 天 | **已完成** 2026-10-30（3 样例通过，6 个单元测试绿，AST 2559 行） |
| M1 | checker（E0/E1/E3/E11）+ `check` | 三个样例 0 错误；11 个反例各自命中预期 E 码且不级联 | 4–6 天 | **已完成** 2026-10-30（14 个 check 测试绿，错误按行列排序输出） |
| M2 | codegen + `hello.dlt` 跑通 | `droplet build examples/hello.dlt && ./hello` 打印 `world` | 5–8 天 | **已完成** 2026-10-30（4 个 gen 测试绿，含 `-Werror` 端到端；30 个测试全过） |
| M3 | `rt.c`（HTTP + blob + hash + spawn）+ `main.dlt` 编译 | `curl` 五个接口全通；412 / 404 / 400 / 413 各自命中；RSS 有基线数字 | 1.5–2 周 | **已完成** 2026-10-30（11 个 HTTP 端到端子测试绿，412/404/400/413/405 均命中；RSS 空转 1.83 MB、8 MB `PUT` 峰值 9.98 MB；`droplet contract` 可导出契约 JSON） |
| M4 | 示例服务部署 + 契约验收 | 五接口 + 412 + 快照 + 一次版本化提交全通；连续运行 3 天无异常 | 2–3 天 | **进行中** 2026-10-30（13 项实况验收全通（v0.10 起多一项「405 空 body 不带 ETag」）；长稳脚本补了「自建实例启动即退出即报错」的护栏（端口残留时旧实例会接走请求，v0.10 实踩过）；快照已改轮转 + 带 `seq`；§8.1 审计完成，`check` 与 `build` 已对齐（`E2`/`E1`，含参数与返回类型比对）；**连续 3 天长稳未跑**） |

M3 的内部拆分（按风险排序，实际按此执行）：

1. ✅ `dp_serve` 请求循环 + `Content-Length` + header 上限（**最大的安全风险，先做**）；
2. ✅ `dp_blob_*` + `dp_valid_key`（含 `../evil`、空串、超长三类用例）；
3. ✅ `dp_sys_hash64` + `dp_sys_u64_str`（ETag）；
4. ✅ `dp_ver_commit_soon`：M3 只落空实现（不影响主链路），M4 补上防抖 + `fork/exec`，
   移到 `rt_ver.c`。

M3 除了运行时，还交了两件不在原判据里但契约先行需要的东西：

- `droplet contract` 动词：把 boundary / 函数签名 / tag 表导成 JSON，供消费方生成客户端
  （`env` 不导出：checker 已禁止 env 声明，见 `docs/D0-DEVIATIONS.md`）；
- `internal/gen/http_test.go`：把手工 `curl` 验证固化成能重跑的端到端测试（编译 → 拉进程 →
  发真请求 → 断言状态码与 body）。

M4 的契约验收（`sh code/webapi/deploy/accept.sh`，`APP_BIND=127.0.0.1 APP_TOKEN=secret
DP_COMMIT_DEBOUNCE=0`，数据目录是临时 git 仓库）：

| 请求 | 结果 |
| --- | --- |
| `GET /health`（无 token） | 200 |
| `GET /v1/keys`（空） | 200 `[]` |
| `PUT /v1/blob/demo` | 200 + 空 body（**响应不带 `ETag`**） |
| `GET /v1/blob/demo` | 200 + `ETag`（运行时从 body 推导，handler 一个字也没写） |
| `PUT` + 正确 `If-Match` | 200 |
| `PUT` + 错 `If-Match` | 412（未进 handler，所以既无快照也无提交） |
| `POST /v1/keys/second` | 200 + 空 body |
| `GET /v1/keys` | 200 `["demo","second"]` |
| `GET /v1/blob/missing` | 404 |
| `GET /v1/blob/../evil` | 400 |
| `DELETE /v1/blob/demo` | 405（handler 兜底） |
| `DELETE /v1/blob/demo` 的响应头 | 无 `ETag`（空 body 没有表示，所以不推导） |
| `PUT` 9 MiB | 413 |

副作用核对：`.snapshots/` 出现 `demo.<unix>`；`git log` 两条 `snapshot demo`（对应两次成功的写，
412 那次不触发）；服务端日志两次 `commit spawned pid=... key=demo`。前四项另有
`TestSnapshotOnPut` / `TestVersioningCommit` / `TestVersioningDebounce` 固化，不必每次手工重跑。

M4 剩下的只有长稳观测，跑法：每 30 s 一次 `PUT`+`GET`，每小时记一次 `VmRSS`，
斜率应为 0；同时看 `systemctl status blobd` 的 `MemoryCurrent` 与重启次数。
脚本已写好：`sh code/webapi/deploy/soak.sh [小时]`（默认 72）——自己建实例、每轮
`PUT`+`GET` 并校验内容、每小时采样 RSS/线程数/子进程数，退出时对 (时间, RSS) 做
最小二乘并对照阈值判定。

压缩时间自测（`SOAK_SECONDS=45 SOAK_INTERVAL=0 SOAK_DEBOUNCE=0`）：602 轮、602 次提交、
0 失败，RSS 1976 → 1984 KB（斜率 0.6 KB/h），全程 1 线程无残留子进程。**这只是 45 s
的高频替代，不等于 3 天**：真需在常开环境上跑默认口径（30 s × 72 h = 8640 轮）。

快照轮转也在同一口径下验过：390 轮（8 key 各 48 次写）后 `.snapshots/` 恰好 64 个文件
（8 key × keep 8），不再线性增长；文件名带 `seq`，同秒多次写不互相覆盖。

### 8.1 M4 收尾审计（附录判据的执行结果）

附录那条「仍从未被 `.dlt` 用到的特性说明它该删」在 M4 末尾逐个跑了一遍：

| 项 | 实测 | 结论 |
| --- | --- | --- |
| `env` 声明 | `check` 报 0 错、`build` 也成功，配置声明被**静默丢弃** | **已修为 `E1`**，`TestEnvDeclRejected` 固定 |
| `match` | 能 check、能 build、能跑（分支之间必须有逗号） | 保留；但 D0 无构造 sum 值的途径，只能匹配零值 → 记入 §5.4 |
| `Sys.getenv` | 能力表 / `rt.h` / `rt_core.c` / `contract.dlt` 四处齐全，**零调用点** | **已删**：`dp_sys_getenv` 23 行 + 能力项 + 契约声明 + `EnvError` |
| `Sys.now_unix` | `.dlt` 无人调用，但 `rt_blob.c` 的快照名在用 | 保留；`rt.h` 注释改为「仓内唯一调用点是快照文件名的时间戳」 |
| `dp_arena_reset` | 原型与定义齐全，**零调用点**（每请求新建 arena、用完整块 free，从不复用） | **已删**；新增 `TestRuntimeHasNoDeadFunctions`，把「运行时函数必须有调用点」变成回归测试 |
| `true`/`false`、`!=` `<` `<=` `>` `>=` `&&` `\|\|` `+` `-` `*` `/` `%` | 均可达，三个 `.dlt` 都没用到 | 属例子覆盖不足，不是死代码；不为它们硬凑样例 |
| 契约 JSON 的 `envs` 分组 | checker 拒绝 env 后 `contract` 走不到导出那一步，字段永远为空 | **已删** `Doc.Envs` / `Env` / `envOf` 与对应断言（v0.11 `env` 落地后按原样恢复） |
| `Response` 的字段形状（缺 `status`、写了 `etag`、写了未知字段） | `check` 说 ok、`build` 报 `E-GEN`（三条样例之外才碰得到） | **登记为残留偏差**：这一类判定本来就长在 gen（§5.3 写的就是 `E-GEN`），所以「`check` 的 ok 等价于能构建」只对**能力表**成立 —— 见 D0-DEVIATIONS §2。**v0.12（D2）已结**：`Response` 整个类型被删，这一类判定从结构上消失，成功形状改由 `check` 认 `bytes` / `Unit` |
| `Sys.hash64` / `Sys.u64_str` | v0.10 起 `webapi` 不再声明 `host Sys`，两个能力在 `.dlt` 侧**零调用点**（运行时内部推 ETag 仍在用） | 保留能力项与 `rt.h` 原型；`hello.dlt` 还在用 `sys.log`。要删的是「能力项能不能被运行时内部消化」这条判据，D2 复评 |
| `droplet help` 的 contract 描述 | 写着「boundary / env / 函数签名 / tag 表」，而 `envs` 字段早已删 | **已修**：帮助文本去掉 env |
| `-o` 缺值 | `droplet build x.dlt -o` 静默回退到 `a.out`，人以为产物在指定位置 | **已修**：报「-o 需要一个输出路径」，`TestSplitBuildArgsMissingOut` 固定 |
| `dump` 的 Intent 节点 | 规则名（invariant / requires）写进 `kind`，把节点类型覆盖掉，`parse` 输出里根本没有 `Intent` 节点 | **已修**：规则名改放 `rule`；新增「dump 不得出现 Unknown* 节点」两条测试（样例 + 全语法探针） |

删完后的能力表是 10 个（`Sys` 4 + `BlobFs` 5 + `Versioning` 1）。

审计还撞出一条跨阶段的系统性问题：**`check` 说 ok 而 `build` 报 `E-GEN`**。表外能力
（`sys.nope`）、带字段变体（`Err { code: u64 }`）都是这个形状，根因只有一个——能力表
`hostCaps` 长在 `internal/gen/c.go` 里，checker 看不见它。

**已修（2026-10-30）**：能力表搬到 `internal/host/host.go`（名字 + 参数/返回的 ABI 类型 +
生成 C 调用的方式），check 与 gen 共用一份：

- 表外能力、参数个数不符 → `check` 报 `E2`（"运行时不提供能力 Sys.nope"）；
- 参数或返回的类型与运行时原型不符 → `check` 报 `E2`（以前要等 cc 报
  `incompatible type for argument`，既没有 E 码也没有 `.dlt` 行号）；
- 带字段的变体 → `check` 报 `E1`（"错误只按 tag 映射到状态码"）；
- `internal/host/host_test.go` 锁住「能力表每一项都能在 rt.h 找到原型」与「恰好 10 项」。

于是 `droplet check` 的 `ok` 现在等价于「能构建」。gen 里的同名检查降为防御性兜底（gen 也可
被测试直接调用）。

全部偏差（含原因与补齐里程碑）集中在 [`docs/D0-DEVIATIONS.md`](D0-DEVIATIONS.md)，
每一条都附了可执行的复核命令。

### 8.2 H7 基线：D0 的语法减重清单

SPEC §2.2 的 H7（重复书写预算）是补上去的，而 D0 的实现正好是它的第一份实测基线。阈值是「0 处」，
所以下表每一条都是待还的账；不写进清单就等于默认接受。

| 缺口 | 实测（2026-10-30） | 减法形式 | 归属 |
| --- | --- | --- | --- |
| `Response` 的 `etag` 字段可推导 | `etag: ""` ×4，共 5 处构造；唯一非空值是 `sys.u64_str(etag.hash)`，而 GET 路径的 tag 也是 `sys.u64_str(sys.hash64(body))` —— 同一个东西算了两遍 | 删掉 `etag` 字段，由运行时从 body 算。**不能靠“字段默认值”绕过**：§5.2 已写明“缺省值必须显式构造，零值不合法” | **已落地**（D1 v0.10：`dp_response` 少一个字段，`Response` 只剩 `status` + `body`；再写 `etag` 在生成期报错并给改法，未知的响应字段也一并拒绝） |
| `entry X { fn f }` 双层外壳 | 2 处 `entry`（`hello.dlt` 的 `Demo`、`main.dlt` 的 `Serve`），每处只有 1 个 `fn`，这层从不承载内容 | 把签名直接挂在 `entry` 上，去掉内层 `fn` | **已落地**（D1 v0.9：`EntryDecl` 整个节点删掉，入口=带 `Entry` 标记的 `FnDecl`；将来的鉴权/超时预算写成签名上的 intent，不回头加壳） |
| host 声明的参数名 | `fn log(msg: str)` 的 `msg` 既不影响语义，也不进报错文本（报错写的是“第 N 个参数”） | 从语法里删掉：`fn log(str) -> Unit` | **已落地**（v0.7，只改 parser 与样例） |
| 末尾 `return Unit` | `hello.dlt` 11 行有效代码里的 1 行纯骨架；任一 Unit 函数都要写 | 规定 Unit 函数**不写** `return` | **已落地**（v0.7，只改 parser 与 gen） |
| `boundary ... with idempotency` | 单个效果也要 `with` | 单效果用标记：`get(key: str) -> bytes idempotent` | D3（与 412 合流同批） |
| `Response` 的 `status` 重复写同一个常量 | **etag 归零后实测仍在**：`main.dlt` 里 `status: 200` 出现 5 次（超阈值 3） | 删不掉：它们重复的是**值**而不是书写位置。要动的是 handler 的返回形状 ——「成功即值」：`entry` 直接返回 `bytes \| ApiError`，状态码与 `Content-Type` 由运行时包装，`Response` 只在需要非 200 的成功码时才出现 | **已落地**（D2 v0.12：`Response` 整个类型被删，5 处 `status: 200` 全部消失 —— 成功恒 200，状态码只由错误变体的 tag 映射） |
| `Response` 的 `body` 重复写空字面量 | 同上，`body: b""` 出现 3 次（POST / PUT / 405 三处） | 同上一条：没有内容就是没有内容，不该要求手写一个空字节字面量 | **已落地**（D2 v0.12：两处改成裸 `return`（R3 的单位值），一处改成 `return ApiError.NotAllowed`） |

**这两行在 v0.12 归零，H7 第一次真正达标。** v0.10 时表里还剩 8 处：前三条减法删的是书写位置，
这 8 处删的是**值本身** —— 书写层的预算已经抽干，剩下的是形状层，所以必须动返回形状而不是加语法糖。
复验命令（阈值 0）：
`grep -c 'status: \|body: b""' code/webapi/src/main.dlt` 应为 0。

**减法一律采取“必不写”，不用“可省略”。** 允许省略只是把“必写”变成“写不写都行”：手写字节没少、
选择成本没少，还多出一种写法（公理 3）。要减就从语法里删掉那个位置。

`match` 分支之间必须写逗号、尾随可选 —— 这条容易漏，但它**不是 H7 项**（逗号不是重复书写，
是分隔符），已记在 §5.4 与 §8.1。

两条「D0 可修」已于 2026-10-30 落地（SPEC §5.1.1）：旧写法一律编译期报错并给改写提示
（`E0006: host 能力的参数不写名字：把 “…: ...” 改成只写类型`、
`E1: …是多余的：返回 Unit 的函数不写 return，删掉这一行即可`），不做静默兼容；
`boundary` 方法与普通函数的参数名保留（前者进对外契约 JSON，后者进函数体）。

第三条（`entry` 壳）随 D1 落地，报错文本是 E0006「entry 直接写签名，不套 `{ fn … }`」并给改写提示；
同批补的两条护栏是多入口报 E1、`entry` 缺函数体报 E0006，三条都实跑验证过，见 D0-DEVIATIONS §6 的 i / j / k 探针。

第四条账（`Response.etag`）随 D1 落地，v0.10。报错在**生成期**：
`E-GEN: Response.etag 不用写：ETag 头由运行时从 body 推导（SPEC §2.2 H7），删掉这个字段`；
同批补一条一直漏着的护栏 —— `Response` 里非 `status` / `body` / `content_type` 的字段以前被**静默丢弃**
（声明了也不进响应，也不报错），现在报 `E-GEN: Response.retry_after 无法映射到响应：可写的只有 status / body /
content_type`。两条分别由 `TestResponseEtagFieldRejected` 与 `TestUnknownResponseFieldRejected` 固化。

**D2（v0.12）把这三类连同 `Response` 一起删了**：记录不存在，就没有「缺 `status` / 写 `etag` /
写未知字段」这三种写法；规则搬进 `check`（`E1`），由 `TestServiceSuccessMustBeValue` 固化，
上面两个 `E-GEN` 用例随之删除。`fs.write` 返回的新版本 `ETag` 在 `main.dlt` 里仍被 `!` 丢弃 ——
丢弃返回值本来就是合法的，**存储层的版本事实不变**，变的只是「谁把它写进响应头」。

硬证据：前三条减法各自落地前后，`droplet build -print-c` 在「旧样例 + 旧编译器」与「新样例 + 新编译器」
下输出**逐字相同**（`hello.dlt` 与 `webapi` 各一份）；等价性本来也可以从结构上看出来 —— 能力调用走
`internal/host` 的运行时原型（参数名不参与生成），`return Unit` 与 gen 的兜底
`return ((dp_unit){0});` 是同一句。减法不改变语义，只改变要写的字节。

第四条不同，它是**唯一一条必须改变生成 C 的减法**（少一行 `out->etag`，`dp_response` 少一个字段，
`rt_http.c` 多一处推导）—— 因为它删的不是书写层冗余，而是**同一件事的两次计算**。判据因此换成
**HTTP 行为等价**：`GET /v1/blob/<key>` 的 `ETag` 与改造前逐字节相同（新旧都是同一个函数对同一份 body 求值），
`If-Match` 的比对也走同一个函数，「GET 取 tag → PUT 带 tag」这条链一字未动。变的是三处书写位置：
`PUT` 的成功响应不再带 tag（空 body 没有表示），`/health` 与 `/v1/keys` 的响应**开始**带 tag（旧代码在那两处
写的是 `etag: ""`，等于不发头）。规则统一成一句：**有表示就有版本，没表示就没版本**。

「D1」的三条会动 AST 或 HTTP 契约，按里程碑走（`entry` 那条已于 v0.9 落地，`etag` 那条已于 v0.10 落地，
剩 `env`；`boundary` 的单效果标记也算 D1）。
不修，H7 就只是一句口号 ——
而它存在的理由正是“口号拦不住样板”。

### 8.3 H8 基线：D0 的实现规模

H8 是 SPEC §2.2 里最新补的一条（单文件 ≤ 800 行、编译器 + 运行时 ≤ 8000 行）。D0 的第一份盘点：

| 位置 | 行数 | 对照 H8 |
| --- | --- | --- |
| `internal/gen/`（4 个文件，最大 `c.go` 335） | 860 | ok |
| `internal/check/`（9 个文件，最大 `types.go` 224） | 1330 | ok |
| `internal/parse/parse.go` | 661 | ok |
| `internal/ast` | 374 | ok |
| `internal/lex` | 311 | ok |
| `internal/dump` | 258 | ok |
| 其余（cmd / contract / host / token / runtime） | 770 | ok |
| Go 非测试合计 | 4564 | — |
| 内嵌运行时 C（4 个 `.c` + `rt.h`） | 1470（最大 `rt_http.c` 622） | ok |
| **合计** | **6034** | 占 8000 的 75% |

另一个不打算消灭、只打算标价的数字：**新增一个 AST 节点要改 5 处** —— `ast`（结构体）→ `parse`（解析）→
`check`（类型与签名）→ `gen`（生成 C）→ `dump`（`parse` 的输出）。这是管道结构的必然，不是缺陷；
登记它只是为了在“加个语法”的决策时看得见价钱。目前只有 `dump` 有专用护栏（`Unknown*` 两条测试），
`check` 与 `gen` 靠用例（v0.12 起 95 个，其中 `TestErrTagsMatchRuntime` 是 gen 侧第一条
跨语言一致性护栏 —— 它盯的是 checker 的编号和 `rt.h` 的常量有没有对不上）。

处置（已完成 2026-10-30）：`check.go` 原 1188 行**同包拆 8 个文件**（不拆包）——
`check.go`（编排与 `Result`，161）、`types.go`（类型 / 作用域 / 类型判定，225）、
`decls.go`（声明收集与签名，213）、`hostcap.go`（能力表比对，76）、
`boundary.go`（边界与意图，87）、`stmts.go`（函数体与语句，196）、
`exprs.go`（表达式，214）、`calls.go`（调用与实参，103）。

比原计划（7 个）多一个 `calls.go`：7 文件方案下 `exprs.go` 会到 303 行，超过 250 目标 ——
「拆了等于没解决痛点」，H8 的度量单位是"一处改动要读多少上下文"，不是"文件数达标"。

**逻辑零改动**，三条独立证据：

1. **代码级**：56 个顶层声明（去掉注释与缩进后）逐字一致 —— 用脚本把两边的声明切开排序比对；
2. **行为级**：`droplet build -print-c` 的 C 输出逐字相同（`webapi` 与 `hello.dlt` 各一份），
   `droplet contract` 的 JSON 与 `droplet parse` 的 AST JSON 也逐字相同；
3. **诊断级**：7 个反例（E1 未定义类型 / E11 缺意图 / E2 能力参数谎报 / E3 不可序列化 /
   env 拒收 / match 未穷尽 / 变体带字段）的错误输出逐字相同，含行列号与消息。

兜底是 91 个用例 + `accept.sh` 13 项 + `go vet`。

D1 追加（2026-10-30）：变体构造落地时 `gen/c.go` 从 798 涨到 826，**卡线变成破线**，
按同一条处置拆成 4 个文件（`c.go` 338 顶层拼接与函数、`stmts.go` 122 语句、`exprs.go` 221 表达式、
`main.go` 179 两种入口产物）。`check/` 同步多出 `construct.go`（91 行，record 与变体共用
`checkFields`），把 `exprs.go` 压回 184 行，守住「每个非测试文件 ≤ 250 行」。
`check.go` 从 161 涨到 163：checker 多了一张 `variantOwner`（变体名 → 首个声明它的 sum）。
证据同上一条：拆分前后 `droplet build -print-c`（`hello.dlt` 与 `webapi`）与 `droplet contract`
的输出逐字相同 —— 变体构造是新语法，两个既有样例都没用它。
上面那 7 个诊断级反例里只有「变体带字段」翻面（`E1` → `ok`），其余 6 条的报错文本、行列号一字未改；
翻面后新增两条反例（带载荷变体裸写 `T.V` 要提示补 `{ … }`、同名变体跨 sum 要报 tag 冲突），
命令与期望输出登记在 D0-DEVIATIONS §6。

D1 第二项（2026-10-30，v0.9）：`entry` 扁平化删掉了 `EntryDecl` 这个 AST 节点 ——
入口就是一个带 `Entry` 标记的 `FnDecl`，于是 `check`/`gen`/`contract`/`dump` 各少一个分支
（Go 非测试合计 4570 → 4566，是本项目第一次**减法让编译器变小**）。
代价写在 parse：多一段「旧写法给改写提示」的判定。顺带补了两条一直漏着的护栏：
多个 `entry` 以前静默按 map 迭代顺序挑一个当 EntryPoint（生成哪个 `main` 由随机数决定），
现在报 `E1`（旧编译器连跑 6 次同一个程序，入口在 `f` 与 `g` 之间跳）；
`entry` 只写签名不写函数体以前 `check` 说 ok、到 `cc` 才炸（`undefined reference to 'dp_f_f'`，
无 E 码无行号），现在 parse 就报 `E0006: entry f 必须有函数体`。
等价性证据同前：旧编译器跑旧语法、新编译器跑新语法，`hello` / `shape` / `webapi` 三份
`build -print-c` 输出逐字相同；`contract` JSON 只有入口函数的 `pos` 变了（壳占的两行没了）。

D1 第三项（2026-10-30，v0.10）：删 `Response.etag`。**这条减法让书写面变小、实现面变大** ——
样例 86 → 80 行（`contract.dlt` 50 → 43：`Response` 少一个字段（多一行说明注释）、`host Sys` 整块不再需要；
`main.dlt` 36 → 37：少 5 处 `etag`、少 GET 的 tag 计算、多两行解释为什么可以丢弃 `fs.write` 的返回值），
而 gen 多两个拒绝分支（+3）、`rt_http.c` 多一处推导（+7）、`rt.h` 多两行说明（+1），合计 6023 → 6034。
这不是把账赖掉了：H7 管的是**用户要手写的字节**，H8 管的是实现总量，两者本来就要分开结账；
6034 仍占 8000 的 75%。真正的净收益是**少一处能算错的地方**：以前响应头的 tag 由 handler 算、
`If-Match` 的比对由运行时算，两处必须用同一个函数才对得上（对不上就是静默的并发 bug）；现在响应侧
只剩运行时一处推导。

顺带删掉一条能力授权：`webapi` 的入口以前收 `sys: Sys`，v0.10 之后它在 handler 里零调用，
按「能力是最小授权」应当从签名消失 —— 于是 `contract.dlt` 不再有 `host Sys` 块，
`droplet contract` 的 `hosts` 从 3 项降到 2 项（`BlobFs` / `Versioning`，`doc_test.go` 同步）。

D1 第四项（2026-10-30，v0.11）：`env` 一等特性。**这条是加法，账要如实记** ——
样例 80 → 91 行（`contract.dlt` 43 → 54 全是那个 `env App` 块；`main.dlt` 37 未变），
实现 6034 → 6381（Go 非测试 4566 → 4927：`check/env.go` 145、`decls.go` +87、
`gen/main.go` 的加载器与接线约 120、`types.go` +23、`contract/doc.go` +38；运行时 1470 → 1454）。
占 8000 的 **80%**，余量 1619 行 —— 从此「加特性」必须同时回答"它替掉了谁"，
否则 §8.4 之后的 D3 会把余量吃干。

D2（2026-10-30，v0.12）：删 `Response`。**计划估 +10 行，实际 +118 行**，差在注释与三处新规则
（下面各条都带行数，合计即实测）：

| 文件 | 变化 | 说明 |
| --- | --- | --- |
| `check/env.go` | +32 | `checkServiceResult`（R1）+ 错误提示 |
| `check/check.go` | +27 | `markErrorTypes`：错误类型按签名认，不再靠 `!` |
| `check/stmts.go` | +17 | `bareReturnable`（R3） |
| `check/types.go` | +9 | `okType` |
| `check/exprs.go` | −2 | 删掉 `!` 处的 `markError` 调用（规则搬走） |
| `gen/stmts.go` | +32 | 裸 `return` 的单位值 + `isErrItem`（错误是值要进 `.tag`） |
| `gen/main.go` | −8 | 删响应字段映射（原估 −18，实际仍要保留成功类型兜底） |
| `gen/c.go` | +3 | 兜底 `return` 支持 `bytes` |
| `rt.h` / `rt_http.c` | +8 | 两个 `DP_ERR_*`、405/412 映射、tag 派生错误改空 body |
| **合计** | **+118** | 6381 → **6499，占 8000 的 81%**，余量 1501 |

样例 91 → 88 行（`contract.dlt` 54 → 51：删 `Response` 3 行、`ApiError` 那行加两条注释抵掉一行；
`main.dlt` 37 → 37：少 8 处字段但多了 4 行注释）。**H7 归零是本轮唯一真正到手的净收益**；
H8 涨了 118 行，其中约 1/3 是注释（这个仓库的注释也是实现，不另算）。

---

### 8.4 D2 方案：删掉 `Response`（响应面统一）· **已落地（v0.12）**

> 本节保留为**决策记录**（为什么是这三条、反对意见是什么）。落地结果、实测行数与证据见
> §8.3 的 D2 段、SPEC §5.1.2 与 D0-DEVIATIONS §2/§5。

> 本节是决策材料，不是已生效的规范。点头后才写 SPEC、才动代码。

#### 8.4.1 为什么把三条合成一条

NEXT §2 P1 上挂着三条：①「成功即值」的返回形状、②把响应面收进 checker、③412 改回错误是值。
它们动的是同一片表面 —— **入口返回类型 → 响应构造 → checker 与 gen 对"响应"的理解**。
分开做要付三次长稳、三套等价性证据；合起来付一次。

#### 8.4.2 现状证据（2026-10-30 实测）

| # | 事实 | 位置 |
| --- | --- | --- |
| 1 | H7 欠 8 处：`status: 200` ×5、`body: b""` ×3 | `code/webapi/src/main.dlt` |
| 2 | `check` 说 ok、`build` 才报 `E-GEN` 的三类：缺 `status`、写 `etag`、未知响应字段 | `gen/main.go:106-163` |
| 3 | `If-Match` 比对在运行时，注释自认「D0 无法表达『条件失败』这个返回值」 | `rt_http.c:460-483` |
| 4 | 405 的响应体今天由 handler 给（空），而 tag 派生的错误走 `send_status(…, "error")`（非空） | `rt_http.c:166`、`main.go:147` |
| 5 | `status_for_tag` 只有 404/400/409 → 其余一律 500，没有 405/412 | `rt_http.c:126-132` |

#### 8.4.3 提案：`Response` 这个类型不该存在

```
entry handle(req: Request, fs: BlobFs, ver: Versioning, cfg: App) -> bytes | ApiError
  ...
  if req.path == "/health"   { return b"{\"ok\":true}" }
  if req.path == "/v1/keys"  { ... return fs.list_json(cfg.root)! }
  ...
  return ApiError.NotAllowed        // 405，由 tag 表映射，不再手写状态码
```

三条规则：

- **R1 成功只有一种形状**：`bytes`（有体）或 `Unit`（无体）。`Response{status, body}` 这种记录类型
  不再被接受，`check` 报 `E1` 并给出改写提示。提示文本按 E9 的处置纪律写：**"你写的 `Response{status: 200,
  body: X}` 在本语言里就是 `X`；状态码不是能构造的值，是标签表的投影。"**
- **R2 状态码只在一处出现**：变体 → HTTP 状态码的映射表（`status_for_tag`）。新增
  `NotAllowed`(405)、`PreconditionFailed`(412)。成功恒 200；`Unit` 成功 = 200 + `Content-Length: 0`。
  201/204/206 等留到 `boundary` 合成，登记 D0-DEVIATIONS。
- **R3 裸 `return` = 该返回类型的单位值**：`Unit` → `()`，`bytes` → 空 body。
  它不违反 §5.2「缺省值必须显式构造，零值不合法」——**它不是零值，是不写字**。
  这条是 D2 里唯一新增的语义，也是唯一值得反对的一条。

#### 8.4.4 H7 前后账

| 处 | 现在 | D2 之后 | 消灭者 |
| --- | --- | --- | --- |
| `status: 200` ×5 | 每个成功分支手写 | 不写（成功恒 200） | R2 |
| `body: b""` ×3 | 手写空体 | 裸 `return`（2 处）/ `ApiError.NotAllowed`（1 处） | R3 / R2 |
| **合计** | **8 处** | **0 处** | |

H7 的判据第一次可以写成 0 处而不是"8 处待还"。

#### 8.4.5 H8 账与文件预算

| 位置 | 动作 | 行数 |
| --- | --- | --- |
| `gen/main.go:146-163` | 删响应映射 switch（含 `etag`/未知字段两个拒绝分支） | −18 |
| `gen/main.go:104-111` | 删「成功值必须是带 `status` 的 record」判定 | −8 |
| `check/`（新 `entry.go` 或并入 `check/env.go`） | R1 规则 + 改写提示 | +20 |
| `rt_http.c` | `status_for_tag` 加 405/412；`send_status` 调用点改空体 | +4 |
| `check/` | R3 的返回类型规则 | +12 |
| **净** | | **+10** |

现状 6381（80%）→ 约 6391。`check/env.go` 145 + 20 = 165，未破 250 的单文件目标；
若另起 `check/entry.go` 则两个文件都更小，二选一在动工时定。

> **实际落地：+118，不是 +10**（6381 → 6499，81%）。估低了四处：三处新规则都带注释、
> `markErrorTypes` 不在原计划里（错误类型改按签名认）、`gen/main.go` 仍要保留成功类型的兜底判定、
> `gen/stmts.go` 要处理「返回错误变体」这条新路径。逐文件账见 §8.3 的 D2 表。

#### 8.4.6 行为等价：两条最容易翻车，先写死

`accept.sh` 13 项必须逐条不变。其中：

- **405 的响应体必须仍为空**。R2 让 405 走 `send_status`，而它现在发 `"error"` 文本
  （`rt_http.c:166` 的 `msg`）。**必须把 tag 派生错误的 body 改成空**，否则 `accept.sh:54`
  的「405 empty body」翻面。要人类可读的错误文案，等 `boundary` 的错误表示（D3）。
- **412 必须仍无快照、无提交**。今天它在运行时就被挡掉、根本不进 handler（`accept.sh:48` 的
  「既无快照也无提交」正是这个原因）。D2 不动这段，所以这条自然保持 —— 但也正因为不动，
  §8.4.7 的争议才存在。

#### 8.4.7 与上一班相反的一处：412 的运行时把守**不删**

NEXT 上一轮写的是「412 可以改回错误是值了，同时删掉 `rt_http.c` 的比对」。**只做前一半。**

- **做**：加 `PreconditionFailed` 变体 + 412 映射。这是 D0 的真实缺口 —— handler 现在无法表达
  「条件失败」，只能靠运行时替它做决定。
- **不做**：把 HTTP 层的 `If-Match` 判定搬进 handler。理由三条：
  1. 它是**安全边界**，与 `dp_valid_key`（`rt_http.c:453`，非法 key 不进 handler）同级。
     运行时把守时，handler 忘了比较也不会丢保护；搬到 handler 后，忘一次就是**静默的丢更新** ——
     正是本语言要消灭的失效类（C1）。
  2. 语言侧的正解不是"交还给人"，而是 `boundary … with idempotency` / 预条件声明**合成**这条检查
     （D3）。在合成能力存在之前，把检查搬走只是把保护降级成约定。
  3. 收益为零：搬走只删 24 行运行时，却要把同一语义写进每个 handler。
- 代价：一段时期内同一语义存在两处（运行时 + 可选的语言侧表达）。登记 D0-DEVIATIONS，
  补齐时机写死为 **D3 合流**。

#### 8.4.8 执行顺序（已完成，按实际结果记）

1. SPEC：`Response` 从样例消失；新增 §5.1.2 的 R1/R2/R3；§2.2 的 H7 状态从"8 处欠账"改成"0 处"。
   （计划里写的 §3.4 是笔误 —— §3.4 是 Java 那一节，样例并不在那里。）
2. 代码：`check` 加 R1/R3 → `gen` 删映射 → `rt` 加两项 status、错误 body 置空。
   额外多出两处：`markErrorTypes`（错误类型按签名认）与 `gen/stmts.go` 的 `isErrItem`
   （`return ApiError.NotAllowed` 要写进联合的 `.tag`，不能写 `.u.ok` —— 第一次编译就炸在这里）。
3. 测试：实际是 **91 → 95** 而不是 97：`check_test.go` 的 9 处服务样例改用 `bytes | ApiError`
   （不是计划里说的 3 处内联样例）；2 个 `E-GEN` 反例 **删掉**，换成 4 个 check 级 R1/R3 用例；
   新增 1 个 tag 表护栏（`TestErrTagsMatchRuntime`）与 1 个「412 不进 handler」集成用例；
   `TestServiceEndToEnd` 的 405 补空 body 断言。
4. 证据：`accept.sh` 13 项逐条过（405 仍空 body、412 仍无快照/无提交）→
   `SOAK_SECONDS=60 SOAK_INTERVAL=0` 866 轮零失败、RSS 恒 1852 KB → `-print-c` 不作为判据。
5. 文档：D0-DEVIATIONS（201/204 未支持；412 双处并存）、NEXT §0、本节改为"已落地"。

#### 8.4.9 三件待点头 → **三件都点头并落地**（2026-10-30）

| # | 事 | 结果 | 反对意见与缓解 |
| --- | --- | --- | --- |
| 1 | 删 `Response`，成功类型限定 `bytes` / `Unit` | **落地** | 真实服务要 201/204/206 时会缺表达力；已登记 D0-DEVIATIONS，缓解：D0 用不上，D3 由 boundary 合成 |
| 2 | R3：裸 `return` = 单位值 | **落地** | 它让 `return` 的含义依赖返回类型，是"两处语义"的种子；缓解：只有 `Unit`/`bytes` 两种，且 `return` 本来就已经是 Unit 函数的写法 |
| 3 | 412：只加变体，不删运行时比对 | **落地** | 一段时期同一语义两处实现，可能漂移；缓解：D3 合流写死，且运行时的那条是超集（先挡） |

---

### 8.5 D3 方案：让 `boundary` 成为 HTTP 面的事实源（路由表 + 条件请求合流 + 成功码投影）· **三件全部落地（v0.13 B1/B2，v0.14 B3）**

> 这一节按 §8.4 的写法带证据与反对意见。**2026-10-30 三件都点头**：B1（路由 × 守卫合成）与
> B2（一致性护栏，A 落地后自动成立）同批完成（v0.13）；B3（201/204）按建议拆成单独一批（v0.14）。
> 实结与执行差异见 §8.5.7（B1/B2）与 §8.5.8（B3），不回头改前面的估算数字。

#### 8.5.1 病根：同一条 HTTP 事实写在三个地方，而且契约是假的

实测（2026-10-30，v0.12 之后）：

| 事实 | 现在写在哪 | 证据 |
| --- | --- | --- |
| 一条路由存在 | handler 的 **3 个** `req.path ==` 字面量 + 运行时的路径归一规则 | `grep -c 'req.path ==' code/webapi/src/main.dlt` = 3 |
| 哪个方法是"写" | rt 的 `is_put`（`rt_http.c:381`）**加** handler 的 `req.method == "PUT"` | 同一事实两处 |
| 条件请求要不要把守 | rt 的 **22 行**块（`rt_http.c:462-483`），依赖 `strip_quotes`（:330-336，7 行）与 `is_put`（:381，1 行） | 删块会留死代码 + `-Wall -Wextra` 的两个警告（`-Wunused-function` / `-Wunused-variable`） |
| `boundary` 声明 | **gen 零消费** —— `grep -rn "Boundary" internal/gen` 为空，只有 `contract` JSON 与 `dump` 读它 | 声明目前是装饰 |
| `Request.if_match` | 声明在 `contract.dlt:16`，handler **从不读** | 这个字段是运行时替 handler 用的影子 |

比"412 有两处实现"更硬的一条账：**契约与真实 HTTP 面对不上**。`boundary BlobApi` 只声明
`get` / `put` / `list` 三个方法，而 handler 实际服务 **5 条路由** —— `/health` 和
`POST /v1/keys`（创建 key）在契约里根本不存在，`droplet contract` 导出的 JSON 因此是
**残缺事实**，而 `accept.sh` 第 1 项与第 7 项打恰恰是这两条没声明的路由。

> **结论**：「这条路由存在、它是写、它需要预条件」这件事，运行时知道得比源码多，
> 契约导出比运行时少。**412 双处并存是症状，不是病**；D2 的收尾把这个分裂写进了
> D0-DEVIATIONS，但它本身没动过 —— D3 要动的是这个。

#### 8.5.2 提案三件

**B1 · 路由与守卫合成（= 412 合流）**

- 语法只扩 `with` 清单：`put(key: str, body: bytes) -> Unit with idempotency, precondition`。
  `precondition` 走 `idempotency` 同一个标识符位（`parse.go:351-355`），**不新增关键字** —— H2 仍是 14。
- 路由与实现怎么对上，两个形状方案：
  - **方案 A（推荐）**：方法上加 `impl` 槽位指向实现函数，gen 合成一张路由表 + dispatcher，
    handler 的 if-chain 消失、变成若干普通 `fn`（本例 5 个：health / keys.list / keys.create /
    blob.get / blob.put）。parser 只加两个槽位，改动集中在 check + gen。
  - **方案 B**：SPEC §5.3 的字面兑现 —— `boundary` 方法直接带实现体。书写面最小，
    但 parser + check + gen + rt 四层同动，并引入"函数体写在声明块里"这个新结构。
    **附录 C 的 V1/V2 实验还没做**（状态机一等单位、AST 身份与语义 diff），
    在它们出结果之前不该把"思考单位"再定死一次。
  - A 的 `impl` 槽位就是 B 的雏形：**先 A，B 记作 A 之后的塌缩**（塌缩是删中间层，不是重做）。
- 比对逻辑搬去哪：函数**留在运行时**（`dp_precondition_ok(a, root, key, want) -> bool` 进 `rt.h`，
  因此受 `TestRuntimeHasNoDeadFunctions` 约束），**生成的守卫是它唯一调用点**。
  于是「实现一处、触发一处」，handler 不参与；`strip_quotes` 并进这个函数，不留死代码。
- 忘不掉的机制（合流的前提）：check 硬规则 —— **boundary 里任何会写的路由必须显式
  `with precondition`，或写 `without precondition: "理由"`**（同 `ignore(reason)` 的纪律：
  可 grep、可统计、不可默默缺席）。理由：把安全边界从固定位置搬到生成代码之后，
  "忘了声明"必须变成编译错误，否则 D2 §8.4.7 反对的那件事就真发生了。

**B2 · 路由一致性护栏（A 的临时形态）**：`check` 比对 handler 里的 path 字面量与 boundary
声明的路由集，不齐报 `E1`（"契约声明了 `/v1/blob` 的 PUT，handler 没有这条分支"，或反向）。
A 落地后 if-chain 消失，这条自动成立 —— 所以 B2 只是"想先拿收益又不动结构"时才单独做。

**B3 · 成功码投影（201 / 204）**：状态码仍然**不是构造物**，是表上的名字 ——
在方法上标 `@created` / `@no_content`，默认 200 不写；约束进类型面：

| 标注 | 要求返回 | 否则 | 理由 |
| --- | --- | --- | --- |
| `@created` | `bytes` | `E1` | 201 必须给出新建资源的表示体 |
| `@no_content` | `Unit` | `E1` | 204 带 body 是无效响应 |

源码里仍然不出现数字，**R2 不破**；handler 的形状不动（状态码由请求命中的路由决定，静态可知）。
**206 不进 D3**：它需要 Range 解析与部分响应，那是运行时新能力（要另开 H4/H5 的账），
与"成功形状"无关 —— 继续挂在 D0-DEVIATIONS。

#### 8.5.3 账（估，做完整表回填）

| 面 | 账 |
| --- | --- |
| H7 | `req.path ==` 3 → 0、`req.method ==` 3 → 0；新增的是"实现名写两次"（`impl blob_put` + `fn blob_put`）—— 那不是 H7 数的形态（H7 数同一常量重复），但记下来不藏 |
| H8 | 估净 **+127**（parser +20、check +55、gen +70、`rt_http.c` −22〔块 −22、`strip_quotes` −7、`is_put` −1、新函数 +8〕、`rt.h` +4）→ 6499 → 约 6630，**83%**，余量约 1370；B3 另加约 +40 |
| 契约 | `droplet contract` 的 JSON 从 3 条方法变 5 条路由，与运行时一致 —— 这条是 D3 唯一"净收益不需要辩护"的账 |
| 教学面 | 词汇 +3（`precondition` / `created` / `no_content`，都在 `with` / `@` 槽位），关键字 0；H2/H3 不动 |

#### 8.5.4 行为账：三条客户端可见，必须点头

1. **412 的 body**：从 `precondition failed` 文本 → 空（与 R2 一致，tag 派生错误一律空 body）。
   `accept.sh` 第 6 项加空 body 断言。
2. **201 / 204**（若采纳 B3）：`PUT /v1/blob` 200 → 204（空 body 因此**仍不发 ETag**，与现状同形）、
   `POST /v1/keys` 200 → 201。`accept.sh` 第 3、5、7 项改口径 —— 这是**主动破"行为等价"判据**，
   所以 B3 单独一批，不与 B1 混。
3. **`Request` 记录少两个字段**：A 之后 handler 不再看到 `path` / `method`（路由由表决定），
   `if_match` 保留但只有生成的守卫读它。

判据升级：B1 是**安全边界搬家**，长稳要按 3 天口径重跑（§2 P0 一起结），不是 60 s 冒烟。

#### 8.5.5 与 D2 相反的一处

D2 是纯减法：行为逐条等价，`accept.sh` 13 项不许动，判据"绿了就对"。
D3-B1 是**结构搬家**，行为应当等价但改动跨四层，所以光看 accept 绿不够 ——
要加一条对照：**旧/新生成的 C 逐字 diff**（除路由表与守卫那两处）+ **请求 × 响应矩阵逐项比**
（5 条路由 × {带/不带 token、合法/非法 key、带/不带 If-Match}）。这条是 D3 的验收本体。

#### 8.5.6 三件待点头

| # | 事 | 我的建议 | 反对意见与缓解 |
| --- | --- | --- | --- |
| 1 | boundary 与实现对上：方案 A（`impl` 槽位 + 合成 dispatcher）还是方案 B（方法带实现体） | **A** | 反对：A 中间层多、名字写两遍，B 才是终态。缓解：A 的 `impl` 就是 B 的雏形，塌缩只删一层；B 等 V1/V2 实验结论再定 |
| 2 | 客户端可见变更：412 空 body 随 B1；201 / 204 单独一批（B3） | **同意** | 反对：一次改两个口径更难归因。缓解：B3 独立成批，accept 逐项改并留旧值注释 |
| 3 | 新增硬门禁：写路由必须显式声明预条件，或写 `without precondition: "理由"` | **要**，它是合流的前提 | 反对：门禁加在 `boundary` 上会挡住"先声明后实现"的写法。缓解：理由形式与 `ignore(reason)` 同构，可统计；且它比现状强 —— 现状是"忘了也没人管" |

→ **三件都按建议点头（2026-10-30）**：① 方案 A；② 412 空 body 随 B1、201/204 拆批；③ 硬门禁生效。
B1 + B2 已落地，实结见下一节。

#### 8.5.7 实结（2026-10-30，v0.13）

| 面 | 估（§8.5.3） | 实 |
| --- | --- | --- |
| H8 | +127 → 约 6630（83%） | **+225 → 6724（84%）**。Go 非测试 5037 → 5255：新文件 `internal/check/route.go`（~190）、`env.go` 重写（服务形态判定 + 键校验抽出）、`gen/main.go` 的 `serviceMain`，parse / ast / token / contract 各自小幅。C 1462 → 1469：新函数与原型 +9，减掉 `is_put` 与 22 行 If-Match 块 −8 |
| H7 | `req.path ==` 3 → 0、`req.method ==` 3 → 0 | 达成。新增的"实现名写两次"（`impl blob_get` + `fn blob_get`）5 处，按 §8.5.3 的口径不计入 H7，但记在这里 |
| 契约 | 3 条方法 → 5 条路由 | 达成；还多了 `verb` / `path` / `impl` / `precondition` 四个导出字段 |
| 用例 | — | 95 → 106（+11：路由门禁 8、守卫位置 1、契约导出 1、env 形态 1） |

**执行差异（照实记，不粉饰）**：

1. `Request` 记录**整个删了**，不只是少两个字段（§8.5.4 第 3 条估得保守）。`if_match` 由生成的守卫
   直接从 `rq->if_match` 读，handler 侧零消费方 —— 留一个只有一处读的字段，正是 D2 删 `Response` 的同一个理由。
2. 服务形态判定从 `len(c.routes) > 0` 改成独立的 `serviceForm` 标志。否则**非法**路由不会被追加进
   `c.routes`，程序被误判成 CLI 形态，负例测试会连带报一串"env 无人消费"的假错误（第一次跑负例就撞上，
   级联错误会把真正的病因埋掉）。
3. `check_test.go` 加完路由门禁到 815 行，破了 H8 单文件 ≤ 800，按同包拆文件的规定拆出
   `internal/check/route_test.go`（612 + 211）。
4. `TestWebapiPasses` 的期望从"入口 handle"改成"无入口 + 恰好 5 条路由"；`doc_test` 从 3 条方法改 5 条，
   并断言 `verb` / `path` / `impl` / `precondition` 导出；`parse_test` 的 `TestParseWebapiMain` 从
   "首个声明是 entry handle"改成"首个声明是普通函数 health，blob_put 签名 `key,body,fs,ver,cfg`"。

**证据**：

- `accept.sh` 13 项逐条同值（412 仍 412、405 仍空 body、ETag 链一字未动），新增第 14 项
  「412 empty body (bytes) = 0」。
- `-print-c` 前后**没有做逐字节 diff**：对照的双方不是同一份源码的两个编译器版本，而是两种源码形状
  （手写 if 链 vs 合成的同一串 `dp_str_eq`）。所以判据换成**请求 × 响应矩阵** —— accept 的 13 项就是
  这张矩阵，逐条同值即等价；生成物只做结构断言（见下条）。
- `TestPreconditionGuardIsSynthesized`：守卫出现在 `dp_f_blob_put(` 之前，且声明了 `without` 的路由
  之后没有任何 `dp_precondition_check`。
- 60 s 长稳：`SOAK_SECONDS=60 SOAK_INTERVAL=3`，20 轮零失败、1 线程、无残留子进程。

**留给 B3 的**：`@created` / `@no_content` → 201 / 204（accept 第 3/5/7 项届时改口径，这是主动破
"行为等价"判据，所以拆批）；206 需要 Range 解析，不在范围。→ **已落地，见 §8.5.8**。

#### 8.5.8 B3 实结（2026-10-30，v0.14）：成功码成为声明的投影

形状：注解复用 D3 的 `@` 槽位，`@created`（201）与 `@no_content`（204）不带括号，
与 `@get("/path")` 并列写在 `impl` 之前。**数字不进源码** —— 源码写的是"产出了新东西"与"没有表示"，
R2（状态码只在投影表里出现）因此不破，反而更完整：错误码投在变体上，成功码投在路由上。

四条判定（全在 check，除最后一条在运行时）：

| 判定 | 位置 | 为什么 |
| --- | --- | --- |
| 只有写路由能投影成功码 | `check/route.go` | 读路由成功恒 200，`@created` 写在 GET 上说明作者把语义搞反了 |
| `@no_content` 要求成功类型是 `Unit` | `check/route.go` | 204 的定义就是没有表示；有 body 的声明投不出这个码 |
| 两个成功码注解只能写一个 | `parse`（`E0003`） | 同一条声明投两个码没有意义，越早在语法层挡住越好 |
| 204 不发 `Content-Length` / `Content-Type`、不写 body | `rt_http.c` 的 `send_response` | RFC 7230 §3.3.2 的 MUST NOT；放运行时是因为**它是状态码的定义，不是某条路由的选择** |

账：Go 非测试 5255 → 5301（+46：parser 注解循环、check 两条门禁、contract 一个字段）、
C 1469 → 1472（+3：`no_body` 判断与注释）、合计 6724 → **6773（85%）**；样例 92 → 95 行
（契约多两行注解与一段说明）；用例 106 → 114（成功码门禁 4、注解解析 3、投影生成 1）。

证据与两个"自己咬自己"的发现：

- `accept.sh` **17 项**：3 项改口径（PUT 200→204 两处、POST 200→201）、
  新增「412 empty body (bytes) = 0」「204 no Content-Length」「204 body (bytes) = 0」「201 头 Content-Length: 0」。
- `soak.sh` 每轮断言 `PUT=200`，改码后**直接 20/20 记失败** —— 这是跑了一次才发现的，
  说明长稳脚本里也硬编码了成功码；已改成 204，并在脚本里注明原因。
  教训：改客户端可见口径时，`grep` 整个 `deploy/` 而不只是测试。
- `TestSuccessStatusProjected` 断言生成物里恰好一条 201、一条 204，且 204 分支不含 `content_type` / `out->body`。

---

## 9. 风险与回退

| 风险 | 触发信号 | 回退动作 |
| --- | --- | --- |
| 手写 HTTP 出安全/正确性问题 | keep-alive 下响应错位、header 注入 | 关闭 keep-alive（`Connection: close`），退回每请求一连接 |
| 提交子进程与原子写并发 | git 扫到正在 rename 的临时文件，`fatal: unable to stat`，整次提交失败 | 只 add `$DP_COMMIT_KEY` 一个路径，不扫全树（已采纳） |
| `!` 的 tag 对齐复杂化 | codegen 出现映射表而非复制 | 简化：禁止跨函数传播，`!` 只在 entry 内使用 |
| RSS 远超 3 MB | M3 基线 > 8 MB | 记录实际值，按 SPEC §9 回砍（先砍 keep-alive 缓冲与日志） |
| 快照无限累积 | 按 `soak.sh` 默认口径跑 72 h，`.snapshots/` 会累积约 8640 个文件（每 30 s 一次写） | 已采纳：每 key 只留最近 `DP_SNAPSHOT_KEEP`（默认 8）；要全量历史就设 0 |
| 语言子集被需求拉动膨胀 | 出现「再加一个语法就能写」的请求 | 记入 `docs/D0-DEVIATIONS.md`（2026-10-30 已建立），M4 之后再议 |
| `check` 放行而 `build` 报 `E-GEN` | 代理把 `droplet check` 当作唯一 gate，把不可构建的候选当成通过 | **已修**：能力表搬到 `internal/host`（含参数/返回的 ABI 类型），`check` 报 `E2`/`E1`；gen 只留兜底 |
| C 后端迟迟跑不通 | M2 超过 2 周 | 暂停语言进度，先用既有实现顶着（可用性优先） |

M3 实测的 RSS：空服务（无请求）1.83 MB，符合 SPEC §9 的 H4（≤ 3 MB）；8 MB body 的 `PUT`
峰值 9.98 MB。后者是“接了 8 MB payload”的瞬时值，不是 H4 的口径，但说明 arena 是按 body
上限预留的：真要把这个数字压下去，得把 arena 改成按需增长。

## 10. 与消费方的接入约定

1. **服务只暴露 HTTP 契约**，消费方不需要知道 Droplet 存在，也不需要装任何 Droplet 工具；
2. 消费方只配置两项：`base URL` 与服务端侧的 `APP_TOKEN`（消费方自己的配置名无所谓）；
3. 服务端二进制独立部署，是否放进消费方仓库由部署方式决定（Docker / systemd / 直接跑均可）；
4. 替换判据：契约测试全通 + 连续运行 3 天无异常，才允许下线旧实现；
5. 旧实现可保留为对照，便于在 412 / 413 / 超时等边界行为上逐项比对。

---

## 附：本文档的判据（防止自己骗自己）

- 每个里程碑只有一个判据，且是**命令可执行**的；
- 每一条「不做」都有原因与补齐时机，不允许静默省略；
- 语言能力只从 `code/webapi/src/*.dlt` 反推，**不允许为了语法完整性增加实现**；
- 若 M4 之后仍有 `code/lang` 里从未被 `.dlt` 用到的特性，说明它该删（2026-10-30 已执行，结果见 §8.1）；
- 编译器实现语言已定 Go，M0 开工后冻结，D0 期间不换。
