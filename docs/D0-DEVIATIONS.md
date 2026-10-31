# D0 偏差登记

> **SPEC.md 是设计，这里记的是已实现的东西。** 两边不一致时，本表给出「SPEC 怎么说 / D0
> 实际怎样 / 为什么 / 什么时候补」。
>
> IMPL-PLAN 附录要求「每一条不做都有原因与补齐时机，不允许静默省略」，这张表就是那份清单。
> 判定口径：一律以 `code/lang` 与 `code/webapi/src/*.dlt` 的**实际行为**为准，不以文档里的
> 意图为准。每一条都能用第 5 节的命令复核。
>
> D0 = M0–M4。最后核对于 2026-10-30。

## 1. 语言

| 项 | SPEC | D0 实际 | 原因 | 补齐 |
| --- | --- | --- | --- | --- |
| `env` 声明 | §5.7 一等特性；缺键即编译失败 | **已完成（v0.11，`8fdde01`）**：`env` 块注册为带 `IsEnv` 的 record，服务入口必须声明且补齐五键；源码不得构造 env 值；前缀由块名推出（`App` → `APP`）；生成器加载器在缺键/非法值时启动即失败；`contract` 导出 `envs` | 曾因「`.dlt` 侧没有消费方」被拒收，`env` 落地后消费方就是 handler 的 `cfg` 参数 | 已完成 |
| 关键字 | 14 个（§14.2） | 10 个；缺 `move` `end` `task` `for` | 无区域、无任务、无循环；handler 只有 `if` 链 | 随区域/任务模型 |
| `else` | 不在 14 个关键字里 | 上下文关键字，`if/else` 可编译可运行 | 比 `match bool` 好读，且实测不占关键字预算 | D1 复评 |
| intent 校验 | §5.10：`E10` 抓不到 mutation 即硬错；`E11` 无 intent 不编译 | 只做 `E11` + **`E10` 的静态片段（v0.16）**：意图表达式必须 bool、参数可见、常量恒真/恒假即报、调用点实参为常量时必须满足 `requires`；`requires`/`example` 的表达式自 v0.15 起以规范形进契约（`intents[].expr`）；证据面由工具层 **`droplet verify`（v0.17）** 提供：封闭的 example 被编进专用二进制逐条求值，零可执行样例算未通过 | §7.8.1 的机械变异检验要独立验证器（判"意图对变异零捕获"），且按 §7.8.3-4 的纪律不进编译器；变异已接四类（v0.18：常量 +1、边界翻转；v0.19：清空 `then` / `match` 分支体、删掉守卫块的 `return`），**另两类（效果注入、写入删除）不做** —— 它们要观测能力调用与写入真的发生过，而 verify 没有那条通道（服务形态、能力调用、开放样例一律不跑），要做等于把工具变成服务运行器；「提前返回」的方向也与 §7.8.1 原文相反（删而非插），已在 SPEC §7.8.1 记账 | D1（变异检验） |
| 候选收敛（§7.8.2） | 两两差分：用随机/边界输入找出外部可观测的行为差异，给出反例对 `输入 X → A 得 Y，B 得 Z`，交人补约束 | 只做**退化版（v0.21）**：`droplet explore` 把候选各自声明的**封闭 example** 当输入集逐条比对，输出分歧、意图集不一致与存活候选集；零存活时给出"谁被哪条样例否掉" | **输入生成没做**：按签名造 u64 / str / bytes / record / sum 的随机与边界值、并把参数化表达式编进驱动，约再 +120 行，H8 余量（v0.21 后 25 行）装不下。所以"在没声明的输入上有没有分歧"仍靠人判 —— 这正好是欠定的主要来源 | P5（预算复议后） |
| opaque secret | §5.7 密钥不可打印、不可进日志 | 没有这个类型 | 鉴权只有一个静态 token，运行时做字符串比较 | D1 |

## 2. 运行时与边界

