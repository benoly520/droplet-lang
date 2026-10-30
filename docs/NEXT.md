# NEXT：交接与待办

> 这份文档只回答一个问题：**换一台电脑、隔一段时间回来，怎么最快接上。**
> 它只记「现在在哪儿 + 还没做什么 + 怎么算做完」。语言设计看 SPEC，实施细节看 IMPL-PLAN，
> 实现与设计的有意偏差看 D0-DEVIATIONS。
>
> **接手的 AI 先做两件事**：跑一遍 §1 的命令确认环境；读完 §2 与 §3 再动任何代码。
> SPEC 里的八条硬约束是人拍板的，不要为了“让测试过”去改它们。

## 0. 现在的状态（截至 2026-10-30）

- `origin/main` = `main` = `ed9b1e0`（v0.12，D2 落地的那笔）。
- 本轮（v0.12）做的事：**D2 落地 —— 删掉 `Response`，响应面统一为「成功即值」**。
  - 入口成功类型只能是 `bytes`（有体）或 `Unit`（无体），签名回到 `-> bytes | ApiError`；
    `Response{status: 200, body: X}` 在 `check` 就报 `E1`，并提示「它在语言里就是 `X`」。
  - 状态码只在「错误变体 → 状态码」的表里出现：新增 `NotAllowed`(104→405)、
    `PreconditionFailed`(105→412)，成功恒 200（`Unit` 成功 = 200 + `Content-Length: 0`）。
  - **R3 是唯一新增的语义**：裸 `return` = 该返回类型的单位值（`bytes` → 空 body）。
  - checker 认错误类型的规则从"谁用过 `!`"改成"签名里写没写"（联合里非首项的 sum），
    否则只 `return ApiError.NotAllowed` 而不传播的程序会拿到 1.. 号、静默变 500。
  - `rt.h` 的 `DP_ERR_*` 与契约声明顺序由 `TestErrTagsMatchRuntime` 钉死（两边漂移 = 静默 500）。
  - **412 只做一半**：变体加了，但运行时 `If-Match` 比对**没删**（安全边界，与 `dp_valid_key` 同级），
    D3 由 `boundary` 合流。tag 派生的错误响应现在一律空 body（405 的验收项）。
- 规模：Go 非测试 4927 → 5037、内嵌运行时 1454 → 1462，合计 6381 → **6499，占 H8 的 8000 的 81%**。
  **余量 1501 行。** 样例 91 → 88 行（`contract.dlt` 54 → 51、`main.dlt` 37 未变）。
  H7 的两笔账第一次归零（`status: 200` 5→0、`body: b""` 3→0）。
- 用例：`grep -c '^func Test'` = **95**（上一轮 91；本轮 −2 个 `E-GEN` 反例、+4 个 check 级 R1/R3、
  +1 个 tag 表护栏、+1 个 412 不进 handler 的集成用例）。
- 证据：`accept.sh` 13 项逐条不变（405 仍空 body、412 仍无快照无提交）；
  `SOAK_SECONDS=60 SOAK_INTERVAL=0` 跑 866 轮，0 失败，RSS 恒 1852 KB、1 线程、无残留子进程。
- 自检输出变了：`ok: 8 项声明`（`Response` 被删，少了一项）。
- **3 天长稳仍未跑**（P0 不动）：D2 改了请求主链路（错误响应改由 tag 派生），60 s 只是冒烟。


## 1. 新机器五分钟上手

