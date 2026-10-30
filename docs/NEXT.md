# NEXT：交接与待办

> 这份文档只回答一个问题：**换一台电脑、隔一段时间回来，怎么最快接上。**
> 它只记「现在在哪儿 + 还没做什么 + 怎么算做完」。语言设计看 SPEC，实施细节看 IMPL-PLAN，
> 实现与设计的有意偏差看 D0-DEVIATIONS。
>
> **接手的 AI 先做两件事**：跑一遍 §1 的命令确认环境；读完 §2 与 §3 再动任何代码。
> SPEC 里的八条硬约束是人拍板的，不要为了“让测试过”去改它们。

## 0. 现在的状态（截至 2026-10-30）

- `origin/main` = `main` = `aa62332`：本轮三条（`feat(lang)` 删 `Response.etag`、
  `docs(lang)` 四份规范文档、`docs(next)`）**已 push**，`git log --oneline origin/main..main`
  实测为空（别凭印象写）；下面记拍板结果的那条 `docs(next)` 补记随后同推。
- 本轮（v0.10）做的事：`Response` 的 `etag` 字段从语法里删掉，ETag 改由**运行时从 body 推导**
  （`rt_http.c`：`dp_sys_u64_str(dp_sys_hash64(resp.body))`，空 body 就不发这个头）。
  连带：`dp_response` 少一个字段、gen 的响应映射多两个拒绝分支（`etag` 与「未知响应字段」，
  后者以前被静默丢弃）、入口不再收 `sys: Sys`（推导后 handler 里 `sys.*` 零调用）。
  样例 86 → 80 行（`contract.dlt` 43 + `main.dlt` 37），实现 6023 → 6034 行。
- 行为变化两条，都已登记 D0-DEVIATIONS §2：`PUT` 的成功响应不再带 `ETag`（客户端要新版本号请
  `GET`）；`/health` 与 `/v1/keys` 的响应**开始**带 `ETag`（旧代码在那两处写 `etag: ""` = 不发头）。
  `GET` 的 tag 值与 `If-Match` 的比对逐字节不变 —— 新旧都是同一个函数对同一份内容求值。
- 本轮已实测：`gofmt -l` 无输出、`go vet ./...` 无输出、**79 个用例全绿**、`accept.sh` **13 项**全通、
  `hello` 打出 `world`、`shape` 打出 `wide`、D0-DEVIATIONS §6 的 13 条探针（含新增 m / o / p 三条）
  逐条与文档期望相同；`-print-c` 行数 hello 23 / shape 47 / webapi 130（原 133，少三行 `m_etag`）。
  **这一条减法不要求 `-print-c` 逐字相同**（它删的是两次计算里的一次，不是书写冗余），
  判据换成 HTTP 行为等价，见 SPEC §5.1.1 与 IMPL-PLAN §8.2。
- 60 s 压缩长稳重跑过，退出码 0（2 轮 0 失败、`fail.log` 0 字节、1 线程、无残留）；
  **第一次跑是无效的** —— 上一班留下的 72 h 长稳实例还占着 18800，新实例 bind 失败即退出，
  请求全打在旧二进制上，而 `sample.log` 的 rss 列是 0（见 §4）。已给 `soak.sh`
  加「自建实例启动即退出就报错」的护栏，并杀掉残留进程。3 天长稳仍没做（需要常开的机器，见 §2 P0）。

## 1. 新机器五分钟上手

```sh
go version                                    # 需 ≥ 1.24（code/lang/go.mod 声明的最低版本）
mkdir -p .build                               # 编译产物放这里，目录不会自动建
cd code/lang && go build -o ../../.build/droplet ./cmd/droplet
go test ./...                                 # 期望全部包 ok（共 79 个用例）
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
# 实测输出：ok: 8 项声明，1 个函数，入口 handle
# （v0.9 之后入口就是那个函数本身，不再有外层的壳名）
```

## 2. 待办

### P0 · 3 天长稳（唯一需要「等」的事）

