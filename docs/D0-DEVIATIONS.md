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
| `env` 声明 | §5.7 一等特性；缺键即编译失败 | 能解析；checker 报 `E1`「不在 D0 子集」，`check`/`build`/`contract` 三个动词一律不复 | 配置由运行时 `dp_cfg_from_env` 读环境变量，`.dlt` 侧没有消费方 | D1 |
| sum 值构造 | 判别联合 + 穷尽 `match`（§5.2） | 能声明、能 `match`；**无法构造值**：裸变体名报 `E1: 未定义标识符 NotFound`，`{...}` 只收 record | 唯一需要 sum 的地方是错误联合，而错误走 `!` 直接抛给运行时，不进 `match` | D1 补变体构造 |
| 关键字 | 14 个（§14.2） | 10 个；缺 `move` `end` `task` `for` | 无区域、无任务、无循环；handler 只有 `if` 链 | 随区域/任务模型 |
| `else` | 不在 14 个关键字里 | 上下文关键字，`if/else` 可编译可运行 | 比 `match bool` 好读，且实测不占关键字预算 | D1 复评 |
| intent 校验 | §5.10：`E10` 抓不到 mutation 即硬错；`E11` 无 intent 不编译 | 只做 `E11`。`requires`/`example`/`effects`/`cap` 能解析并导出，**不校验**；表达式不导出（`exprOf` 只认字面量） | mutation testing 要独立验证器；也没有表达式打印器 | D1（先补 `fmt`） |
| opaque secret | §5.7 密钥不可打印、不可进日志 | 没有这个类型 | 鉴权只有一个静态 token，运行时做字符串比较 | D1 |

## 2. 运行时与边界

| 项 | SPEC | D0 实际 | 原因 | 补齐 |
| --- | --- | --- | --- | --- |
| 412 条件失败 | 错误是值（C1） | 运行时在**进 handler 之前**判定 | checker 不接受裸变体名，handler 表达不了「条件失败」 | 语言能构造 sum 值后改回错误变体 |
| keep-alive | 未限定 | 不做；每请求一连接，`Connection: close` | 手写 HTTP 最容易在这里响应错位；也省内存 | 不做 |
| chunked | 未限定 | 拒绝，回 501 | 防请求走私；body 上限 8 MiB 够用 | 不做 |
| 并发 | 未限定 | 单线程；读写各 30 s 超时（只靠 `SO_RCVTIMEO`/`SO_SNDTIMEO`），超时回 408 | H4 内存预算；超时防一条连接钉死服务 | 需要时多进程 + `SO_REUSEPORT` |
| host 能力 | 开放 effect 系统 | 写死 10 个（`Sys` 4 / `BlobFs` 5 / `Versioning` 1）；表外能力、参数个数、参数与返回类型不符在 `check` 就报 `E2`，表在 `internal/host`（check 与 gen 共用，含 ABI 类型与 `u64 → ETag` 的包装） | 只实现示例服务真正用到的 | 新增能力时扩表 + `rt.h` 加原型 |
| 带字段的变体 | 支持 | 语法是 `Err { code: u64 }`，`check` 报 `E1`（错误只按 tag 映射，字段无处安放） | 运行时只按 tag 映射状态码 | 确有需要时 |
| boundary codegen | §7.4 生成 stub / handler / serializer | 只导出 JSON 契约（`droplet contract`） | 消费方手写客户端就够（§10） | D1 |
| 存储 | schema 驱动查询 | 扁平文件 + `.snapshots/`；`DP_ROOT` 必须是已存在的目录，否则启动时直接退出 | 哑存储是刻意的最小面；缺目录不能让服务「起得来、写全 500」 | D2 |
| 快照 | 未限定 | `<key>.<unix>.<seq>`，每 key 只留最近 `DP_SNAPSHOT_KEEP` 份（默认 8，0 = 不轮转） | 不带 `seq` 时同秒两次写会互相覆盖；不轮转 72 h 堆约 8640 个文件 | 已完成 |
| 传输安全 | 未限定 | 无 TLS，只有静态 `Bearer` token | 部署形态要求反代终结 TLS（IMPL-PLAN §7.3） | 不做 |

## 3. 工具链

| 项 | SPEC | D0 实际 | 原因 | 补齐 |
| --- | --- | --- | --- | --- |
| 动词 | `check` / `mem` / `bench` / `explore` / `trace` / `verify` | `parse` / `check` / `build` / `contract`（+ `help`） | 让服务跑起来所需的最小子集 | D1 |
| 错误码 | `E0`–`E12` | `E1`（类型与匹配）、`E2`（能力与签名）、`E3`（边界序列化）、`E11` + 语法号 `E0003`–`E0009` + 生成期 `E-GEN` | 没实现的检查不报假错误；生成期只剩结构形状类报错 | 随特性 |
| 增量编译 | 未限定 | 每次全量重编 | 样例只有 91 行（`contract.dlt` 53 + `main.dlt` 38） | 不做 |
| 契约 JSON | 含 `envs` 分组 | 已删该字段 | checker 拒绝 env → `contract` 走不到导出那一步，字段永远为空 | 随 env 一起回来 |

## 4. 一条跨阶段的老毛病：`check` 的保证比 `build` 弱

### 4.1 已修：四类构造在 `check` 阶段就拦下

审计时撞到四次同一个形状 —— **`check` 说 ok，`build` 才报错**：

