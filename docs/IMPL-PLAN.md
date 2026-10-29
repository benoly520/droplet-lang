# Droplet 实现计划（D0 子集 → C 后端 → 示例 Web API 服务）

> 状态：v0.2 / 2026-10-30 / 上位文档 `docs/SPEC.md` v0.4
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

```
./droplet build code/webapi/src/contract.dlt code/webapi/src/main.dlt -o blobd
AUTH_TOKEN=dev DATA_DIR=/tmp/blobd ./blobd
curl -H 'Authorization: Bearer dev' 'localhost:8788/v1/blob/demo'
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

> 这份「不做」清单是对 SPEC 的**显式偏差登记**，不是遗漏。

## 2. 验收契约：哑存储 Web API

服务端是哑存储：它不理解 payload 内部的任何字段，只做字节读写与元数据。

| 方法 | 路径 | 成功 | 失败 |
| --- | --- | --- | --- |
| GET | `/v1/blob/<key>` | 200 + 字节 + `ETag` | 404 不存在 / 400 非法 key |
| PUT | `/v1/blob/<key>` | 200 + 新 `ETag` | 412 版本不符 / 413 超限 / 400 |
| GET | `/v1/keys` | 200 + `["k1","k2"]` | 500 |
| POST | `/v1/keys/<key>` | 200 + 空 body | 400 非法 key / 500 |
| GET | `/health` | 200 + `{"ok":true}` | — |
| 其它 | 任意 | — | 405（handler 兜底） |

约束：

- 所有 `/v1/*` 需 `Authorization: Bearer <AUTH_TOKEN>`；`/health` 不校验，供探活用；
- `key` 只允许 `[a-z0-9_-]{1,32}`，其余 400（**这是安全边界**：防目录穿越，
  `../evil`、空串、超长、含 `/` 全部拒绝）；
- 路由解析规则：`path` 为去掉 query 的路径。`/v1/<group>/<key>` 按**第三个斜杠**拆，
  前半截归一到 `path`（`/v1/blob`），后半截作 `key`；`/v1/keys` 与 `/health` 无 `key`；
- key 校验与 `If-Match` 校验都在**进 handler 之前**由运行时做完（见 §5.4），handler 只看得到
  已经合法的 `key`、以及已经通过版本检查的写请求；
- `PUT` 顺序固定：**快照 → 原子写 → 调度版本化提交**，提交在响应发出后执行；
- `PUT` body 上限 8 MiB（`DP_MAX_BODY` 可调），header 总量上限 8 KiB，单行 4 KiB，
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
item         := record_type | sum_type | fn_decl | entry_decl
              | host_decl | env_decl | boundary_decl

record_type  := 'type' IDENT '{' field (',' field)* ','? '}'
sum_type     := 'type' IDENT '=' variant ('|' variant)*
field        := IDENT ':' type
variant      := IDENT ('{' field (',' field)* '}')?

fn_decl      := 'fn' IDENT '(' params? ')' '->' type intent* block
params       := param (',' param)*
param        := IDENT ':' type
intent       := 'requires' expr
              | 'invariant' STRING
              | 'example' expr
              | 'effects' '{' IDENT (',' IDENT)* '}'
              | 'cap' 'mem' '<' INT IDENT '>'
              | 'idempotent' 'by' IDENT

entry_decl    := 'entry' IDENT '{' fn_decl+ '}'
host_decl     := 'host' IDENT '{' host_fn+ '}'
host_fn       := fn_decl_head                      // 无函数体，实现由运行时提供
env_decl      := 'env' IDENT '{' env_field+ '}'     // 只到语法层，D0 不实现（§5.4）
env_field     := IDENT ':' type ('=' literal)?
boundary_decl := 'boundary' IDENT '@' 'v' INT '{' bnd_method+ '}'
bnd_method    := IDENT '(' params? ')' '->' type ('with' 'idempotency')? intent*

fn_decl_head := 'fn' IDENT '(' params? ')' '->' type intent*

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
              | IDENT '{' (IDENT ':' expr) (',' IDENT ':' expr)* '}'   // 构造
              | IDENT '(' (expr (',' expr)*)? ')'                      // 调用
              | primary '.' IDENT                                      // 字段
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
| 词法 / 语法错误 | E0 | 带行号列号 |

**D0 不检查**：效果集内容、`cap` 上界、`idempotent` 一致性、`requires` 静态可判性（只解析）。
这些位置在生成的 `contract.json` 里保留，供 M3 启用。

### 3.5 tag 编号约定（决定 `!` 的实现）

全局错误 tag 从 100 起顺序分配，成功 tag 固定 0：

```
0        成功值
1..99    record / sum 的普通变体（按声明顺序）
100+     进入过 `!` 传播的错误变体（NotFound=100, BadRequest=101, IoError=102, Conflict=103）
```

规则：**同一个变体名在所有联合里共享同一 tag**。因此 `!` 只需复制 tag，不做映射表。

## 4. 三个样例（必须全绿）

### 4.1 `code/lang/examples/hello.dlt`

```droplet
// 最小闭环：验证 lexer + parser + checker + codegen + cc 五层都能跑通
// 判据：droplet build examples/hello.dlt && ./hello 打印 world
host Sys {
  fn log(msg: str) -> Unit
}

entry Demo {
  fn greet(sys: Sys) -> Unit
    invariant "greet 向标准输出写一行 world"
  {
    sys.log("world")
    return Unit
  }
}
```

### 4.2 `code/webapi/src/contract.dlt`

```droplet
// 通用哑存储 Web API 契约：服务端只做字节读写，不解构 payload
type ETag { hash: u64 }

type ApiError = NotFound | BadRequest | IoError | Conflict

type Request {
  method: str,
  path: str,
  key: str,
  body: bytes,
  if_match: str
}

type Response {
  status: u64,
  body: bytes,
  etag: str
}

host BlobFs {
  fn read(root: str, key: str) -> bytes | ApiError
  fn write(root: str, key: str, body: bytes) -> ETag | ApiError
  fn backup(root: str, key: str) -> Unit
  fn create(root: str, key: str) -> bool | ApiError
  fn list_json(root: str) -> bytes | ApiError
}

host Versioning {
  fn commit_soon(root: str, key: str) -> Unit
}

host Sys {
  fn now_unix() -> i64
  fn hash64(data: bytes) -> u64
  fn u64_str(x: u64) -> str
  fn log(msg: str) -> Unit
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
entry Serve {
  fn handle(req: Request, fs: BlobFs, ver: Versioning, sys: Sys, root: str)
      -> Response | ApiError
    invariant "非 GET/PUT 的请求一律 405"
  {
    if req.path == "/health" {
      return Response{status: 200, body: b"{\"ok\":true}", etag: ""}
    }
    if req.path == "/v1/keys" {
      if req.method == "GET" {
        let body = fs.list_json(root)!
        return Response{status: 200, body: body, etag: ""}
      }
      if req.method == "POST" {
        fs.create(root, req.key)!
        return Response{status: 200, body: b"", etag: ""}
      }
    }
    if req.path == "/v1/blob" {
      if req.method == "GET" {
        let body = fs.read(root, req.key)!
        let tag = sys.u64_str(sys.hash64(body))
        return Response{status: 200, body: body, etag: tag}
      }
      if req.method == "PUT" {
        fs.backup(root, req.key)
        let etag = fs.write(root, req.key, req.body)!
        ver.commit_soon(root)
        return Response{status: 200, body: b"", etag: sys.u64_str(etag.hash)}
      }
    }
    return Response{status: 405, body: b"", etag: ""}
  }
}
```

> `handle` 的 `!` 把 `ApiError` 抛给运行时；运行时不认识业务语义，只按 tag 映射
> 404 / 400 / 409 / 500。**这是「错误是值」在边界处的唯一转换点。**

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
  out->etag = _r.u.ok.m_etag;
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

服务形态的返回必须是 `Response | 错误` 且 `Response` 带 `status` 字段，否则报 `E-GEN`——
运行时只认 `status`，不猜。

### 5.4 语言子集与生成期的已知限制

这些限制都**显式报错**（带行列号），不允许静默降级：

| 限制 | 现状 | 补齐时机 |
| --- | --- | --- |
| `env` 声明 | 语法能解析，checker 报 `E1`「不在 D0 子集」；配置现走环境变量（§6），`contract` 里的 env 段恒为空 | D1 按 SPEC §5.7 实现 |
| `match` 匹配不到有意义的变体 | 语法与 codegen 都对（分支之间必须有逗号），但 D0 没有构造 sum 值的途径：裸变体名被 `E1` 拒、`{...}` 只收 record，因此只能匹配到零值 | D1 补变体构造语法 |
| 带字段的错误变体 | 报 `E-GEN`；错误只保留 tag | 确有需要时再扩 |
| record 字段按值嵌入 | 被引用类型必须在源文件里先出现；循环引用不支持 | D1 再考虑指针化 |
| host 能力白名单 | `Sys` / `BlobFs` / `Versioning` 共 10 个能力已全部接通；表外的能力逐个报 `E-GEN` 并列出位置 | 无（新增能力时扩表） |
| 无 `entry` 的程序 | 生成立即返回 0 的 `main`，并引用全部函数避免 `-Wunused-function` | 无需补 |
| `If-Match` / 412 归运行时 | checker 不接受裸变体名（`return Conflict` 报 `E1`），handler 表达不了“条件失败”，412 由运行时在进 handler 前判定 | 语言能表达条件失败时，换成错误变体参与 |
| 提交是 leading-edge 防抖 | 窗口内的后续写不单独提交，要等下一次写才带上；若进程之后再无写入，最后一次写只落盘、不进历史 | 要“每次写都进历史”就把 `DP_COMMIT_DEBOUNCE` 设 0 |
| `contract` 导出的意图 | 只导出文字（`invariant` / `cap` / `idempotent`）与 `effects` 的标识符；`requires` / `example` 的表达式不导出（没有表达式打印器） | 需要时补一个 `droplet fmt` 的前置能力 |

## 6. C 运行时 `code/lang/internal/runtime/assets/`

> 下面与 `rt.h` 逐行一致，两边漂移时以 `rt.h` 为准并同步回本文。
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

/* ---- arena：按块分配，整块释放 ---- */
typedef struct dp_arena dp_arena;
dp_arena* dp_arena_new(size_t cap);
void      dp_arena_reset(dp_arena* a);
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

