# NEXT：交接与待办

> 这份文档只回答一个问题：**换一台电脑、隔一段时间回来，怎么最快接上。**
> 它只记「现在在哪儿 + 还没做什么 + 怎么算做完」。语言设计看 SPEC，实施细节看 IMPL-PLAN，
> 实现与设计的有意偏差看 D0-DEVIATIONS。
>
> **接手的 AI 先做两件事**：跑一遍 §1 的命令确认环境；读完 §2 与 §3 再动任何代码。
> SPEC 里的八条硬约束是人拍板的，不要为了“让测试过”去改它们。

## 0. 现在的状态（截至 2026-10-30）

- `main` = `a594bc3`，**已推送**，没有未推送的提交。
- D0 完成：58 个 Go 测试全绿、`accept.sh` 12 项全通、README Quick start 可原样执行。
- **工作区有 5 个未提交文件**：`docs/SPEC.md`、`docs/IMPL-PLAN.md`、`docs/NEXT.md`、`README.md`、`README_CN.md`
  （本轮新增的 H7 / H8、两份基线、本文与两处指针）。换机器前必须提交，否则这几轮的设计结论带不走。

## 1. 新机器五分钟上手

```sh
go version                                    # 需 ≥ 1.24（code/lang/go.mod 声明的最低版本）
mkdir -p .build                               # 编译产物放这里，目录不会自动建
cd code/lang && go build -o ../../.build/droplet ./cmd/droplet
go test ./...                                 # 期望全部包 ok（共 58 个用例）
```

还需要 `cc`（gcc 或 clang，编译器以 `-std=c11` 调用它）。

clone 后确认基线：`git log -1 --oneline` 应当等于本文件 §0 写的那条提交，或它的后继。

**沙盒坑**：如果 `/tmp` 不可写（本机就是这样），上面三条命令之前还要加

```sh
export TMPDIR=$PWD/.build GOCACHE=$TMPDIR/gocache
```

否则 `go build` / `go test` 会在创建临时目录时失败。

**一条命令自检**（不跑测试，只确认链路通）：

```sh
.build/droplet check code/webapi/src/contract.dlt code/webapi/src/main.dlt
# 实测输出：ok: 9 项声明，1 个函数，入口 handle
# （EntryPoint 报的是 entry 里的函数名，不是 entry 名 Serve）
```

## 2. 待办

### P0 · 提交当前改动

- 内容：SPEC v0.5（H7 重复书写预算）/ v0.6（H8 实现规模预算）；IMPL-PLAN §8.2 / §8.3 两份基线；本文；两处 README 指针
  （具体是哪几个文件以 `git status --short` 为准）
- 判据：`git status --short` 为空
- 提交信息用 `type(scope): 指令式摘要`，例：`docs(spec): 补 H7 重复书写与 H8 实现规模两条硬约束`

### P0 · 3 天长稳（唯一需要「等」的事）

- 为什么一直没做：需要一台常开的机器
- 命令：`sh code/webapi/deploy/soak.sh 72`
- 判据：退出码 0；`.build/soak/fail.log` **为 0 字节**（脚本会预建这个空文件，它存在不代表失败）；RSS 斜率 ≤ 64 KB/h
- 想先压缩时间自测：`SOAK_SECONDS=60 sh code/webapi/deploy/soak.sh`（60 秒 = 2 轮，实测退出码 0、1 线程、无残留子进程）

### P1 · 拆 `check.go`（H8 唯一真违反项）

- 现状：**1188 行**，H8 上限 800（SPEC §2.2）
- 拆法：同包 7 个文件，见 [IMPL-PLAN §8.3](IMPL-PLAN.md)（逻辑零改动，不拆包）
- 判据：
  - `code/lang/internal/check/` 下每个**非测试** `.go` 文件 ≤ 250 行（`check_test.go` 本身就是 355 行，不受此限）
  - 58 个用例 + `accept.sh` 12 项全通
  - 拆分前后 `droplet build -print-c` 的输出逐字相同（这是「零改动」的硬证据）

### P2 · 两条语法减法（**需要点头**）

- `host` 声明的参数名零信息：`fn log(msg: str) -> Unit` → `fn log(str) -> Unit`
- 末尾 `return Unit` 必写：改为 Unit 函数**不写** `return`
- 为什么不直接做：会改语法、三份样例要跟着改，属于 break change
- 判据：旧写法报错且带改写提示；样例改完后 58 用例 + accept 全通

### P3 · D1 设计项（**需要点头**）

- **变体构造**：现在裸变体名被 `E1` 拒、`{...}` 只收 record，`match` 只能匹配零值
- **删 `Response.etag`**：它可由 body 推导（GET 路径已经在算同一个 hash），现为 5 处构造里 4 处写 `""`
- **`entry` 扁平化**：2 个 `entry`（`Demo` / `Serve`）各带 1 个永不承载内容的 `fn` 外壳
- **`env` 一等特性**：现在 `check` 直接拒（`E1`），配置走环境变量

## 3. 等你拍板的三件事

1. P2 现在做，还是并到 D1 一起改（那时反正要动 AST）？
2. `check.go` 拆 7 个文件还是 4 个（7 个更均衡，4 个幅度更小）？
3. `.build/verify.sh` 要不要提升为入库脚本 `code/webapi/deploy/verify-docs.sh`
   （它把 IMPL-PLAN §6 的复核命令整段跑一遍，现在只在被 gitignore 的目录里）？

## 4. 坑（踩过的，别再踩）

- `pkill -f blobd` 会把自己的命令行一起匹配掉，用 `pkill -x blobd`
- `DP_ROOT` 目录必须已存在，否则 `dp_serve` 打印「数据目录不可用」并 `return 1`
- `.build/` 在 `.gitignore` 第 7 行，里面三个脚本**不入库**：
  `verify.sh`（= IMPL-PLAN §6 的抽取）、`quickstart.sh`（= README Quick start 的抽取）、
  `audit.sh`（运行时 `dp_*` 调用点计数）
- `match` 分支之间**必须**有逗号（尾随可选），漏了报 `E0006`
- 所有 `*_test.go` 用 **tab** 缩进，用空格写 `SearchReplace` 会匹配失败
- 编辑文件与跑 `go vet` 不要放在同一批并行调用里，会读到旧文件

## 5. 文档地图

| 文件 | 管什么 |
| --- | --- |
| [SPEC.md](SPEC.md) | 语言设计。**八条硬约束 H1–H8 在这里**（教学面 / 关键字 / 原语 / 内存 / CPU / 边际内存 / 重复书写 / 实现规模） |
| [IMPL-PLAN.md](IMPL-PLAN.md) | 实施：里程碑、§8.1–§8.3 三份基线、§6 复核命令 |
| [D0-DEVIATIONS.md](D0-DEVIATIONS.md) | 实现与设计的有意偏差，每条带补齐时机 |
| NEXT.md | 本文件：交接与待办 |

## 6. 这份文档怎么维护

- 每完成一条待办：**删掉该条**，不留「已完成」列表（历史在 git 里）
- 每加一条约束：先写进 SPEC §2.2，再回这里登记判据
- 每次提交后更新 §0 的「现在的状态」