| 项 | SPEC | D0 实际 | 原因 | 补齐 |
| --- | --- | --- | --- | --- |
| 412 条件失败 | 错误是值（C1） | **已合流（D3 / v0.13）**：`PreconditionFailed` 变体与 412 映射（D2）+ 路由上的 `with precondition`。`rt_http.c` 里的 `is_put` 与 22 行 If-Match 比对下沉成 `dp_precondition_check`（运行时唯一实现），gen 在调用 impl 之前插唯一触发点；写路由沉默不表态是 `E1` | 预条件的正确性只在一个边界事实上（上一版成功写入的版本），不在 handler 的调用点：交还给人就是「忘一次 = 静默丢更新」 | 已完成；`accept.sh` 新增「412 empty body (bytes) = 0」，`TestPreconditionGuardIsSynthesized` 锁守卫位置 |
| `ETag` 由谁书写 | IMPL-PLAN §2 曾写「PUT 成功返回新 `ETag`」 | v0.10 起 ETag 由**运行时**按 `dp_sys_u64_str(dp_sys_hash64(body))` 从响应 body 推导：非空 body 就有头，空 body 就没有。`PUT` 的成功响应因此不带 tag（要新版本号请 `GET`），`/health` 与 `/v1/keys` 则**开始**带 tag | 同一份表示只能有一个版本号（SPEC §2.2 H7 第四条减法）；`If-Match` 的比对本来就在运行时，两处必须用同一个函数才对得上 —— 对不上就是静默的并发 bug | 已完成（行为变化，客户端视角见 IMPL-PLAN §2） |
| keep-alive | 未限定 | 不做；每请求一连接，`Connection: close` | 手写 HTTP 最容易在这里响应错位；也省内存 | 不做 |
| chunked | 未限定 | 拒绝，回 501 | 防请求走私；body 上限 8 MiB 够用 | 不做 |
| 并发 | 未限定 | 单线程；读写各 30 s 超时（只靠 `SO_RCVTIMEO`/`SO_SNDTIMEO`），超时回 408 | H4 内存预算；超时防一条连接钉死服务 | 需要时多进程 + `SO_REUSEPORT` |
| host 能力 | 开放 effect 系统 | 写死 10 个（`Sys` 4 / `BlobFs` 5 / `Versioning` 1）；表外能力、参数个数、参数与返回类型不符在 `check` 就报 `E2`，表在 `internal/host`（check 与 gen 共用，含 ABI 类型与 `u64 → ETag` 的包装） | 只实现示例服务真正用到的 | 新增能力时扩表 + `rt.h` 加原型 |
| boundary codegen | §7.4 生成 stub / handler / serializer | **D3 起 gen 读 `boundary`**：有路由就生成 dispatcher（`serviceMain`），并按 `impl` 绑定实现、按 `precondition` 插守卫；但仍只导出 JSON 契约，不生成客户端 stub / serializer | 服务端需要「路由是事实」；客户端有 `contract` JSON 手写就够（§10），生成 stub 属于下一层消费面 | 客户端 stub 未排期 |
| 契约与真实 HTTP 面不一致 | 「一次声明同时产出对外契约」 | **已消除（D3 / v0.13）**：`boundary` 现在声明全部 5 条路由（`@verb("/path")` + `impl`），有 `impl` 无路由 / 有路由无 `impl` / 契约签名与实现不一致都是 `E1`；JSON 契约同时导出 `verb` / `path` / `impl` / `precondition` | 声明落后于实现在 D3 之后无法通过编译 —— 这正是「契约可被验证」的前提 | 已完成（`TestWebapiPasses` 断言 5 条路由；`doc_test.go` 断言导出） |
| 存储 | schema 驱动查询 | 扁平文件 + `.snapshots/`；`APP_ROOT` 必须是已存在的目录，否则启动时直接退出 | 哑存储是刻意的最小面；缺目录不能让服务「起得来、写全 500」 | **未排期**：schema 查询超出最小服务面，D3 也不含（D2 结的是响应面那笔账，与本行无关） |
| 快照 | 未限定 | `<key>.<unix>.<seq>`，每 key 只留最近 `DP_SNAPSHOT_KEEP` 份（默认 8，0 = 不轮转） | 不带 `seq` 时同秒两次写会互相覆盖；不轮转 72 h 堆约 8640 个文件 | 已完成 |
| 传输安全 | 未限定 | 无 TLS，只有静态 `Bearer` token | 部署形态要求反代终结 TLS（IMPL-PLAN §7.3） | 不做 |

