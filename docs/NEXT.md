# NEXT：交接与待办

> 这份文档只回答一个问题：**换一台电脑、隔一段时间回来，怎么最快接上。**
> 它只记「现在在哪儿 + 还没做什么 + 怎么算做完」。语言设计看 SPEC，实施细节看 IMPL-PLAN，
> 实现与设计的有意偏差看 D0-DEVIATIONS。
>
> **接手的 AI 先做两件事**：跑一遍 §1 的命令确认环境；读完 §2 与 §3 再动任何代码。
> SPEC 里的八条硬约束是人拍板的，不要为了“让测试过”去改它们。

## 0. 现在的状态（截至 2026-10-30）

- `origin/main` = `main` = `82ac367`（v0.14，D3-B3 落地的那笔）。
- 本轮（v0.15）做的事：**表达式打印器**（P2 的前置件）。
  - 新包 `internal/print`：`Expr` / `Type` 渲染成可重新解析的**规范形**，括号按需
    （右操作数是二元式、`!` 的操作数是二元式、被调/被取是复合式），转义表与 lexer 对齐
    （不用 `strconv.Quote` —— 它生成 lexer 不认识的 `\u00e9`）。
  - 契约 `intents[].expr` 现在有值：`requires` / `example` 的表达式不再只导出一段无法定位的
    原文，这是 `E10` 报错能指到具体子表达式的前提。语义仍不校验。
  - 往返定理有测试把守：`TestExprRoundTrip`（打印 → 重新解析 → 再打印恒等）、
    `TestQuoteEscapes`（打印器产出的每个转义 lexer 必须认得）。
  - 规模：Go 非测试 5301 → 5470（C 1472 不变），合计 6773 → **6942，占 H8 的 8000 的 87%**，
    余量 1058 行；用例 114 → 120。
- 上一轮（v0.14）做的事：**D3-B3 落地 —— 成功码成为声明的投影**（上一批 B1/B2 的收尾）。
  - 写路由可加 `@created`（201）或 `@no_content`（204），默认 200；数字不进源码。
  - `@no_content` 只能投在 `Unit` 成功类型上（204 = 没有表示），读路由写这两个注解报 `E1`，
    两个同时写是解析期 `E0003`。
  - 运行时按 RFC 7230 §3.3.2 对 204 不发 `Content-Length` / `Content-Type`、不写 body。
  - 契约 JSON 多一个 `success` 字段（200 省略）；示例服务 `PUT /v1/blob` 200 → 204、
    `POST /v1/keys` 200 → 201（**主动破**上一批的行为等价判据，按批准单独一批）。
  - `soak.sh` 每轮的期望码同步改成 204 —— 不改会 100% 记失败，实际跑一次就是这样抓到的。
- 上一批（v0.13）做的事：**D3-B1/B2 —— 路由搬进 `boundary`，预条件守卫由声明合成**。
  - `boundary` 方法尾部新增 `@<verb>("/path")` + `impl <fn>`；动词是 check 期校验的小写标识符，
    不占关键字（`precondition` / `without` 是上下文关键字，总数仍 14）。
  - **写路由必须表态预条件**：`with precondition`，或 `without precondition: "理由"`。
    沉默 = 忘了写，`E1` 拦下 —— 与「错误不可丢弃」同一条纪律。
  - 服务形态的入口是**路由表**：有路由就生成 dispatcher，`entry` 并存报 `E1`；`env` 也只服务这个形态
    （没有路由时 env 无人消费，报错）。
  - `rt_http.c` 里的 `is_put` + 22 行 If-Match 比对下沉为 `dp_precondition_check`（运行时唯一实现），
    gen 在调用 impl 之前插唯一守卫。**412 现在恒空 body**（以前同理但没断言）。
  - 示例源码里 `Request` 记录被删：`main.dlt` 是 5 个普通函数（health / blob_get / blob_put / keys_list / keys_create），
    不再有 `req.path` 字面量与 `req.method == "PUT"`。
  - 契约 JSON 新增 `verb` / `path` / `impl` / `precondition`，5 条路由全部在契约里（D2 时只有 3 条 —— 契约是残缺的）。
- 规模：Go 非测试 5037 → 5301、内嵌运行时 1462 → 1472，合计 6499 → **6773，占 H8 的 8000 的 85%**。
  **余量 1227 行。** 样例 88 → 95 行（`contract.dlt` 51 → 64、`main.dlt` 37 → 31，路由与成功码都在声明上）。
  H7 的 path / method 字面量各 3 处 → 0（它们现在是声明上的数据，不是函数体里的字符串）。
- 用例：`grep -rc '^func Test' code/lang` 合计 **114**（上一轮 106；+8：成功码门禁 4、注解解析 3、
  投影生成 1）。`internal/check/route_test.go` 是新拆的，`check_test.go` 因此回落到 612 行（H8 单文件 ≤ 800）。
- 证据：`accept.sh` **17 项**（原 13 项里 3 项改口径 200→204/201，新增「412 empty body (bytes) = 0」
  「204 no Content-Length」「204 body (bytes) = 0」「201 头 Content-Length: 0」）；
  `SOAK_SECONDS=60 SOAK_INTERVAL=3` 跑 20 轮，0 失败，1 线程、无残留子进程；
  `go build ./... && go vet ./... && go test ./...` 全绿。
- 自检输出：`ok: 11 项声明，5 个函数`（服务形态没有入口行）。
- **3 天长稳仍未跑**（P0 不动）：D3 改了请求主链路（路由匹配 + 预条件守卫），60 s 只是冒烟。


## 1. 新机器五分钟上手

