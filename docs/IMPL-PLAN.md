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
| GET | `/v1/keys` | 200 + `{"ok":true,"keys":[{"key":"...","exists":true}]}` | 500 |
| POST | `/v1/keys` | 200 + `{"ok":true,"created":bool}` | 400 |
| GET | `/health` | 200 + `{"ok":true}` | — |

约束：

- 所有 `/v1/*` 需 `Authorization: Bearer <AUTH_TOKEN>`，静态资源不需要；
- `key` 只允许 `[a-z0-9_-]{1,32}`，其余 400（**这是安全边界**：防目录穿越，
  `../evil`、空串、超长、含 `/` 全部拒绝）；
- 路由解析规则：`path` 为去掉 query 的完整路径，`key` 为 `/v1/blob/` 之后的最后一段
  （不含 query）；`/v1/keys` 与 `/health` 无 `key`；
- `PUT` 顺序固定：**快照 → 原子写 → 调度版本化提交**，提交在响应之后异步执行；
- `PUT` body 上限 8 MiB，header 总量上限 32 KiB，读超时 30 s；
- 每请求一个 arena，响应发送完 `dp_arena_reset`。

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
env_decl      := 'env' IDENT '{' env_field+ '}'
env_field     := IDENT ':' type ('=' literal)?
boundary_decl := 'boundary' IDENT '@' 'v' INT '{' bnd_method+ '}'
bnd_method    := IDENT '(' params? ')' '->' type ('with' 'idempotency')? intent*

fn_decl_head := 'fn' IDENT '(' params? ')' '->' type intent*

block        := '{' stmt* '}'
stmt         := let_stmt | return_stmt | if_stmt | match_stmt | expr_stmt
let_stmt     := 'let' IDENT (':' type)? '=' expr
return_stmt  := 'return' expr?
if_stmt      := 'if' expr block ('else' (block | if_stmt))?
match_stmt   := 'match' expr '{' arm+ '}'
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
// 最小闭环：验证 parser + checker + codegen 三件套
host Sys {
  fn log(msg: str) -> Unit
}

