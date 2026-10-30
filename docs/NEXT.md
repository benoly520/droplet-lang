# NEXT：交接与待办

> 这份文档只回答一个问题：**换一台电脑、隔一段时间回来，怎么最快接上。**
> 它只记「现在在哪儿 + 还没做什么 + 怎么算做完」。语言设计看 SPEC，实施细节看 IMPL-PLAN，
> 实现与设计的有意偏差看 D0-DEVIATIONS。
>
> **接手的 AI 先做两件事**：跑一遍 §1 的命令确认环境；读完 §2 与 §3 再动任何代码。
> SPEC 里的八条硬约束是人拍板的，不要为了“让测试过”去改它们。

## 0. 现在的状态（截至 2026-10-30）

- `origin/main` = `main` = `8fdde01`，工作区干净（`git status --short` 空、
  `git log --oneline origin/main..main` 空）。
- 本轮（v0.11）做的事：**`env` 一等特性落地**，D1 四项至此全部完成。新文件 `check/env.go`（145 行）：
  `env` 块注册为带 `IsEnv` 标记的 record；源码里**不许构造** env 值（配置只能来自启动环境）；
  服务入口必须声明 `env` 块并补齐 `port/bind/root/token/max_body` 五键；命令行程序声明 `env` 报「无人消费」。
  生成器加配置加载器（必填键缺失或值非法 → 启动即失败），环境变量前缀由块名推出（`App` → `APP`），
  **源码里永不出现环境变量名**；`droplet contract` 增 `envs` 分组。
  样例 80 → 91 行（`contract.dlt` 43 → 54 的行全是那个 `env App` 块；`main.dlt` 37 未变）。
- 规模：Go 非测试 4566 → 4927（+361，其中 `check/env.go` 145、`gen/main.go` 的加载器与接线约 120），
  内嵌运行时 1470 → 1454（-16），合计 6034 → **6381，占 H8 的 8000 的 80%**。
  **余量只剩 1619 行** —— §8.3 那条「加语法与拆文件同一条提交」的纪律，从现在起是硬要求。
- 用例：`grep -c '^func Test'` = **91**（上一轮 79；本轮 +10 checker +2 生成期集成）。
  验收 `accept.sh` 仍是 13 项，结论未变。
- **本轮没跑长稳**，而 `env` 改了启动路径（配置加载先于监听）。所以 §2 P0 的门禁比之前更硬：
  下一次改动落地前，至少要重跑一次 `SOAK_SECONDS=60`，3 天口径仍挂在 P0。


## 1. 新机器五分钟上手

```sh
go version                                    # 需 ≥ 1.24（code/lang/go.mod 声明的最低版本）
mkdir -p .build                               # 编译产物放这里，目录不会自动建
cd code/lang && go build -o ../../.build/droplet ./cmd/droplet
go test ./...                                 # 期望全部包 ok（共 91 个用例）
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

### P1 · D2 设计项（三条合成一条，**全部待点头**，方案见 IMPL-PLAN §8.4）

- **删掉 `Response`，成功即值**（`entry -> bytes | ApiError`；无体用裸 `return`；状态码只在
  `tag → status` 表里写一次）。这条同时吃掉下面两条，所以合成一批、只重跑一次长稳。
- **H7 欠 8 处**：`main.dlt` 里 `status: 200` ×5、`body: b""` ×3。D2 之后应为 0。
  注意 **3 处 `b""` 不是靠"成功即值"消掉的**，靠的是「裸 `return` = 单位值」这条新规则 —— 它才是 D2 里
  唯一新增的语义，也是唯一需要认真反对的一条（见 §8.4 待点头表）。
- **`Response` 的字段形状长在 gen**：`check` 说 ok 而 `build` 报 `E-GEN` 的三类（缺 `status` /
  写 `etag` / 未知字段）。**原计划的"搬进 checker"不做了** —— 那是给同一套映射补一份并行实现。
  删掉 `Response` 之后这三类在结构上不存在，check 与 build 不可能再分歧。
- **412 只做一半**：加 `PreconditionFailed` 变体与 412 映射（该补的 D0 缺口），**但运行时里那段
  `If-Match` 比对不删**。理由是安全边界（与 `dp_valid_key` 同级）：它由运行时把守时，handler 忘写
  不会丢保护；搬到 handler 后忘一次就是静默丢更新。语言侧的正解是 `boundary ... with idempotency`
  合成这条检查（D3），不是交还给人。**这一条与上一班的建议相反，请重点看 §8.4 的理由再点头。**
- 批完之后要付的账：`accept.sh` 13 项必须逐条不变（其中两条最容易翻车：405 的**空 body**、
  412 的**无快照无提交**）、重跑 `SOAK_SECONDS=60`、`README`/`SPEC` 的 H7 状态更新。

## 3. 上一班拍板的结果（2026-10-30）

1. 下一条做 **`env` 一等特性**（连同 `contract` 的 `envs` 分组一起回来）——
   **已落地**：`8fdde01`，D1 四项全完成。
2. **不起 72 h 长稳**：本机不保证这几天不关机，P0 保持挂着；自测继续用
   `SOAK_SECONDS=60 sh code/webapi/deploy/soak.sh`（跑前先确认端口没被残留实例占）。
3. `.build/verify_d0.sh` **不入库**：D0-DEVIATIONS §6 的 13 条探针继续按文档手跑；
   要变成可重跑的护栏，得先解决它跟 `accept.sh` / `soak.sh` 抢实例的问题。
4. （2026-10-30 追加）**D2 合成一批做**，方案与三件待点头写在 IMPL-PLAN §8.4；
   本轮只落文档、没动代码。

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