```sh
go version                                    # 需 ≥ 1.22（code/lang/go.mod 声明的最低版本，2026-10-30 由 1.24 下调）
mkdir -p .build                               # 编译产物放这里，目录不会自动建
cd code/lang && go build -o ../../.build/droplet ./cmd/droplet
go test ./...                                 # 判据是退出码 0，不是"数 ok 行"（见 §4）；共 114 个用例
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
# 实测输出：ok: 11 项声明，5 个函数（env App 块算一项；服务形态没有入口行，入口是 boundary 的路由表）
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

### P1 · 已完成：B3 成功码投影（201 / 204），见 §0（v0.14）

- 落点：`ast.BndMethod.Status` → `check.Route.Status` → `gen` 的 `out->status`；
  契约多一个 `success` 字段；运行时对 204 不收 `Content-Length`。
- 判据：`accept.sh` 17 项（13 项 + 上次的 412 空 body + 本次 3 项）；check 级 4 条门禁；`soak.sh` 期望码同步。
- 没做也不打算做：206（要 Range 解析与分段表示）。

<details>
<summary>已完成（v0.13）：D3-B1/B2 —— 路由与预条件守卫合成（原 §8.5 三件中的 B1 + B2）</summary>

- **病根比"412 有两处"更大**：`gen` 完全不读 `boundary`（`grep -rn "Boundary" internal/gen` 为空），
  路由是 handler 里的 3 个 `req.path ==` 字面量，"哪个方法是写"在 rt 的 `is_put` 与 handler 的
  `req.method == "PUT"` 各写一遍。更硬的一条：**契约是残缺的** —— `boundary` 声明 3 条方法，
  服务实际 5 条路由，`/health` 与 `POST /v1/keys` 在导出 JSON 里根本存在不了。
- 提案三件：**B1** 路由与守卫合成（`with … precondition` + `impl` 槽位，删 `rt_http.c` 那 22 行，
  比对函数留运行时、生成的守卫做唯一调用点）；**B2** 路由一致性护栏（A 落地后自动成立）；
  **B3** 成功码投影 `@created` / `@no_content`（201 / 204，数字仍不进源码，R2 不破；206 需要
  Range 解析，**不进 D3**）。
- **三件待点头**（原文表格在 §8.5.6，含反对意见）：① 方案 A（`impl` 槽位）vs 方案 B（方法带实现体）
  —— 我推 A，B 等附录 C 的 V1/V2 实验；② 客户端可见变更分批：412 空 body 随 B1，201/204 单独一批；
  ③ 新硬门禁「写路由必须显式声明预条件，或写 `without precondition: "理由"`」—— 我推**要**，
  它是安全边界搬家的前提。
- **判据升级**：B1 是安全边界搬家，光 `accept.sh` 绿不够 —— 要旧/新 C 逐字 diff +
  请求 × 响应矩阵逐项比（§8.5.5），且**必须重跑 3 天长跑**（与 P0 一起结）。
- 账（估）：H8 净 +127 → 约 6630（83%）；H7 的 path/method 字面量各 3 → 0；契约从 3 条变 5 条路由。
- 实结：H8 净 +225 → 6724（84%）；契约 5 条路由；`Request` 记录删除；用例 95 → 106。
  详见 IMPL-PLAN §8.5.7。

</details>

### P2 · 进行中：意图表达式 —— 先拍板组合子，再动 `E10`

- 已完成（v0.15）：**表达式打印器**，契约 `intents[].expr` 已有值（见 §0）。
- 待拍板：SPEC §5.10 列了约 7 个封闭组合子（`all/any/sum`、`unique`、`monotonic`、`conserved`、
  `once/stable/refines`），**尚未拍板**；`E10`（intent 抓不到 mutation 即硬错）因此还是空的。
- 顺序：词汇（组合子拍板）→ ~~打印器~~ → `E10` → 把 `require`/`example` 接进检查。


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
5. （2026-10-30 追加）**D3 按「方案 A」落地**：`impl` 槽位 + 合成 dispatcher，B1（路由 × 守卫）
   与 B2（一致性护栏）合成一批完成（v0.13）；**B3（201/204）单独一批**也已落地（v0.14）。
   新硬门禁「写路由必须显式声明预条件」同步生效。
6. （2026-10-30 追加）**3 天长稳继续缓缓**：本机不保证常开，P0 保持挂着；D3 两批都只跑了
   60 s 冒烟（20 轮、0 失败、1 线程），**这不算长稳结论**。下次能占住机器时优先补这一条。

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
- **推之前跑全套 `go test ./...`，不是只跑改过的那几个包**：v0.12（`ed9b1e0`）只跑了
  `./internal/check`，`internal/parse` 里 `TestParseWebapiContract` 的旧断言（`len(ApiError.Variants) == 4`，
  而 D2 把变体加到了 6）把 main 红了整整两个提交（`ed9b1e0` → `e4a0896`），CI 两档一起红，
  到 v0.13 才顺带修掉。同一份源码，`check` 局部的绿不蕴含 `./...` 的绿
- **别用 `| head` 截 `accept.sh` / `soak.sh` 的输出**：SIGPIPE 会杀掉脚本，`trap` 不执行，
  残留实例继续占着 18799 / 18800。下一次运行的新实例 `bind` 失败立刻退出，而每一项照样"绿"——
  测的是旧二进制（本次实踩：`srv.log` 里 `bind: Address already in use`，其余 17 行全对）。
  `soak.sh` 有启动存活检查，`accept.sh` 从 v0.14 起也有；两者都要读 `srv.log` 确认是本次的二进制
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
