# NEXT：交接与待办

> 这份文档只回答一个问题：**换一台电脑、隔一段时间回来，怎么最快接上。**
> 它只记「现在在哪儿 + 还没做什么 + 怎么算做完」。语言设计看 SPEC，实施细节看 IMPL-PLAN，
> 实现与设计的有意偏差看 D0-DEVIATIONS。
>
> **接手的 AI 先做两件事**：跑一遍 §1 的命令确认环境；读完 §2 与 §3 再动任何代码。
> SPEC 里的八条硬约束是人拍板的，不要为了“让测试过”去改它们。

## 0. 现在的状态（截至 2026-10-30）

- `main` = `a4f7a3a`，**已推送**。其上还有一批**未提交**改动（本轮 = H8 拆分 + 两条语法减法）：
  - P1 H8：`internal/check/` 同包拆 8 个文件（`check.go` 1188 → 161，最大 `types.go` 225）
  - P2 两条语法减法：host 能力参数不写名字、Unit 函数不写 `return`（旧写法报 `E0006` / `E1` 并带改写提示）
  - 跟着改的样例：`examples/hello.dlt`、`webapi/src/contract.dlt`；测试 58 → 63
  - 文档：`docs/SPEC.md`（v0.7 §5.1.1）、`docs/IMPL-PLAN.md`（§8.2 / §8.3 基线刷新）、
    `docs/D0-DEVIATIONS.md`（复核命令换成新语法）、两份 README（H7/H8 表与用例数）
- 本轮已实测：`go vet ./...` 无输出、63 个用例全绿、`accept.sh` 12 项全通、
  **`-print-c` 逐字相同**（旧样例+旧编译器 vs 新样例+新编译器，`hello.dlt` 与 `webapi` 各一份）
- 具体文件清单以 `git status --short` 为准。

## 1. 新机器五分钟上手

```sh
go version                                    # 需 ≥ 1.24（code/lang/go.mod 声明的最低版本）
mkdir -p .build                               # 编译产物放这里，目录不会自动建
cd code/lang && go build -o ../../.build/droplet ./cmd/droplet
go test ./...                                 # 期望全部包 ok（共 63 个用例）
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

- 内容：P1（`internal/check/` 拆 8 文件）+ P2（两条语法减法及样例、测试、文档）——
  具体是哪几个文件以 `git status --short` 为准
- 判据：`git status --short` 为空
- 建议拆两条提交，方便单独回退：
  `refactor(check): check.go 同包拆 8 个文件，逻辑零改动` /
  `feat(parse): 落地两条语法减法（host 参数无名、Unit 函数不写 return）`

### P0 · 3 天长稳（唯一需要「等」的事）

- 为什么一直没做：需要一台常开的机器
- 命令：`sh code/webapi/deploy/soak.sh 72`
- 判据：退出码 0；`.build/soak/fail.log` **为 0 字节**（脚本会预建这个空文件，它存在不代表失败）；RSS 斜率 ≤ 64 KB/h
- 想先压缩时间自测：`SOAK_SECONDS=60 sh code/webapi/deploy/soak.sh`（60 秒 = 2 轮，实测退出码 0、1 线程、无残留子进程）

### P1 · D1 设计项（**需要点头**）

- **变体构造**：现在裸变体名被 `E1` 拒、`{...}` 只收 record，`match` 只能匹配零值
- **删 `Response.etag`**：它可由 body 推导（GET 路径已经在算同一个 hash），现为 5 处构造里 4 处写 `""`
- **`entry` 扁平化**：2 个 `entry`（`Demo` / `Serve`）各带 1 个永不承载内容的 `fn` 外壳
- **`env` 一等特性**：现在 `check` 直接拒（`E1`），配置走环境变量
- 四条都是 H7 基线里剩下未还的账，见 [IMPL-PLAN §8.2](IMPL-PLAN.md)；判据是 H7 实测归零 +
  63 用例与 `accept.sh` 全通 + 旧写法报错带改写提示

## 3. 等你拍板的三件事

1. 本轮两条提交现在就做，还是先把 D1 四条一起改完再一起提交？
2. D1 四条按什么顺序做（建议：变体构造 → `entry` 扁平化 → 删 `etag` → `env`）？
3. `.build/verify.sh` 要不要提升为入库脚本 `code/webapi/deploy/verify-docs.sh`
   （它把 IMPL-PLAN §6 的复核命令整段跑一遍，现在只在被 gitignore 的目录里）？

## 4. 坑（踩过的，别再踩）

- `pkill -f blobd` 会把自己的命令行一起匹配掉，用 `pkill -x blobd`
- `DP_ROOT` 目录必须已存在，否则 `dp_serve` 打印「数据目录不可用」并 `return 1`
- `.build/` 在 `.gitignore` 第 7 行，里面的脚本**不入库**：
  `verify.sh`（= IMPL-PLAN §6 的抽取）、`quickstart.sh`（= README Quick start 的抽取）、
  `audit.sh`（运行时 `dp_*` 调用点计数）。本轮（2026-10-30）`verify.sh` 已不在，
  要复核就按 IMPL-PLAN §6 的命令逐段跑
- `match` 分支之间**必须**有逗号（尾随可选），漏了报 `E0006`
- 所有 `*_test.go` 用 **tab** 缩进，用空格写 `SearchReplace` 会匹配失败
- 编辑文件与跑 `go vet` 不要放在同一批并行调用里，会读到旧文件
- 做“拆分前后逐字相同”的对照时，`git worktree add .build/head HEAD` 后在 worktree 里用
  相对路径 `-o ../../../.build/x` 会落到 `.build/.build/x`（层级算错，反查不到）—— 用绝对路径
- 沙盒跑起来先 `export TMPDIR=$PWD/.build GOCACHE=$PWD/.build/gocache`，否则 `go build` 建临时目录失败

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