typedef struct {
  uint16_t status;
  dp_str   content_type;
  dp_bytes body;
  dp_str   etag;
} dp_response;

typedef struct {
  uint16_t port;
  dp_str   bind;       /* 监听地址，默认 "0.0.0.0" */
  dp_str   root;
  dp_str   token;      /* 空串表示不校验 Authorization */
  size_t   max_body;
} dp_cfg;

/* 返回 0 表示已填 out；100+ 表示错误 tag，由运行时映射状态码 */
typedef int (*dp_handler)(dp_arena* a, dp_str root, const dp_request* rq, dp_response* out);

dp_cfg dp_cfg_from_env(void);
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
2. **状态码映射**：`100→404`、`101→400`、`103→409`、其余错误 `500`；找不到路由由 handler
   自己返回 405。
3. **原子写**：写 `.tmp.<pid>.<seq>` → `fsync` → `rename`。
4. **快照**：`<root>/.snapshots/<key>.<unix>.<seq>`，写前先备一份；每个 key 只留最近
   `DP_SNAPSHOT_KEEP` 份（默认 8，0 = 不轮转），按 `(unix, seq)` 从旧到新删。带 `seq`
   是因为秒级时间戳单独用会让同一秒内的多次写互相覆盖，轮转也就无从谈起。
