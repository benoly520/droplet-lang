# NEXT：交接与待办

> 这份文档只回答一个问题：**换一台电脑、隔一段时间回来，怎么最快接上。**
> 它只记「现在在哪儿 + 还没做什么 + 怎么算做完」。语言设计看 SPEC，实施细节看 IMPL-PLAN，
> 实现与设计的有意偏差看 D0-DEVIATIONS。
>
> **接手的 AI 先做两件事**：跑一遍 §1 的命令确认环境；读完 §2 与 §3 再动任何代码。
> SPEC 里的八条硬约束是人拍板的，不要为了“让测试过”去改它们。

## 0. 现在的状态（截至 2026-10-30）

- 本地 `main` = `012c0ea`（`chore(lang)`），本文件这条 docs(next) 再压在上面 ——
  **共 5 条未推送，`origin/main` 仍在 `90ccef3`**（`git log --oneline origin/main..main` 实测）：
  `5c69477` 变体构造 → `f71c50b` docs(next) → `d49e6a0` entry 扁平化 → `012c0ea` 注释清理 → docs(next)。
- `d49e6a0` 做的事：入口从 `entry Name { fn f(…) }` 改成 `entry f(…)` —— 那层壳从不承载内容，
  按 H7 删的是语法位置而不是写法：旧形状在 parse 就报 `E0006` 并给改写提示。
  `EntryDecl` 这个 AST 节点整个删掉（入口 = 带 `Entry` 标记的 `FnDecl`），
  `check`/`gen`/`contract`/`dump` 各少一个分支。顺带补两条漏着的护栏：多入口报 `E1`
  （以前静默按 map 迭代顺序挑 EntryPoint）、`entry` 缺函数体报 `E0006`（以前拖到 `cc` 才炸）。
- `012c0ea`：清掉上一轮 `check.go` 拆分留下的重复注释（纯注释，`-print-c` 逐字相同）。
- 本轮已实测：`gofmt -l` 无输出、`go vet ./...` 无输出、**77 个用例全绿**、`accept.sh` 12 项全通、
  `hello` 打出 `world`、`shape` 打出 `wide`、**旧编译器跑旧语法 / 新编译器跑新语法的三份 `-print-c` 逐字相同**
  （hello 23 / shape 47 / blobd 133 行），`contract` JSON 只有入口的 `pos` 变了
- 60 s 压缩长稳（`SOAK_SECONDS=60 sh soak.sh`）本轮重跑过，退出码 0（2 轮 0 失败、无残留）；
  3 天长稳仍没做 —— 它需要一台常开的机器（见 §2 P0）

## 1. 新机器五分钟上手

```sh
go version                                    # 需 ≥ 1.24（code/lang/go.mod 声明的最低版本）
mkdir -p .build                               # 编译产物放这里，目录不会自动建
cd code/lang && go build -o ../../.build/droplet ./cmd/droplet
go test ./...                                 # 期望全部包 ok（共 77 个用例）
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
# （v0.9 之后入口就是那个函数本身，不再有外层的壳名）
```

## 2. 待办

### P0 · 3 天长稳（唯一需要「等」的事）

- 为什么一直没做：需要一台常开的机器
- 命令：`sh code/webapi/deploy/soak.sh 72`
- 判据：退出码 0；`.build/soak/fail.log` **为 0 字节**（脚本会预建这个空文件，它存在不代表失败）；RSS 斜率 ≤ 64 KB/h
- 想先压缩时间自测：`SOAK_SECONDS=60 sh code/webapi/deploy/soak.sh`（60 秒 = 2 轮，实测退出码 0、1 线程、无残留子进程）

### P1 · D1 设计项（**需要点头**）

- **删 `Response.etag`**：它可由 body 推导（GET 路径已经在算同一个 hash），现为 5 处构造里 4 处写 `""`
- **`env` 一等特性**：现在 `check` 直接拒（`E1`），配置走环境变量
- 两条都是 H7 基线里剩下未还的账，见 [IMPL-PLAN §8.2](IMPL-PLAN.md)；判据是 H7 实测归零 +
  77 用例与 `accept.sh` 全通 + 旧写法报错带改写提示
- 已定顺序：删 `Response.etag` → `env`（`env` 放最后因为它要跟 `contract` 的 `envs` 分组一起回来）
- **新解锁的一条**：412 条件失败可以改回「错误是值」了 —— handler 返回 `ApiError.Conflict`，
  同时删掉 `rt_http.c` 里自带的 If-Match 比对。以前它在语言侧表达不出来（构造不出错误值），
  变体构造落地后障碍消失。这条动了 HTTP 语义与运行时，**要单独点头**，也意味着要重跑长稳

## 3. 等你拍板的两件事