entry Demo {
  fn greet(name: str) -> Unit
    example greet("world") == Unit
  {
    log(name)
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
  fn commit_soon(root: str) -> Unit
}

host Sys {
  fn env(name: str) -> str | NotFound
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
| record `T` | `typedef struct dp_T { ... } dp_T;` |
| sum `T` | `typedef struct dp_T { uint8_t tag; union { ... } u; } dp_T;` |
| 返回 `A \| E1 \| E2` | `dp_R_<fn> { uint8_t tag; union { A a; E1 e1; E2 e2; } u; }` |
| `fn f(...)` | `static dp_R_<f> dp_f(dp_arena* a, <params>)` |
| `let x = e` | 局部变量 + 立即求值（无惰性） |
| `match` | `switch (v.tag)`，`_` 编译为 `default:`（checker 已保证穷尽） |
| `!` | `if (r.tag != 0) { dp_R_out o = {0}; o.tag = r.tag; return o; }` |
| `b"..."` | 静态 `static const uint8_t k[] = {...}` |
| `"..."` | 静态 `dp_str` 常量 |

字符串比较 `==` 生成 `dp_str_eq`，**禁止** `strcmp`（非 NUL 结尾）。

### 5.3 生成代码的骨架

```c
/* 编译单元：每个 .dlt 程序一个 .c，符号前缀 dp_ */
#include "rt.h"

static dp_response dp_serve_handle(dp_arena* a, const dp_request* rq, const dp_cfg* cfg);
int main(int argc, char** argv) {
  return dp_serve(&dp_serve_handle, cfg_from_env());
}
```

`entry` 的函数被改名为 `dp_serve_handle`，签名由运行时逆向构造：
`Request` 由 `dp_request` 平铺，`root` 由配置注入，`Host` 参数消失（Host 是编译期能力集合，
调用点直接生成 `dp_blob_read(...)`）。**这是 `host` 的实现方式：不传值，直接内联调用。**

## 6. C 运行时 `code/lang/runtime/rt.h`

```c
typedef struct { const char* p; size_t n; } dp_str;
typedef struct { const uint8_t* p; size_t n; } dp_bytes;
typedef struct dp_arena dp_arena;

/* ---- arena：每请求一个，reset 即释放 ---- */
dp_arena* dp_arena_new(size_t cap);
void      dp_arena_reset(dp_arena* a);
void*     dp_alloc(dp_arena* a, size_t n);
void      dp_arena_free(dp_arena* a);

/* ---- HTTP ---- */
typedef struct {
  dp_str   method, path, key, query, authorization, if_match;
  dp_bytes body;
} dp_request;

typedef struct {
  uint16_t status;
  dp_str   content_type;
  dp_str   etag;
  dp_bytes body;
} dp_response;

typedef dp_response (*dp_handler)(dp_arena*, const dp_request*, const dp_cfg*);
int dp_serve(uint16_t port, dp_handler h, const dp_cfg* cfg);   /* 阻塞，单线程 */

/* ---- 哑存储能力（对应 host BlobFs） ---- */
int  dp_blob_read(dp_arena*, const char* root, dp_str key, dp_bytes* out);
int  dp_blob_write(dp_arena*, const char* root, dp_str key, dp_bytes body, uint64_t* hash);
int  dp_blob_backup(dp_arena*, const char* root, dp_str key);
int  dp_blob_create(dp_arena*, const char* root, dp_str key, bool* created);
int  dp_blob_list_json(dp_arena*, const char* root, dp_bytes* out);

/* ---- 版本化钩子（对应 host Versioning） ---- */
void dp_ver_commit_soon(const char* root);

/* ---- 系统能力（对应 host Sys） ---- */
int      dp_sys_env(dp_arena*, dp_str name, dp_str* out);
int64_t  dp_sys_now_unix(void);
uint64_t dp_sys_hash64(dp_bytes body);
dp_str   dp_sys_u64_str(dp_arena*, uint64_t x);
void     dp_sys_log(dp_str msg);

/* ---- 校验（安全边界，不可丢） ---- */
bool   dp_valid_key(dp_str key);           /* ^[a-z0-9_-]{1,32}$ */
bool   dp_starts_with(dp_str s, const char* prefix);
dp_str dp_query_param(dp_str query, const char* key);
```

实现要点：

1. **HTTP/1.1 最小集**：只处理 `Content-Length`，不支持 `chunked`（`fetch` 不会发）；
   `Connection: keep-alive` 每连接最多 100 个请求；header 上限 32 KiB；body 上限 8 MiB。
2. **原子写**：`open(tmp, O_CREAT|O_TRUNC)` → `write` → `fsync` → `rename` → `fsync(dir)`。
3. **快照**：`snapshots/<key>/<hash8>.bin`，最多保留 20 份，超出删最旧（`readdir` 排序）。
4. **版本化钩子**：默认实现用 `posix_spawn` 跑外部命令（如
   `sh -c 'git add -A data && git commit -m ... && git push'`），60 s 防抖，**不阻塞响应**；
   不使用版本控制时该能力可编译为空实现。
5. **RSS 预算**：arena 初始 1 MiB，按 2 倍增长到 8 MiB 上限；`.bss` 静态缓冲 256 KiB；
   M3 结束时用 `/usr/bin/time -v` 记录基线，不达标就按 SPEC §9 回砍机制。

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
  go.mod                       # module droplet
  cmd/droplet/main.go          # 动词表：parse / check / build / contract
  internal/token/token.go      # 词法单元与源码位置
  internal/lex/lex.go
  internal/ast/ast.go
  internal/parse/parse.go
  internal/dump/dump.go        # AST → JSON（M0 的验收出口）
  runtime/rt.h  runtime/rt.c   # go:embed 打包
  examples/hello.dlt
droplet-lang/code/webapi/
  src/{contract.dlt,main.dlt}
  Makefile                     # droplet build src/{contract,main}.dlt → cc -O2 -static
```

构建：`go build ./cmd/droplet`；运行时始终是一个 `rt.c` + 一个 `rt.h`，
由 `cc -O2 -static` 直编。

## 8. 里程碑

| # | 交付 | 通过判据（不过不进下一步） | 预估 |
| --- | --- | --- | --- |
| M0 | 骨架 + lexer + parser + AST dump | 三个样例 `.dlt` 全部 parse 成功；`./droplet parse` 输出合法 JSON | 3–4 天 |
| M1 | checker（E0/E1/E3/E11）+ `check` | 三个样例 0 错误；8 个反例各自命中预期 E 码且不级联 | 4–6 天 |
| M2 | codegen + `hello.dlt` 跑通 | `./droplet build examples/hello.dlt && ./hello` 打印 `world` | 5–8 天 |
| M3 | `rt.c`（HTTP + blob + hash + spawn）+ `main.dlt` 编译 | `curl` 五个接口全通；412 / 404 / 400 / 413 各自命中；RSS 有基线数字 | 1.5–2 周 |
| M4 | 示例服务部署 + 契约验收 | 五接口 + 412 + 快照 + 一次版本化提交全通；连续运行 3 天无异常 | 2–3 天 |

M3 的内部拆分（按风险排序，先做最危险的部分）：

1. `dp_serve` 请求循环 + `Content-Length` + header 上限（**最大的安全风险，先做**）；
2. `dp_blob_*` + `dp_valid_key`（含 `../evil`、空串、超长三类用例）；
3. `dp_sys_hash64` + `dp_sys_u64_str`（ETag）；
4. `dp_ver_commit_soon`（`posix_spawn` + 防抖，最后做，因为最不影响主链路）。

## 9. 风险与回退

| 风险 | 触发信号 | 回退动作 |
| --- | --- | --- |
| 手写 HTTP 出安全/正确性问题 | keep-alive 下响应错位、header 注入 | 关闭 keep-alive（`Connection: close`），退回每请求一连接 |
| `!` 的 tag 对齐复杂化 | codegen 出现映射表而非复制 | 简化：禁止跨函数传播，`!` 只在 entry 内使用 |
| RSS 远超 3 MB | M3 基线 > 8 MB | 记录实际值，按 SPEC §9 回砍（先砍 keep-alive 缓冲与日志） |
| 语言子集被需求拉动膨胀 | 出现「再加一个语法就能写」的请求 | 记入 `docs/D0-DEVIATIONS.md`，M4 之后再议 |
| C 后端迟迟跑不通 | M2 超过 2 周 | 暂停语言进度，先用既有实现顶着（可用性优先） |

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
- 若 M4 之后仍有 `code/lang` 里从未被 `.dlt` 用到的特性，说明它该删；
- 编译器实现语言已定 Go，M0 开工后冻结，D0 期间不换。