**成功码现状（v0.14 / D3-B3 之后）**：默认 200（`Unit` 成功 = 200 + `Content-Length: 0`），
写路由可以用 `@created`（201）与 `@no_content`（204）投影别的码 —— 数字不进源码，
源码里写的是"产出了新东西"或"没有表示"。`@no_content` 只能投在 `Unit` 成功类型上，
读路由写这两个注解报 `E1`。**206（Partial Content）仍不支持**：它需要 Range 解析与
分段表示，属于另一件事，明确不在范围。另外，tag 派生的错误响应一律**空 body**
（405 与 412 因此都不带文本），要人类可读的错误文案需要新的错误表示（未排期）。

## 3. 工具链

| 项 | SPEC | D0 实际 | 原因 | 补齐 |
| --- | --- | --- | --- | --- |
| 动词 | `check` / `mem` / `bench` / `explore` / `trace` / `verify` | `parse` / `check` / `build` / `contract`（+ `help`） | 让服务跑起来所需的最小子集 | D1 |
| 错误码 | `E0`–`E12` | `E1`（类型与匹配）、`E2`（能力与签名）、`E3`（边界序列化）、`E11` + 语法号 `E0003`–`E0009` + 生成期 `E-GEN` | 没实现的检查不报假错误；生成期只剩结构形状类报错 | 随特性 |
| 增量编译 | 未限定 | 每次全量重编 | 样例只有 95 行（`contract.dlt` 64 + `main.dlt` 31） | 不做 |
| 成功码 | 未限定（HTTP 面） | **已完成（v0.14）**：`@created` / `@no_content` 投影 201 / 204，默认 200；契约 JSON 导出 `success`（200 省略） | 以前只能恒 200（D2 删 `Response` 的代价）；恢复表达力的方式是声明上的注解，不是把状态码写回 handler | 已完成；206 不在范围 |
| 契约 JSON | 含 `envs` 分组 | **已完成（v0.11）**：导出 `envs`（键名 / 类型 / 变量名 / 必填） | 曾因 checker 拒绝 env 而删掉该字段，`env` 落地后随之一并回来（`doc_test.go` 有断言） | 已完成 |

## 4. 一条跨阶段的老毛病：`check` 的保证比 `build` 弱

### 4.1 已修：五类构造在 `check` 阶段就拦下

审计时撞到四次同一个形状 —— **`check` 说 ok，`build` 才报错**（D1 补上第五类）：

| 构造 | 修之前的 `check` | 修之前的 `build` | 现在 |
| --- | --- | --- | --- |
| `sys.nope(1)`（表外能力） | ok | `E-GEN: 运行时不提供能力 Sys.nope` | `check` 报 `E2` |
| `host Sys { fn log(u64) }` + `sys.log(1)` | ok | cc 报 `incompatible type for argument 1 of 'dp_sys_log'`（无 E 码、无行号） | `check` 报 `E2` |
| `type Bad = Err { code: u64 }` | ok | `E-GEN: M2 不支持带字段的…变体` | `check` 报 `E1`（D1 起已支持，该行保留作历史；见 §5） |
| `env App { port: u64 }` | ok | ok，配置被**静默丢弃** | `check` 报 `E1` |
| `entry f() -> Unit`（只写签名不写函数体） | ok | cc 报 `undefined reference to 'dp_f_f'`（无 E 码、无行号） | parse 报 `E0006: entry f 必须有函数体` |

原因只有一个：能力表以前长在 `internal/gen/c.go`，checker 看不见它；而表里也只记了参数
**个数**，类型完全以 `.dlt` 里那份 `host` 声明为准，声明谎报也照单全收。

修法：能力表搬到 [`internal/host/host.go`](../code/lang/internal/host/host.go)，一份表同时
描述「参数与返回的运行时 ABI 类型」与「怎么生成 C 调用」，check 与 gen 都从它取。于是：

