# Droplet

**给 AI 写的服务用的可验收 C 工程：源码是合法 C11，声明是几张表，实现一旦偏离声明，检查就会红。**

这里的“超集”只有一条含义：合法的 C 不会被这一层拒绝。自造语言已删除，也**不再对 C 做任何语言扩展**
（见 docs/SPEC.md §2.1）—— 不加语法、不加编译器分支、不做注解检查器。但有一件事要说清，不藏：
v0.38 加了 `lang/`，一套**独立的**登记格式，用来登记“承诺 ↔ 判据”的关系（见 docs/LANG2.md）。
v0.39 更正：`lang/*.dpt` 是**格式不是语言**（离开 `req/` 五表它什么都判不了），另起 `drop/` 才是那门
极简语言 —— 定义只有 `drop/spec.txt`，不引用仓库任何其它文件（见 docs/DROP.md）。
v0.40 把「同一件事的第二种写法」全删了（循环只剩尾递归一条，没有 `map`/`filter`/`fold`/`range`/`sort`，
没有隐式转换），并当场用这门语言写了一个能跑的 HTTP 服务器来证明删完还表达得出来。
它不碰 C、不进题面冻结、不替换 `req/`，状态是实验性，并且写了自杀条款。

“AI”在这里指作者，不是设计对象：语法和工具链里没有任何一处是专为 AI 设计的。专为 AI 的是**工作流** ——
声明能被机器对账、检查会失败、人不必读实现就能验收。这个工作流是否真的让 AI 写出更可靠的 C：
现有**一项小规模测量** —— 决定层 3 次独立生成，首次提交全部通过（pass@1 = 3/3）；
外加一次维护实验（405 -> 501，5 文件 / 18 个 +/- 行，全套检查绿）。
完整服务与真实人类维护者仍未测量，见 [docs/EXPERIMENT.md](docs/EXPERIMENT.md)。

它围绕这个分工设计：

| 角色 | 做什么 | 不做什么 |
| --- | --- | --- |
| AI | 写实现与声明、自己跑检查 | 不解释过程，不自证正确 |
| 编译器 / 构建脚本 / 检查器 | 该错的时候让构建或验收失败 | 不判断业务对不对 |
| 人 | 验收结果：能跑吗、断言全绿吗、改坏会红吗 | 不逐行读实现 |

所以交付物不是语法，而是**一条从声明走到运行二进制的、会失败的检查链**。红不了，就等于没检查。

## 现状

仓库里只剩一种介质。原来那门自造语言（自己的词法、语法、类型检查、代码生成，4727 行 `.dlt` 前端）
**已删除** —— 拿同一个服务用纯 C 写一遍做对照，它唯一在乎的那条没有赢（“声明的目标”到“跑着的实现”
之间没有一条会失败的检查），而且它自己声明的 invariant 被自己的旗舰服务违反了。留下的形态保留了真正
起作用的那部分，并且明确不再往**改 C** 的方向走。`code/` 下现在只有 C11 与 shell / Python；
`lang/` 下是与 C 无关、只登记承诺与判据的实验性格式（v0.38，四段门 + 三道破坏守着，见 docs/LANG2.md）。
`drop/` 下是一门从零重做的极简语言（v0.40）：`sh drop/check.sh` 一条命令，五段门 + 十一条破坏，
定义 `spec.txt` 150 行 / 1,750 token = 预算的 100.0%；同一件事只许一种写法（迭代只剩尾递归，
没有 `map`/`filter`/`fold`/`range`/`sort` 这些替你循环的名字，没有隐式转换），并且这份定义足够写出一个能跑的
HTTP 服务器（`drop/examples/http.dr`，23 行，门里第五段拿真 socket 打它）。它不碰 C、不进题面冻结、也不读 `req/`。

## 五分钟上手

需要 `cc`（C11）、`sh`、`python3`、`git`、`flock`、`curl`。不需要 Go，也不需要代码生成器。

