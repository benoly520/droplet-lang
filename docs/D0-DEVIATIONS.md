# D0 偏差登记

> **SPEC.md 是设计，这里记的是已实现的东西。** 两边不一致时，本表给出「SPEC 怎么说 / D0
> 实际怎样 / 为什么 / 什么时候补」。
>
> IMPL-PLAN 附录要求「每一条不做都有原因与补齐时机，不允许静默省略」，这张表就是那份清单。
> 判定口径：一律以 `code/lang` 与 `code/webapi/src/*.dlt` 的**实际行为**为准，不以文档里的
> 意图为准。每一条都可以用第 4 节的命令复核。
>
> D0 = M0–M4，2026-10-30 收尾。

## 1. 语言

| 项 | SPEC | D0 实际 | 原因 | 补齐 |
| --- | --- | --- | --- | --- |
| `env` 声明 | §5.7 一等特性；缺键即编译失败 | 语法能解析，checker 报 `E1`「不在 D0 子集」 | 配置由运行时 `dp_cfg_from_env` 从环境变量读，`.dlt` 侧没有消费方 | D1 |
| sum 值构造 | 判别联合 + 穷尽 `match`（§5.2） | 能声明、能 `match`，但**无法构造值**：裸变体名被 `E1` 拒，`{...}` 只收 record | 唯一需要 sum 的地方是错误联合，而错误走 `!` 直接抛给运行时 | D1 补变体构造 |
| 关键字 | 14 个（§14.2） | 10 个；缺 `move` `end` `task` `for` | 无区域、无任务、无循环；handler 只有 `if` 链 | 随区域/任务模型 |
| `else` | 不在 14 个关键字里 | 上下文关键字 | 可写 `if/else` 比 `match bool` 好读，且已验证不占关键字预算 | D1 复评 |
| intent 校验 | §5.10：`E10` 抓不到 mutation 即硬错；`E11` 无 intent 不编译 | 只做 `E11`；`requires`/`example`/`effects`/`cap` 能解析并导出，**不校验** | mutation testing 要独立验证器 | D1 |
| opaque secret | §5.7 密钥不可打印/不可进日志 | 没有这个类型 | 鉴权只有一个静态 token，运行时字符串比较 | D1 |

## 2. 运行时与边界

| 项 | SPEC | D0 实际 | 原因 | 补齐 |
| --- | --- | --- | --- | --- |
| 412 条件失败 | 错误是值（C1） | 运行时在**进 handler 之前**判定 | checker 不接受裸变体名，handler 表达不了“条件失败” | 语言能构造 sum 值后改回错误变体 |
| keep-alive | 未限定 | 不做；每请求一连接，`Connection: close` | 手写 HTTP 最容易在这里响应错位；也省内存 | 不做 |
| chunked | 未限定 | 拒绝，501 | 防请求走私；body 上限 8 MiB 够用 | 不做 |
| 并发 | 未限定 | 单线程；读写各 30 s 超时防单连接钉死 | H4 内存预算 | 需要时多进程 + `SO_REUSEPORT` |
| host 能力 | 开放的 effect 系统 | 写死 10 个（`Sys` 4 / `BlobFs` 5 / `Versioning` 1），表外报 `E-GEN` | 只实现示例服务真正用到的 | 新增能力时扩表 |
| 带字段的错误变体 | 支持 | 报 `E-GEN`，错误只保留 tag | 运行时只按 tag 映射状态码 | 确有需要时 |
| boundary codegen | §7.4 生成 stub / handler / serializer | 只导出 JSON 契约 | 消费方手写客户端就够（§10） | D1 |
| 存储 | schema 驱动查询 | 扁平文件 + `.snapshots` | 哑存储是刻意的最小面 | D2 |
| 快照 | 未限定 | 每 key 保留最近 8 份，文件名带 `seq` | 不带 `seq` 同秒写会互相覆盖；不轮转 72 h 堆约 8640 个文件 | 已完成 |
| 传输安全 | 未限定 | 无 TLS，只有静态 `Bearer` token | 部署形态要求反代终结 TLS（§7.3） | 不做 |

## 3. 工具链

| 项 | SPEC | D0 实际 | 原因 | 补齐 |
| --- | --- | --- | --- | --- |
| 动词 | `check` / `mem` / `bench` / `explore` / `trace` / `verify` | `parse` / `check` / `build` / `contract` | 让服务跑起来所需的最小子集 | D1 |
| 契约里的 intent | 用来合成可执行 oracle | 只导出文字（`invariant`/`cap`/`idempotent`）与 `effects` 标识符；`requires`/`example` 的表达式不导出 | 没有表达式打印器 | 先补 `droplet fmt` |
| 错误码 | `E0`–`E12` | `E1` / `E3` / `E11` + `E00xx` 语法号 | 没实现的检查不报假错误 | 随特性 |
| 增量编译 | 未限定 | 每次全量重编 | 样例只有 91 行 | 不做 |

## 4. 已不算偏差

- **编译器用 Go 写**：SPEC 不规定实现语言；选型与理由在 IMPL-PLAN §7.1。
- **产物是 C11 二进制**：SPEC 允许；`droplet build -print-c` 能打印全文，`-keep-c` 能保留
  `<out>.c` 便于对照。

## 5. 怎么复核这张表

```sh
B=.build/droplet
(cd code/lang && go build -o ../../.build/droplet ./cmd/droplet)

echo 'env App { port: u64 }' > /dev/null        # 任意含 env 的 .dlt
$B check your_env.dlt                            # → E1「不在 D0 子集」
$B contract code/webapi/src/contract.dlt code/webapi/src/main.dlt | head -40
$B build -print-c code/lang/examples/hello.dlt   # 看生成的 C
grep -c 'hostCaps' code/lang/internal/gen/c.go   # 能力表在这里，共 10 条
sh code/webapi/deploy/accept.sh                  # 12 项边界行为
```