```sh
go version                                    # 需 ≥ 1.22（code/lang/go.mod 声明的最低版本，2026-10-30 由 1.24 下调）
mkdir -p .build                               # 编译产物放这里，目录不会自动建
cd code/lang && go build -o ../../.build/droplet ./cmd/droplet
go test ./...                                 # 判据是退出码 0，不是"数 ok 行"（见 §4）；共 95 个用例
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
# 实测输出：ok: 8 项声明，1 个函数，入口 handle（env App 块算一项；v0.12 删掉 Response 后少一项）
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

### P1 · 下一件：D3 设计（`boundary` 合成，才谈得上合流）

- **同一语义现在有两处**：运行时 `If-Match` 比对 + 语言侧 `PreconditionFailed`。D3 让
  `boundary … with idempotency`（或预条件声明）**合成**这条检查，然后才删运行时那份。
  在合成能力存在之前删，等于把保护降级成约定（IMPL-PLAN §8.4.7 的三条理由）。
- **201 / 204 / 206 仍无法表达**：成功恒 200。正解同样是 `boundary` 的成功形状合成状态码
  （与错误变体共用同一张 tag → status 表），不是把 `Response` 加回来。
- 这是**设计项**：先写方案（像 §8.4 那样带证据表与反对意见），点头后再动代码。
- 判据不变：`accept.sh` 13 项 + 长稳；合流那一轮必须重跑 3 天口径。

### P2 · 意图表达式：先拍板组合子，再动 `E10`

- SPEC §5.10 列了约 7 个封闭组合子（`all/any/sum`、`unique`、`monotonic`、`conserved`、
  `once/stable/refines`），**尚未拍板**；`E10`（intent 抓不到 mutation 即硬错）因此还是空的。
- 前置缺口：表达式不导出（`exprOf` 只认字面量），要先把 `fmt` 打印器补上，
  否则 `E10` 的报错指不到具体子表达式。
- 顺序：词汇 → 打印器 → `E10` → 把 `require`/`example` 接进检查。

### P3 · 工具链前向对照（只能在装了新 Go 的机器上跑）

- 沙盒只有 Go 1.22.5；CI 的 `latest` 档覆盖 build / vet / test，但**不跑 `-print-c` 逐字节对照**。
- 要补的证据：`GOTOOLCHAIN=go1.24.0`（或更新）编出的 `droplet`，对
  `hello.dlt` / `shape.dlt` / `webapi`（contract + main）各生成一份 C，与 1.22 版 `diff` 无输出。
- 为什么值得跑：`go` 指令已降到 1.22，但"1.24 与 1.22 生成的 C 相同"这件事目前只有 1.22 侧的证据。

## 3. 上一班拍板的结果（2026-10-30）

1. 下一条做 **`env` 一等特性**（连同 `contract` 的 `envs` 分组一起回来）——
   **已落地**：`8fdde01`，D1 四项全完成。
2. **不起 72 h 长稳**：本机不保证这几天不关机，P0 保持挂着；自测继续用
   `SOAK_SECONDS=60 sh code/webapi/deploy/soak.sh`（跑前先确认端口没被残留实例占）。
3. `.build/verify_d0.sh` **不入库**：D0-DEVIATIONS §6 的探针继续按文档手跑；
   要变成可重跑的护栏，得先解决它跟 `accept.sh` / `soak.sh` 抢实例的问题。
4. （2026-10-30 追加）**D2 三件合成一批**：删 `Response` / 裸 `return` = 单位值 /
   412 只加变体不删运行时 —— **已落地（v0.12）**，方案与证据在 IMPL-PLAN §8.4。

## 4. 坑（踩过的，别再踩）

- `pkill -f blobd` 会把自己的命令行一起匹配掉，用 `pkill -x blobd`
- `APP_ROOT` 目录必须已存在，否则 `dp_serve` 打印「数据目录不可用」并 `return 1`
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
- **后台 / 非交互 shell 里的 `go` 可能不是同一个**：它常常解析到 `/usr/bin/go`。
  以前这会让 `go build` 报 `go.mod requires go >= 1.24（GOTOOLCHAIN=local）`；
  2026-10-30 起下限降到 1.22，`/usr/bin/go`（1.22.5）已可编、全套用例绿（实测）。
  仍要留神的是**任何一个 `go` 低于 1.22** 的环境，以及 `GOTOOLCHAIN=auto` 在缺工具链时
  静默去联网下载 —— 那会表现为"网络超时"而不是"版本不对"
- **长稳的「0 失败」要先证明观测的是活的、且是新的进程**：上一班的 72 h 实例还占着端口时，
  新实例 bind 失败立刻退出，`$!` 那个 pid 已经死了 —— `/proc/$PID/status` 读不到就写 rss=0，
  而请求全由**旧二进制**答着，于是「2 轮 0 失败、全程 1 线程」全绿但整轮无效。
  `soak.sh` 已加护栏（sleep 0.4 后 `kill -0 $PID` 不成就报错退出），判据也补在 §2 P0
- 一次性脚本往 markdown 里插 **shell 探针**时，探针文本里的 `\n` 要用 `r'''…'''` 原样字符串：
  普通字符串里的 `\\n` 会被 Python 先吃掉，写进文档的 printf 参数就成了真换行，
  整条探针碎成一地（本轮 `patch_dev_etag.py` 第一版就是这样，重写后才对）
- 改「用例数 / 验收项数」这种散在多处的小数字，先 `grep -rn` 全仓再改：本轮 77 → 79、12 → 13
  合计 7 处（README×2、IMPL-PLAN×3、NEXT×1、D0-DEVIATIONS×1），漏一处就是一份自相矛盾的文档
- **入口返回联合里的「错误是值」需要生成器分两路**：`return ApiError.NotAllowed` 不是成功值，
  不能写进 `.u.ok`（C 会报 `incompatible types`），要写进联合的 `.tag`。判据是表达式类型是不是
  当前返回联合里的错误成员（`isErrItem`）。D2 第一次编译就是踩在这里
- **412 与 405 不是同一条路**：405 现在由 handler 的 tag 派生（空 body），412 仍由运行时在进
  handler 之前发出（文本 body）。写测试时别把「tag 派生 = 空 body」推广到 412
- 批量改 `check_test.go` 里的服务样例：先换签名（`-> Response | ApiError` → `-> bytes | ApiError`），
  再换构造（`return Response{…}` → `return b""`），最后删 `Response` 类型声明 —— 顺序倒了会
  留下「签名是 bytes、函数体还返回 Response」的中间态，`expectOne` 会因此报 2 条错而不是 1 条

## 5. 文档地图

| 文件 | 管什么 |
| --- | --- |
| [SPEC.md](SPEC.md) | 语言设计。**八条硬约束 H1–H8 在这里**（教学面 / 关键字 / 原语 / 内存 / CPU / 边际内存 / 重复书写 / 实现规模） |
| [IMPL-PLAN.md](IMPL-PLAN.md) | 实施：里程碑、§8.1–§8.3 三份基线、§6 复核命令 |
| [D0-DEVIATIONS.md](D0-DEVIATIONS.md) | 实现与设计的有意偏差，每条带补齐时机 |
| NEXT.md | 本文件：交接与待办 |
| `.github/workflows/ci.yml` | 两档 CI：`floor`（Go 1.22，守下限）+ `latest`（守前向）。只跑 build / vet / test |

## 6. 这份文档怎么维护

- 每完成一条待办：**删掉该条**，不留「已完成」列表（历史在 git 里）
- 每加一条约束：先写进 SPEC §2.2，再回这里登记判据
- 每次提交后更新 §0 的「现在的状态」