```sh
sh code/sup/check.sh                           # 一条命令一个退出码（构建/样例/契约/登记/冻结/预算/登记格式）
sh drop/check.sh                               # 极简语言 drop：例子 + 对账 + 预算 + 真 socket 打服务器 + 十一条破坏
sh code/sup/report.sh .build/check/blobd --accept 18881   # 人类验收页：条款 -> 断言/样例 -> 红绿
```

作者回路是三段：`brief.sh` 出题面包（声明 + 契约 + 命令，不含实现）、`check.sh` 给第一个失败项、
`submit-sup.sh 候选.c` 在临时树里跑整条面并接受/拒绝（含题面冻结）。维护者用 `check.sh --full`
追加验收、变异、破坏与变异覆盖分。

或者直接打它：

```sh
mkdir -p /tmp/blobd-data && APP_ROOT=/tmp/blobd-data APP_TOKEN=secret \
  APP_BIND=127.0.0.1 APP_PORT=8787 .build/sup/blobd &
curl -s -X POST -H 'Authorization: Bearer secret' http://127.0.0.1:8787/v1/keys/demo -i
curl -s -X PUT  -H 'Authorization: Bearer secret' --data-binary 'hello' http://127.0.0.1:8787/v1/blob/demo -i
curl -s          -H 'Authorization: Bearer secret' http://127.0.0.1:8787/v1/blob/demo
```

## 到底检查了什么

| 检查 | 什么时候红 |
| --- | --- |
| 表 ↔ 实现双向对账 | 表里列了不存在的实现（`used but never defined`），或实现没被表引用（`-Wunused-function` + `-Werror`） |
| 符号审计（`audit-sup.sh`） | **用户** TU 引用了 `getenv` / `environ` / `socket` / `open` … 却没声明 |
| 接线检查（`build.sh`，`nm -u`） | 服务不再引用被 `DP_EXAMPLES` 覆盖的决定 —— 那说明样例验的是复制品 |
| 样例 oracle（`oracle-sup.sh`） | `DP_EXAMPLES` 任一行挂；零条可执行样例算失败；崩溃单列，绝不记成通过 |
| 机械变异（`mutate-dec.sh`） | 13 个变异体里有一个存活且不是人工判定的等价变异（实测 10 抓 / 2 等价 / 1 编译期响） |
| wire 断言（`accept-table.sh`） | 30 条对外承诺任何一条破了（状态码、头、body、快照、提交、鉴权、清单外壳、Content-Type、启动期拒绝） |
| 契约对账（`contract-sup.py`） | `--contract` 四节缺失，或目标引用了不存在的断言编号，或目标留成未证（写 `""` 是显式承认） |
| 必读面预算（`surface-budget.sh`） | 必读的表、类型与参考实现超过 9,000 token / 32 KB（实测约 8,197 token，估算） |
| 候选比较（`compare-sup.sh`） | 0 份或 >1 份候选通过样例（无解 / 歧义） |
| 首次成功率量尺（`firstpass.sh`） | 给定候选序列时算 first-pass 与 pass@k；**没有模型，就没有数字** |
| 维护成本（`maint-cost.sh`） | 脚本化改动的文件数或 +/- 行数超过预算 |
| 翻译层（`req/SRC.md` + `req/REQ.tsv` + `req/TRANS.tsv` + `req/AMBIG.tsv` + `req/GAP.tsv` + `req/BASELINE`） | 条款没有译法记录、反例栏空着、反例认领的 `S<n>`/`E<n>` 不存在或实测没弄红本条款的断言、歧义“未定”、缺口“未闭”；或“新增”条款在基线（旧实现 + 当前题面）上就是绿的（把现状抄成了需求）—— 基线核对 v0.37 起自动跑 |
| 需求登记（`req-check.py`） | 条款没有断言/样例撑着；或源句/断言/样例/目标没人认领；或条款与目标声明的证明不一致 |
| 登记格式（`lang-check.sh`，实验性） | 定义与检查器对不上（写了不会发生的错误码、或有条码从没被样本打中）；或 `lang/blobd.dpt` 与 `req/` 不再逐栏等价；或完整定义超过 1,500 token |
| 题面冻结（`freeze.sh`） | 验收表、声明头、**5 份译稿**或契约与 `req/FROZEN` 不一致（题被改过） |
| 变异覆盖分（`mutation-score.py`） | 改第 N 条断言的期望它不红；或实现变异红了没声明的断言；或覆盖低于 floor |
| `drop` 语言（`drop/check.sh`，独立于本工程） | 目录里出现指到本工程别处的引用（独立性现在是门，不是文档里的一句 grep）；例子（含被改写成 v0.40 写法的 4 份读者程序）输出对不上 `.out`；定义的 14 条错误码与实现的 14 条不一致、有条码没有能触发它的样本、或实现某条行为在定义里找不到原话；定义超过 1,750 token；定义里 `> ` 那 24 行真代码抽出来跑不通；真 socket 打 `examples/http.dr` 的 89 行对话台账对不上；命令行吐出 Python 堆栈 |
| 破坏测试（`sabotage-sup.sh`） | 23 条注入的缺陷里有一条**没有**被具名工具以具名诊断抓住（含 6 条改题/改译稿/改基线的、3 条改登记语言定义的） |