- 为什么一直没做：需要一台常开的机器
- 命令：`sh code/webapi/deploy/soak.sh 72`
- 判据：退出码 0；`.build/soak/fail.log` **为 0 字节**（脚本会预建这个空文件，它存在不代表失败）；RSS 斜率 ≤ 64 KB/h
- 想先压缩时间自测：`SOAK_SECONDS=60 sh code/webapi/deploy/soak.sh`（60 秒 = 2 轮，实测退出码 0、1 线程、无残留子进程）
- **跑之前先确认端口空闲**：`ss -ltn | grep 18800` 与 `pgrep -x blobd` 都该是空的。残留实例会把
  新实例的 bind 顶掉，而请求照样有响应（来自旧二进制）—— 这种「0 失败」是假的。
  `sample.log` 的 rss 列为 0 就是这个形状；v0.10 起脚本自己会拦（启动即退出 → 报错并退出 1）

### P1 · D1 设计项（`env` 已点头，其余仍**需要点头**）

- **`env` 一等特性**：现在 `check` 直接拒（`E1`），配置走环境变量。D1 剩下的最后一条，
  它要跟 `contract` 的 `envs` 分组一起回来
- **H7 还欠 8 处**（本轮数出来的，以前没进清单）：`main.dlt` 里 `status: 200` ×5、`body: b""` ×3。
  它们删不掉，因为重复的是**值**不是书写位置；处置要动 handler 的返回形状（「成功即值」：
  `entry` 直接返回 `bytes | ApiError`，状态码与 `Content-Type` 由运行时包装）。
  **这条动了入口的返回类型，要单独点头**，已登记 IMPL-PLAN §8.2 归属 D2
- **`Response` 的字段形状仍长在 gen**：`check` 说 ok 而 `build` 报 `E-GEN` 的三类（缺 `status` /
  写 `etag` / 未知字段）没有收进 checker —— 「响应面」目前不是 checker 的概念。
  要不要搬过去是笔设计账，见 D0-DEVIATIONS §4.1 与 IMPL-PLAN §8.1
- **拍板结果（2026-10-30）**：下一条做 `env`。上面两条（返回形状改造、checker 的响应面）
  **没有点头**，留在本清单等下一轮再议
- **新解锁的一条**：412 条件失败可以改回「错误是值」了 —— handler 返回 `ApiError.Conflict`，
  同时删掉 `rt_http.c` 里自带的 If-Match 比对。以前它在语言侧表达不出来（构造不出错误值），
  变体构造落地后障碍消失。这条动了 HTTP 语义与运行时，**要单独点头**，也意味着要重跑长稳

## 3. 上一班拍板的结果（2026-10-30）

1. 下一条做 **`env` 一等特性**（连同 `contract` 的 `envs` 分组一起回来）。「成功即值」的
   返回形状改造与「把响应面收进 checker」两条**没有点头**，留在 §2 P1 等下一轮再议。
2. **不起 72 h 长稳**：本机不保证这几天不关机，P0 保持挂着；自测继续用
   `SOAK_SECONDS=60 sh code/webapi/deploy/soak.sh`（跑前先确认端口没被残留实例占）。
3. `.build/verify_d0.sh` **不入库**：D0-DEVIATIONS §6 的 13 条探针继续按文档手跑；
   要变成可重跑的护栏，得先解决它跟 `accept.sh` / `soak.sh` 抢实例的问题。

（本轮三条提交已 push：`9d7d30a..aa62332`。）

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
- **长稳的「0 失败」要先证明观测的是活的、且是新的进程**：上一班的 72 h 实例还占着端口时，
  新实例 bind 失败立刻退出，`$!` 那个 pid 已经死了 —— `/proc/$PID/status` 读不到就写 rss=0，
  而请求全由**旧二进制**答着，于是「2 轮 0 失败、全程 1 线程」全绿但整轮无效。
  `soak.sh` 已加护栏（sleep 0.4 后 `kill -0 $PID` 不成就报错退出），判据也补在 §2 P0
- 一次性脚本往 markdown 里插 **shell 探针**时，探针文本里的 `\n` 要用 `r'''…'''` 原样字符串：
  普通字符串里的 `\\n` 会被 Python 先吃掉，写进文档的 printf 参数就成了真换行，
  整条探针碎成一地（本轮 `patch_dev_etag.py` 第一版就是这样，重写后才对）
- 改「用例数 / 验收项数」这种散在多处的小数字，先 `grep -rn` 全仓再改：本轮 77 → 79、12 → 13
  合计 7 处（README×2、IMPL-PLAN×3、NEXT×1、D0-DEVIATIONS×1），漏一处就是一份自相矛盾的文档

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