1. 现在本地攒了 5 条未推送（变体构造 + docs(next) + entry 扁平化 + 注释清理 + docs(next)）：`git push`，
   还是继续攒到 D1 第三项（删 `Response.etag`）？
2. `.build/verify.sh` 要不要提升为入库脚本 `code/webapi/deploy/verify-docs.sh`
   （它把 IMPL-PLAN §6 的复核命令整段跑一遍，现在只在被 gitignore 的目录里）？

## 4. 坑（踩过的，别再踩）

- `pkill -f blobd` 会把自己的命令行一起匹配掉，用 `pkill -x blobd`
- `DP_ROOT` 目录必须已存在，否则 `dp_serve` 打印「数据目录不可用」并 `return 1`
- `.build/` 在 `.gitignore` 第 7 行，里面的脚本**不入库**：
  `verify.sh`（= IMPL-PLAN §6 的抽取）、`quickstart.sh`（= README Quick start 的抽取）、
  `audit.sh`（运行时 `dp_*` 调用点计数）。本轮（2026-10-30）`verify.sh` 已不在，
  要复核就按 IMPL-PLAN §6 的命令逐段跑
- 交接文档里的 **git 状态必须每次真跑一遍再写**（`git status --short`、
  `git log --oneline origin/main..main`）。上一轮凭印象写了「已提交、尚未推送」，
  实际那两条早在 origin 上；错的结论比没结论更贵，因为下一班会照着它决定推不推
- 所有 `*_test.go` 用 **tab** 缩进，用空格写 `SearchReplace` 会匹配失败
- 编辑文件与跑 `go vet` 不要放在同一批并行调用里，会读到旧文件
- 做“拆分前后逐字相同”的对照时，`git worktree add .build/head HEAD` 后在 worktree 里用
  相对路径 `-o ../../../.build/x` 会落到 `.build/.build/x`（层级算错，反查不到）—— 用绝对路径
- 沙盒跑起来先 `export TMPDIR=$PWD/.build GOCACHE=$PWD/.build/gocache`，否则 `go build` 建临时目录失败
- 改 `*_test.go` 里某个测试函数：**整段函数（含函数头）一起替换**，别只替换函数头下面几行。
  本轮一次批量替换把 `func TestVariantWithFieldsRejected(t *testing.T) {` 吞掉了，留下一个
  没有函数头的悬空语句块；接着按行去补又因为「以为是 4 空格、实际是 tab」连续匹配失败，白绕一圈
- 改 markdown 的**表格行**用一次性 python 补丁（`.build/patch_*.py`：读全文、`replace(old, new, 1)`、
  找不到就 raise）。编辑工具会重排整张表，diff 里混进一堆无关空格改动，看不出真正改了哪条
- 语法侧再加构造类特性前先估行数：`gen/c.go` 曾因变体构造从 798 涨到 826，**破 H8 的 800 线**，
  只能同包拆文件（见 IMPL-PLAN §8.3 的 D1 追加）。拆分与加语法可以放同一条提交，
  但 `-print-c` 逐字相同的证据要在拆分完成、既有样例仍不使用新语法时取
- **同一个文件不要混用编辑工具与外部脚本**：`gen/c.go` 上轮已由 python 拆成 4 个文件，
  本轮又用 `SearchReplace` 改它 —— 编辑器递上去的是**拆分前的旧缓冲区**，826 行的整份版本被写回，
  表现为一堆 `redeclared`。恢复用 `git checkout -- <file>`，之后这个文件一律用脚本改到底
- 批量改写 `*_test.go` 里的 raw string 时，别把「闭合行」定成**整行恰好是 `}`**：
  `…})` 这种收尾会被跳过，脚本一路扫到 Go 函数自己的 `}`，把 `expectOne(…)` 与函数尾一起吃掉
  （本轮坏了 6 个文件）。要用 `startswith(col + "}")` + 把余下内容当尾巴接回，
  并加两条断言：body 内恰好一个 `fn`、末行必须以 `}` 结尾
- 同一份文档里「先改写旧探针、再插入新探针」的顺序不能倒，且**给旧写法留样本**：
  D0-DEVIATIONS §6 的 i.dlt 就是要用 `entry Demo { fn … }` 被 E0006 拒掉，它不能被自己的批量改写误伤；
  本轮第一条脚本因为一个锚点 `count == 0` 直接 abort，整个文件未写入（幸好 raise 得早，没写出半改状态）
- **后台 / 非交互 shell 里的 `go` 不是同一个**：它会解析到 `/usr/bin/go`（1.22），
  `go build` 直接报 `go.mod requires go >= 1.24（GOTOOLCHAIN=local）`。跑长稳、跑后台任务先
  `export PATH=/home/monom/sdk/go1.24/bin:$PATH`

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