5. **版本化钩子**：防抖窗口内只起一个子进程；只把 `$DP_COMMIT_KEY` 这一个路径交给 git
   （扫全树会撞上正在 `rename` 的临时文件）；父进程不 `wait`，子进程由 `SIGCHLD=SIG_IGN`
   自动收割。防抖是 **leading edge**：窗口内的后续写不会单独提交，等下一次写触发。
6. **配置全部走环境变量**：`DP_BIND`（默认 `0.0.0.0`）/ `DP_PORT`（8787）/ `DP_ROOT`
   （`./data`）/ `DP_TOKEN`（空 = 不校验）/ `DP_MAX_BODY`（8 MiB）/ `DP_COMMIT_CMD`
   （空 = 不提交）/ `DP_COMMIT_DEBOUNCE`（60）/ `DP_SNAPSHOT_KEEP`（8）。
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

### 7.2 目录结构（Go）

```
droplet-lang/code/lang/
  go.mod                         # module droplet
  cmd/droplet/main.go            # 动词表：parse / check / build / contract
  internal/token/token.go        # 词法单元与源码位置
  internal/lex/lex.go
  internal/ast/ast.go
  internal/parse/parse.go
  internal/check/check.go        # 两遍式检查，导出类型/Tag/Sigs 供 codegen 复用
  internal/dump/dump.go          # AST → JSON（M0 的验收出口）
  internal/gen/c.go              # AST → C11
  internal/gen/http_test.go      # 端到端：编出服务、拉进程、发真请求
  internal/contract/doc.go       # AST + 检查结果 → 契约 JSON
  internal/runtime/embed.go      # go:embed 打包运行时
  internal/runtime/assets/       # rt.h + 四个 .c（包目录里不能放 .c，所以放子目录）
    rt.h  rt_core.c  rt_http.c  rt_blob.c  rt_ver.c
  examples/hello.dlt
droplet-lang/code/webapi/
  src/{contract.dlt,main.dlt}
  deploy/{blobd.service,blobd.env.example,accept.sh,soak.sh}
```