- 表外能力、参数个数不符、参数或返回类型与运行时原型不符 → `E2`（效果与能力，SPEC §14 的
  码意义）；参数写成 record 也是 `E2`（record 传不进 C 原型）；
- 带字段的变体 → 曾经 `E1`（类型与匹配），D1 变体构造落地后不再是偏差；
- `internal/host/host_test.go` 锁住两件事：能力表恰好 10 项；每一项生成的 `dp_*` 调用都能在
  `rt.h` 找到原型。

还有一个同类但更阴的形状：**多个 `entry`**。以前 `check` 照样 ok，而 `EntryPoint` 取的是 map
迭代顺序里遇到的任意一个 —— 同一份源码连跑 6 次，入口在 `f` 与 `g` 之间跳过。现在报 `E1`。

**结论**：`droplet check` 的 `ok` 现在等价于「能构建」—— **只对能力表这一类成立**。

还有一类曾留在 gen 里没收进来：**服务形态的响应字段形状**。三种写法（`Response` 缺 `status`、
写了 `etag`、写了未知字段）以前 `check` 都说 ok、`build` 才报 `E-GEN`，根因是「响应面」是
`rt.h` 里那个 struct 的形状，不是 checker 的概念。**D2 已从结构上消除这一类**：`Response`
这个类型不存在了，成功类型只能是 `bytes` 或 `Unit`，`check` 直接报 `E1` 并给出改写提示
（§6 的 m 探针）。于是 §4.1 的五类构造加上这一类，`check` 的 ok 与 build 的分歧面只剩「生成器
自身的 bug」。同批把「未知响应字段被**静默丢弃**」一起消灭：没有响应记录，就没有可多写的字段。

### 4.2 顺带收掉的影子表：`u64 → ETag` 的包装

`BlobFs.write` 的运行时原型返回 `uint64_t`，语言侧是 record `ETag`。包装代码以前在
`internal/gen/c.go` 里另立一张 `hostWraps` 表，还带一个「按类型名判断要不要包」的猜测。

现在它是能力的一项数据：`Cap.RetName`（语言侧成功类型名，空则同 `Ret`）与 `Cap.Wrap`
（`nil` 表示直接赋值）。checker 用 `RetName` 比对返回类型，gen 只负责 `if hc.Wrap != nil`。
两条信息住在一起，不会再出现「check 认的类型和 gen 包的类型不是同一个」的漂移。

## 5. 已不算偏差

- **sum 值构造**（D1 落地）：变体用限定名构造 —— 零字段写 `T.V`，带载荷写 `T.V { f: v }`；
  `match` 能绑定字段（`Square { side } => …`）。裸变体名仍然报 `E1: 未定义标识符`，
  这是刻意的：构造点必须看得见类型，不靠「期望类型反推」。
  配套硬规则：**变体名跨整个程序必须唯一**（tag 按名字全局分配，同名会让两个 sum 撞 tag），
  在声明处报 `E1: 变体名 X 已被 A.X 使用`。
- **带字段的变体**（D1 落地）：`type Bad = Err { code: u64 }` 能声明、能构造、能 `match`；
  运行时错误映射仍只看 tag，字段留在语言侧（要按字段映射状态码时再动 `rt_http.c`）。
- **`entry` 的写法**（D1 落地）：入口直接写签名 —— `entry handle(req: Request) -> bytes | ApiError`，
  不再有 `entry Name { fn … }` 那层壳。外层名字从不承载内容（不能鉴权、不带超时预算，一个程序也只能
  有一个入口），所以删位置而不是删写法：**旧形状在 parse 就报 `E0006` 并给改写提示**。
  壳没了，`EntryDecl` 这个 AST 节点也没了（入口 = 带 `Entry` 标记的 `FnDecl`，四处 switch 各少一个分支）；
  将来的鉴权 / 超时 / 重试预算写成签名上的 intent，不回头把壳加回来。
- **路由写在 `boundary` 上**（D3 落地，v0.13）：`@get("/v1/blob") impl blob_get with idempotency`。
  动词是 check 期校验的小写标识符，不是关键字；`precondition` / `without` 是上下文关键字。
  服务形态的入口因此是路由表（有路由就没有 `entry`，两者并存报 `E1`），`env` 也只服务这个形态。