| 构造 | 修之前的 `check` | 修之前的 `build` | 现在 |
| --- | --- | --- | --- |
| `sys.nope(1)`（表外能力） | ok | `E-GEN: 运行时不提供能力 Sys.nope` | `check` 报 `E2` |
| `host Sys { fn log(u64) }` + `sys.log(1)` | ok | cc 报 `incompatible type for argument 1 of 'dp_sys_log'`（无 E 码、无行号） | `check` 报 `E2` |
| `type Bad = Err { code: u64 }` | ok | `E-GEN: M2 不支持带字段的…变体` | `check` 报 `E1` |
| `env App { port: u64 }` | ok | ok，配置被**静默丢弃** | `check` 报 `E1` |

原因只有一个：能力表以前长在 `internal/gen/c.go`，checker 看不见它；而表里也只记了参数
**个数**，类型完全以 `.dlt` 里那份 `host` 声明为准，声明谎报也照单全收。

修法：能力表搬到 [`internal/host/host.go`](../code/lang/internal/host/host.go)，一份表同时
描述「参数与返回的运行时 ABI 类型」与「怎么生成 C 调用」，check 与 gen 都从它取。于是：

- 表外能力、参数个数不符、参数或返回类型与运行时原型不符 → `E2`（效果与能力，SPEC §14 的
  码意义）；参数写成 record 也是 `E2`（record 传不进 C 原型）；
- 带字段的变体 → `E1`（类型与匹配）；
- `internal/host/host_test.go` 锁住两件事：能力表恰好 10 项；每一项生成的 `dp_*` 调用都能在
  `rt.h` 找到原型。

**结论**：`droplet check` 的 `ok` 现在等价于「能构建」。gen 里那两处检查降为防御性兜底。

### 4.2 顺带收掉的影子表：`u64 → ETag` 的包装

`BlobFs.write` 的运行时原型返回 `uint64_t`，语言侧是 record `ETag`。包装代码以前在
`internal/gen/c.go` 里另立一张 `hostWraps` 表，还带一个「按类型名判断要不要包」的猜测。

现在它是能力的一项数据：`Cap.RetName`（语言侧成功类型名，空则同 `Ret`）与 `Cap.Wrap`
（`nil` 表示直接赋值）。checker 用 `RetName` 比对返回类型，gen 只负责 `if hc.Wrap != nil`。
两条信息住在一起，不会再出现「check 认的类型和 gen 包的类型不是同一个」的漂移。

## 5. 已不算偏差

- **编译器用 Go 写**：SPEC 不规定实现语言；选型与理由在 IMPL-PLAN §7.1。
- **产物是 C11 二进制**：SPEC 允许；`-print-c` 打印全文，`-keep-c` 保留 `<out>.c` 便于对照。

## 6. 怎么复核这张表

```sh
B=.build/droplet
(cd code/lang && go build -o ../../.build/droplet ./cmd/droplet)

# env 被拒：期望 E1，exit=1
printf 'env App {\n  port: u64\n}\n\nentry E {\n  fn f() -> Unit\n  invariant "x"\n  {\n  }\n}\n' > .build/e.dlt
$B check .build/e.dlt

# 裸变体名构造不出 sum 值：期望 E1「未定义标识符 NotFound」
printf 'type ApiError = NotFound | Bad\n\nentry E {\n  fn f() -> u64 | ApiError\n  invariant "x"\n  {\n    return NotFound\n  }\n}\n' > .build/b.dlt
$B check .build/b.dlt

# 表外能力：check 就报 E2（以前要拖到 build 才报 E-GEN）
printf 'host Sys {\n  fn nope(u64) -> Unit\n}\n\nentry E {\n  fn f(sys: Sys) -> Unit\n  invariant "x"\n  {\n    sys.nope(1)\n  }\n}\n' > .build/c.dlt
$B check .build/c.dlt

# 带字段的变体：同样在 check 报 E1
printf 'type Bad = Err { code: u64 }\n\nentry E {\n  fn f() -> u64 | Bad\n  invariant "x"\n  {\n    return 1\n  }\n}\n' > .build/f.dlt
$B check .build/f.dlt

# 声明谎报参数类型：check 直接报 E2（以前要到 cc 才炸，无 E 码无行号）
printf 'host Sys {\n  fn log(u64) -> Unit\n}\n\nentry E {\n  fn f(sys: Sys) -> Unit\n  invariant "x"\n  {\n    sys.log(1)\n  }\n}\n' > .build/l.dlt
$B check .build/l.dlt   # 期望 E2: 能力 Sys.log 的第 1 个参数运行时是 str，声明为 u64

# 契约与生成物
$B contract code/webapi/src/contract.dlt code/webapi/src/main.dlt | head -40
$B build -print-c code/lang/examples/hello.dlt | head -5

# 能力表在这里，共 10 条
sed -n '/^var Caps = map/,/^}/p' code/lang/internal/host/host.go | grep -cE '^[[:space:]]+"[A-Za-z]+\.[a-z0-9_]+": \{'

# 数据目录缺失 → 启动即退出（不是起得来再 500）
$B build code/webapi/src/contract.dlt code/webapi/src/main.dlt -o .build/q >/dev/null
DP_ROOT=.build/nope .build/q ; echo "exit=$?"   # 期望打印原因 + exit=1

# 12 项边界行为（含 412 / 405 / 413 / 501）
sh code/webapi/deploy/accept.sh
```