构建：`go build ./cmd/droplet`。运行时由 `go:embed` 收进编译器，`droplet build` 把它释放到
临时目录后交给 `cc -std=c11 -O2 -Wall -Wextra` 直编（是否 `-static` 由部署方决定）。
当前 Web API 的构建与拉起（`DP_TOKEN` 空时跳过鉴权）：

```sh
droplet build code/webapi/src/contract.dlt code/webapi/src/main.dlt -o blobd
DP_BIND=127.0.0.1 DP_PORT=8787 DP_ROOT=./data DP_TOKEN=secret ./blobd
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

- 对外只暴露反代：`DP_BIND=127.0.0.1`，行级访问控制交给 nginx / caddy；
- `DP_TOKEN` 必填，否则 `/v1/*` 对所有人开放（`/health` 不需要）；
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
| M4 | 示例服务部署 + 契约验收 | 五接口 + 412 + 快照 + 一次版本化提交全通；连续运行 3 天无异常 | 2–3 天 | **进行中** 2026-10-30（12 项实况验收全通；长稳脚本 `soak.sh` 就位，45 s 高频替代跑 602 轮 0 失败；快照已改轮转 + 带 `seq`；§8.1 审计完成；**连续 3 天长稳未跑**） |

M3 的内部拆分（按风险排序，实际按此执行）：

1. ✅ `dp_serve` 请求循环 + `Content-Length` + header 上限（**最大的安全风险，先做**）；
2. ✅ `dp_blob_*` + `dp_valid_key`（含 `../evil`、空串、超长三类用例）；
3. ✅ `dp_sys_hash64` + `dp_sys_u64_str`（ETag）；
4. ✅ `dp_ver_commit_soon`：M3 只落空实现（不影响主链路），M4 补上防抖 + `fork/exec`，
   移到 `rt_ver.c`。

M3 除了运行时，还交了两件不在原判据里但契约先行需要的东西：

- `droplet contract` 动词：把 boundary / env / 函数签名 / tag 表导成 JSON，供消费方生成客户端；
- `internal/gen/http_test.go`：把手工 `curl` 验证固化成能重跑的端到端测试（编译 → 拉进程 →
  发真请求 → 断言状态码与 body）。

M4 的契约验收（`sh code/webapi/deploy/accept.sh`，`DP_BIND=127.0.0.1 DP_TOKEN=secret
DP_COMMIT_DEBOUNCE=0`，数据目录是临时 git 仓库）：

| 请求 | 结果 |
| --- | --- |
| `GET /health`（无 token） | 200 |
| `GET /v1/keys`（空） | 200 `[]` |
| `PUT /v1/blob/demo` | 200 + `ETag` |
| `PUT` + 正确 `If-Match` | 200 |
| `PUT` + 错 `If-Match` | 412（未进 handler，所以既无快照也无提交） |
| `POST /v1/keys/second` | 200 + 空 body |
| `GET /v1/keys` | 200 `["demo","second"]` |
| `GET /v1/blob/missing` | 404 |
| `GET /v1/blob/../evil` | 400 |
| `DELETE /v1/blob/demo` | 405（handler 兜底） |
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

### 8.1 M4 收尾审计（附录判据的执行结果）

附录那条「仍从未被 `.dlt` 用到的特性说明它该删」在 M4 末尾逐个跑了一遍：

| 项 | 实测 | 结论 |
| --- | --- | --- |
| `env` 声明 | `check` 报 0 错、`build` 也成功，配置声明被**静默丢弃** | **已修为 `E1`**，`TestEnvDeclRejected` 固定 |
| `match` | 能 check、能 build、能跑（分支之间必须有逗号） | 保留；但 D0 无构造 sum 值的途径，只能匹配零值 → 记入 §5.4 |
| `Sys.getenv` | 能力表 / `rt.h` / `rt_core.c` / `contract.dlt` 四处齐全，**零调用点** | **已删**：`dp_sys_getenv` 23 行 + 能力项 + 契约声明 + `EnvError` |
| `Sys.now_unix` | `.dlt` 无人调用，但 `rt_blob.c` 的快照名在用 | 保留；`rt.h` 注释改为「仓内唯一调用点是快照文件名的时间戳」 |
| `true`/`false`、`!=` `<` `<=` `>` `>=` `&&` `||` `+` `-` `*` `/` `%` | 均可达，三个 `.dlt` 都没用到 | 属例子覆盖不足，不是死代码；不为它们硬凑样例 |

删完后的能力表是 10 个（`Sys` 4 + `BlobFs` 5 + `Versioning` 1）。

## 9. 风险与回退

| 风险 | 触发信号 | 回退动作 |
| --- | --- | --- |
| 手写 HTTP 出安全/正确性问题 | keep-alive 下响应错位、header 注入 | 关闭 keep-alive（`Connection: close`），退回每请求一连接 |
| 提交子进程与原子写并发 | git 扫到正在 rename 的临时文件，`fatal: unable to stat`，整次提交失败 | 只 add `$DP_COMMIT_KEY` 一个路径，不扫全树（已采纳） |
| `!` 的 tag 对齐复杂化 | codegen 出现映射表而非复制 | 简化：禁止跨函数传播，`!` 只在 entry 内使用 |
| RSS 远超 3 MB | M3 基线 > 8 MB | 记录实际值，按 SPEC §9 回砍（先砍 keep-alive 缓冲与日志） |
| 快照无限累积 | 按 `soak.sh` 默认口径跑 72 h，`.snapshots/` 会累积约 8640 个文件（每 30 s 一次写） | 已采纳：每 key 只留最近 `DP_SNAPSHOT_KEEP`（默认 8）；要全量历史就设 0 |
| 语言子集被需求拉动膨胀 | 出现「再加一个语法就能写」的请求 | 记入 `docs/D0-DEVIATIONS.md`，M4 之后再议 |
| C 后端迟迟跑不通 | M2 超过 2 周 | 暂停语言进度，先用既有实现顶着（可用性优先） |

M3 实测的 RSS：空服务（无请求）1.83 MB，符合 SPEC §9 的 H4（≤ 3 MB）；8 MB body 的 `PUT`
峰值 9.98 MB。后者是“接了 8 MB payload”的瞬时值，不是 H4 的口径，但说明 arena 是按 body
上限预留的：真要把这个数字压下去，得把 arena 改成按需增长。

## 10. 与消费方的接入约定

1. **服务只暴露 HTTP 契约**，消费方不需要知道 Droplet 存在，也不需要装任何 Droplet 工具；
2. 消费方只配置两项：`base URL` 与 `AUTH_TOKEN`；
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