- **`Response` 这个类型**（D2 落地，v0.12）：v0.10 先删了它的 `etag` 字段，D2 把整个记录删了 ——
  成功即值：入口成功类型只能是 `bytes`（有体）或 `Unit`（无体），状态码只在「错误变体 → 状态码」
  的表里出现。写 `Response{status: 200, body: X}` 现在在 `check` 就报 `E1` 并提示「它在语言里就是 `X`」。
  裸 `return` 表示该返回类型的单位值（`Unit` → `()`，`bytes` → 空 body）。`BlobFs.write` 与
  `boundary put` 仍然返回 `ETag`（存储层的事实，运行时 ABI 未动），`main.dlt` 用 `!` 丢弃它的返回值。
- **编译器用 Go 写**：SPEC 不规定实现语言；选型与理由在 IMPL-PLAN §7.1。
- **产物是 C11 二进制**：SPEC 允许；`-print-c` 打印全文，`-keep-c` 保留 `<out>.c` 便于对照。

## 6. 怎么复核这张表

```sh
B=.build/droplet
(cd code/lang && go build -o ../../.build/droplet ./cmd/droplet)

# env 被拒：期望 E1，exit=1
printf 'env App {\n  port: u64\n}\n\nentry f() -> Unit\n  invariant "x"\n{\n}\n' > .build/e.dlt
$B check .build/e.dlt

# 裸变体名构造不出 sum 值：期望 E1「未定义标识符 NotFound」
printf 'type ApiError = NotFound | Bad\n\nentry f() -> u64 | ApiError\n  invariant "x"\n{\n  return NotFound\n}\n' > .build/b.dlt
$B check .build/b.dlt

# 表外能力：check 就报 E2（以前要拖到 build 才报 E-GEN）
printf 'host Sys {\n  fn nope(u64) -> Unit\n}\n\nentry f(sys: Sys) -> Unit\n  invariant "x"\n{\n  sys.nope(1)\n}\n' > .build/c.dlt
$B check .build/c.dlt

# 带字段的变体：D1 起是合法构造，期望 ok
printf 'type Bad = Err { code: u64 }\n\nentry f() -> u64 | Bad\n  invariant "x"\n{\n  return Bad.Err { code: 1 }\n}\n' > .build/f.dlt
$B check .build/f.dlt

# 带载荷的变体裸写：期望 E1「要写成 `Bad.Err { … }`」
printf 'type Bad = Err { code: u64 }\n\nentry f() -> Bad\n  invariant "x"\n{\n  return Bad.Err\n}\n' > .build/g.dlt
$B check .build/g.dlt

# 同名变体跨 sum：期望 E1「变体名 X 已被 A.X 使用」
printf 'type A = X\ntype B = X\n\nentry f() -> A\n  invariant "x"\n{\n  return A.X\n}\n' > .build/h.dlt
$B check .build/h.dlt

# entry 套壳的旧写法：期望 E0006 并给改写提示（以前这层壳合法，里面那个名字从不承载内容）
printf 'entry Demo {\n  fn f() -> Unit\n  invariant "x"\n  {\n  }\n}\n' > .build/i.dlt
$B check .build/i.dlt

# 两个入口：期望 E1「入口只能有一个」（以前静默按 map 迭代顺序挑一个当 EntryPoint）
printf 'entry a() -> Unit\n  invariant "x"\n{\n}\n\nentry b() -> Unit\n  invariant "y"\n{\n}\n' > .build/j.dlt
$B check .build/j.dlt

# entry 只写签名不写函数体：期望 E0006「必须有函数体」（以前 check ok、到 cc 才 undefined reference）
printf 'entry a() -> Unit\n  invariant "x"\n' > .build/k.dlt
$B check .build/k.dlt

# Response 形状的服务入口：D2 起 check 就报 E1（以前 check ok、build 才报 E-GEN，见 §4.1）
printf 'env App {\n  port: u64 = 1,\n  bind: str = "127.0.0.1",\n  root: str = "/tmp",\n  token: str = "",\n  max_body: u64 = 1024\n}\n\ntype ApiError = NotFound\n\ntype Request { method: str }\n\ntype Response {\n  status: u64,\n  body: bytes\n}\n\nentry handle(req: Request, cfg: App) -> Response | ApiError\n  invariant "x"\n{\n  return Response{status: 405, body: b""}\n}\n' > .build/m.dlt
$B check .build/m.dlt   # 期望 1 条 E1：成功类型必须是 bytes 或 Unit（并提示「你写的 `Response{status: 200, body: X}` 在本语言里就是 `X`」）

# 裸 return 只对 Unit / bytes 合法（R3）：str 上仍是 E1
printf 'fn f() -> str\n{\n  return\n}\n' > .build/n.dlt
$B check .build/n.dlt   # 期望 E1: 函数 f 返回 str，不能空 return

# 声明谎报参数类型：check 直接报 E2（以前要到 cc 才炸，无 E 码无行号）
printf 'host Sys {\n  fn log(u64) -> Unit\n}\n\nentry f(sys: Sys) -> Unit\n  invariant "x"\n{\n  sys.log(1)\n}\n' > .build/l.dlt
$B check .build/l.dlt   # 期望 E2: 能力 Sys.log 的第 1 个参数运行时是 str，声明为 u64

# 契约与生成物
$B contract code/webapi/src/contract.dlt code/webapi/src/main.dlt | head -40
$B build -print-c code/lang/examples/hello.dlt | head -5

# 能力表在这里，共 10 条
sed -n '/^var Caps = map/,/^}/p' code/lang/internal/host/host.go | grep -cE '^[[:space:]]+"[A-Za-z]+\.[a-z0-9_]+": \{'

# 数据目录缺失 → 启动即退出（不是起得来再 500）
$B build code/webapi/src/contract.dlt code/webapi/src/main.dlt -o .build/q >/dev/null
APP_ROOT=.build/nope .build/q ; echo "exit=$?"   # 期望打印原因 + exit=1

# 写路由不表态预条件：期望 E1「必须显式表态」（以前靠运行时那段 If-Match，漏写无人拦）
printf 'env App {\n  port: u64 = 1,\n  bind: str = "127.0.0.1",\n  root: str = "/tmp",\n  token: str = "",\n  max_body: u64 = 1024\n}\n\ntype ApiError = NotFound\n\nboundary Api @v1 {\n  put(key: str, body: bytes) -> Unit @put("/v1/x") impl h with idempotency\n    invariant "x"\n}\n\nfn h(key: str, body: bytes, cfg: App) -> Unit | ApiError {\n  return\n}\n' > .build/p.dlt
$B check .build/p.dlt   # 期望 E1: 写路由 put 必须显式表态预条件

# 路由没写 impl：期望 E1「却没有实现」
printf 'boundary Api @v1 {\n  get(key: str) -> bytes @get("/v1/x")\n    invariant "x"\n}\n' > .build/o.dlt
$B check .build/o.dlt   # 期望 E1「却没有实现」（另有一条 env 缺失：路由让它成了服务形态）

# @no_content 投在有 body 的路由上：期望 E1「204 不能有 body」
printf 'env App {\n  port: u64 = 1,\n  bind: str = "127.0.0.1",\n  root: str = "/tmp",\n  token: str = "",\n  max_body: u64 = 1024\n}\n\ntype ApiError = NotFound\n\nboundary Api @v1 {\n  put(key: str, body: bytes) -> bytes @put("/v1/x") @no_content impl h with idempotency, precondition\n    invariant "x"\n}\n\nfn h(key: str, body: bytes, cfg: App) -> bytes | ApiError {\n  return b""\n}\n' > .build/r.dlt
$B check .build/r.dlt

# 成功码写进契约：期望恰好两条 "success"（put 204、create 201；其余方法省略，默认 200）
$B contract code/webapi/src/contract.dlt code/webapi/src/main.dlt | grep -c '"success"'   # 期望 2

# 17 项边界行为（含 412 / 412 空 body / 204 无 Content-Length / 201 / 405 / 413 / 501，
# 以及 v0.10 起「405 的空 body 不带 ETag」）
sh code/webapi/deploy/accept.sh
```