最后一行最重要：它检查的是检查器本身。“被抓住”如果其实是注入代码写坏了导致的编译失败，那是假绿 ——
这套东西存在，就是因为这件事真的发生过一次。

## 目录

```
code/sup/            受检层：四张声明表、旗舰服务、全部检查
  blobd.c            DP_ROUTES + DP_GOALS —— 229 行，用户所有
  dp_env.h           DP_ENVS —— 18 行，用户所有
  dp_decide.h/.c     五个纯决定 + 19 条 DP_EXAMPLES —— 114 行，用户所有，服务链接的就是这份
  check.sh report.sh brief.sh submit-sup.sh   三条回路：作者、人类验收、提交口
  req-check.py freeze.sh mutation-score.py   登记链+翻译层 / 题面冻结 / 断言灵敏度
  dp_sup.h dp_cfg.c dp_conf.h dp_oracle.c   机制，344 行
code/runtime/        服务运行时，1468 行 C（rt_core / rt_http / rt_blob / rt_ver，含 rt.h）
code/sup/lab/        实验题面包：两条本仓库里不存在的决定
code/webapi/deploy/  30 条断言表、soak、systemd 单元、环境变量样例
req/                 源句 SRC.md、19 条条款、译稿 TRANS/AMBIG/GAP、基线 BASELINE、冻结清单 FROZEN、README（五道门）
lang/                实验性登记格式：droplet.dpt（定义，1,321 token）、blobd.dpt（由 req/ 机械转写，等价性有门）
drop/                v0.40 的极简语言（干净房间）：spec.txt 唯一定义（1,750 token 顶满预算）、drop.py 实现（含 serve/reply 服务器）、check.sh 五段门 + 十一条破坏、examples/ + tests/
docs/                SPEC、VALIDATION（实测数字）、EXPERIMENT（三个实验）、LANG2（登记格式与三臂实测）、DROP（极简语言与三轮冷读）、NEXT（交接）
```

## 已知边界

这是检查面，不是沙箱。绕开 `build.sh` 就绕开审计（E7）；`dlopen` + `dlsym` 在运行时解析，
`nm -u` 看不见（E8）；oracle 只够得到**纯决定**（条件请求、鉴权、body 上限现在都在里面），
所以运行时里的读旧版本、读 header、算 ETag、快照轮转只有 wire 断言兜着。首次成功率有量尺
（`firstpass.sh`），但仓库里没有模型在环，**没有测出来的数字**。
这些都写在 [docs/SPEC.md](docs/SPEC.md) §5 与 [docs/VALIDATION.md](docs/VALIDATION.md) §4 里 ——
连同“这些绿色数字**不能**证明什么”。

英文版见 [README.md](README.md)。
