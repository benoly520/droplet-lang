# Droplet

**给 AI 写的服务用的可验收 C 工程：源码是合法 C11，声明是几张表，实现一旦偏离声明，检查就会红。**

这里的“超集”只有一条含义：合法的 C 不会被这一层拒绝。自造语言已删除，也不再做任何语言扩展（见 docs/SPEC.md §2.1）。

“AI”在这里指作者，不是设计对象：语法和工具链里没有任何一处是专为 AI 设计的。专为 AI 的是**工作流** ——
声明能被机器对账、检查会失败、人不必读实现就能验收。这个工作流是否真的让 AI 写出更可靠的 C，
**目前没有测量**（见 docs/VALIDATION.md §4）。

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
起作用的那部分，并且明确不再往语言方向走。`code/` 下现在只有 C11 与 shell / Python。

## 五分钟上手

需要 `cc`（C11）、`sh`、`python3`、`git`、`flock`、`curl`。不需要 Go，也不需要代码生成器。

```sh
sh code/sup/build.sh sup                       # 编用户 TU -> 符号审计 -> 接线检查 -> 链接
sh code/sup/accept-sup.sh "$PWD/.build/sup/blobd" 18880 sup   # 24 条 wire 断言
```

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
| 机械变异（`mutate-dec.sh`） | 变异体存活且不是人工判定的等价变异 |
| wire 断言（`accept-table.sh`） | 24 条对外承诺任何一条破了（状态码、头、body、快照、提交） |
| 契约对账（`contract-sup.py`） | 目标引用了不存在的断言编号，或目标留成未证（写 `""` 是显式承认） |
| 破坏测试（`sabotage-sup.sh`） | 12 条注入的缺陷里有一条**没有**被具名工具以具名诊断抓住 |

最后一行最重要：它检查的是检查器本身。“被抓住”如果其实是注入代码写坏了导致的编译失败，那是假绿 ——
这套东西存在，就是因为这件事真的发生过一次。

## 目录

```
code/sup/            受检层：四张声明表、旗舰服务、全部检查
  blobd.c            DP_ROUTES + DP_GOALS —— 125 行，用户所有
  dp_env.h           DP_ENVS —— 18 行，用户所有
  dp_decide.h/.c     三个纯决定 + DP_EXAMPLES —— 87 行，用户所有，服务链接的就是这份
  dp_sup.h dp_cfg.c dp_conf.h dp_oracle.c   机制，344 行
code/runtime/        服务运行时，1469 行 C（rt_core / rt_http / rt_blob / rt_ver）
code/webapi/deploy/  24 条断言表、soak、systemd 单元、环境变量样例
docs/                SPEC（这是什么）、VALIDATION（实测数字）、NEXT（交接）
```

## 已知边界

这是检查面，不是沙箱。绕开 `build.sh` 就绕开审计（E7）；`dlopen` + `dlsym` 在运行时解析，
`nm -u` 看不见（E8）；oracle 只够得到**纯决定**，所以运行时里的鉴权、body 上限与快照轮转只有 wire 断言兜着（条件请求的判断已经搬出来了）。
这些都写在 [docs/SPEC.md](docs/SPEC.md) §5 与 [docs/VALIDATION.md](docs/VALIDATION.md) §4 里 ——
连同“这些绿色数字**不能**证明什么”。

英文版见 [README.md](README.md)。
