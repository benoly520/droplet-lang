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
| intent 校验 | §5.10：`E10` 抓不到 mutation 即硬错；`E11` 无 intent 不编译 | 只做 `E11`。`requires`/`example`/`effects`/`cap` 能解析并导出，**不校验**；表达式不导出（`exprOf` 只认字面量） | mutation testing 要独立验证器；也没有表达式打印器 | D1（先补 `fmt`） |
| opaque secret | §5.7 密钥不可打印、不可进日志 | 没有这个类型 | 鉴权只有一个静态 token，运行时做字符串比较 | D1 |

## 2. 运行时与边界

| 项 | SPEC | D0 实际 | 原因 | 补齐 |
| --- | --- | --- | --- | --- |
| 412 条件失败 | 错误是值（C1） | **D2 已补一半（v0.12）**：新增 `PreconditionFailed` 变体与 412 映射，handler 现在能表达**自己的**条件失败；HTTP 层的 `If-Match` 比对仍在运行时、进 handler 之前 | 语言侧障碍（构造不出错误值）已消失，但这段比对是**安全边界**（与「非法 key 不进 handler」同级）—— 搬到 handler 后忘一次就是静默丢更新。语言侧的正解是 `boundary … with idempotency` 合成，不是交还给人 | **D3 合流**：由 boundary 合成后删掉运行时比对，届时同一语义只剩一处；须重跑长稳 |
| `ETag` 由谁书写 | IMPL-PLAN §2 曾写「PUT 成功返回新 `ETag`」 | v0.10 起 ETag 由**运行时**按 `dp_sys_u64_str(dp_sys_hash64(body))` 从响应 body 推导：非空 body 就有头，空 body 就没有。`PUT` 的成功响应因此不带 tag（要新版本号请 `GET`），`/health` 与 `/v1/keys` 则**开始**带 tag | 同一份表示只能有一个版本号（SPEC §2.2 H7 第四条减法）；`If-Match` 的比对本来就在运行时，两处必须用同一个函数才对得上 —— 对不上就是静默的并发 bug | 已完成（行为变化，客户端视角见 IMPL-PLAN §2） |
| keep-alive | 未限定 | 不做；每请求一连接，`Connection: close` | 手写 HTTP 最容易在这里响应错位；也省内存 | 不做 |
| chunked | 未限定 | 拒绝，回 501 | 防请求走私；body 上限 8 MiB 够用 | 不做 |
| 并发 | 未限定 | 单线程；读写各 30 s 超时（只靠 `SO_RCVTIMEO`/`SO_SNDTIMEO`），超时回 408 | H4 内存预算；超时防一条连接钉死服务 | 需要时多进程 + `SO_REUSEPORT` |
| host 能力 | 开放 effect 系统 | 写死 10 个（`Sys` 4 / `BlobFs` 5 / `Versioning` 1）；表外能力、参数个数、参数与返回类型不符在 `check` 就报 `E2`，表在 `internal/host`（check 与 gen 共用，含 ABI 类型与 `u64 → ETag` 的包装） | 只实现示例服务真正用到的 | 新增能力时扩表 + `rt.h` 加原型 |
| boundary codegen | §7.4 生成 stub / handler / serializer | 只导出 JSON 契约（`droplet contract`）—— **gen 完全不读 `boundary`** | 消费方手写客户端就够（§10）；而路由事实源还不在语言侧（见下一行） | **D3**：方案见 IMPL-PLAN §8.5（待点头） |
| 契约与真实 HTTP 面不一致 | 「一次声明同时产出对外契约」 | `boundary` 只声明 `get` / `put` / `list` 三条，示例服务实际有 **5 条路由**：`/health` 与 `POST /v1/keys` 在契约里不存在，导出的 JSON 因此是残缺事实（`accept.sh` 第 1、7 项打的就是这两条） | D0 没把路由从 `boundary` 合成，路由是 handler 里的字符串比较 —— 声明因此可以落后于实现且无人拦 | **D3 B1/B2**（§8.5.1 是证据表）；合流前不假装契约可信 |
| 存储 | schema 驱动查询 | 扁平文件 + `.snapshots/`；`APP_ROOT` 必须是已存在的目录，否则启动时直接退出 | 哑存储是刻意的最小面；缺目录不能让服务「起得来、写全 500」 | **未排期**：schema 查询超出最小服务面，D3 也不含（D2 结的是响应面那笔账，与本行无关） |
| 快照 | 未限定 | `<key>.<unix>.<seq>`，每 key 只留最近 `DP_SNAPSHOT_KEEP` 份（默认 8，0 = 不轮转） | 不带 `seq` 时同秒两次写会互相覆盖；不轮转 72 h 堆约 8640 个文件 | 已完成 |
| 传输安全 | 未限定 | 无 TLS，只有静态 `Bearer` token | 部署形态要求反代终结 TLS（IMPL-PLAN §7.3） | 不做 |

**D2 起（v0.12）生效的偏差**：`Response` 类型被删掉之后，成功响应恒为 200
（`Unit` 成功 = 200 + `Content-Length: 0`），**201 / 204 / 206 等暂时无法表达**。
以前 `Response{status: 201}` 是能写的。补齐：D3 由 `boundary` 的成功形状合成状态码
（与错误变体共用同一张 tag → status 表）。另外，tag 派生的错误响应一律**空 body**
（405 因此不带文本），要人类可读的错误文案也等 D3 的错误表示。

## 3. 工具链

| 项 | SPEC | D0 实际 | 原因 | 补齐 |
| --- | --- | --- | --- | --- |
| 动词 | `check` / `mem` / `bench` / `explore` / `trace` / `verify` | `parse` / `check` / `build` / `contract`（+ `help`） | 让服务跑起来所需的最小子集 | D1 |
| 错误码 | `E0`–`E12` | `E1`（类型与匹配）、`E2`（能力与签名）、`E3`（边界序列化）、`E11` + 语法号 `E0003`–`E0009` + 生成期 `E-GEN` | 没实现的检查不报假错误；生成期只剩结构形状类报错 | 随特性 |
| 增量编译 | 未限定 | 每次全量重编 | 样例只有 88 行（`contract.dlt` 51 + `main.dlt` 37） | 不做 |
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

# 13 项边界行为（含 412 / 405 / 413 / 501，以及 v0.10 起「405 的空 body 不带 ETag」）
sh code/webapi/deploy/accept.sh
```
