# NEXT：现在在哪、下一步做什么

> 换一台机器、隔一段时间回来，看这一页就够。
> 形态与判据看 [SPEC.md](SPEC.md)，实测数字看 [VALIDATION.md](VALIDATION.md)。

## 0. 现在在哪

- `main` 上只有一个介质：**合法 C11 + 四张声明表 + 一套检查**（`code/sup/` + `code/runtime/` + 24 条 wire 断言）。
- 原来那门自造语言（`.dlt`、编译器 4727 行）**已删除**。理由见 SPEC §2：实测没有赢，且它自己声明的
  invariant 被自己的服务违反。
- 所有数字都是跑出来的，不是估的：兼容性 5/0、wire 24/0（ASan 同）、oracle 7/7、变异 5 抓 / 2 等价 MISS /
  1 编译期、破坏测试 11/0、契约 14 目标 / 未证 0 / 孤儿 0。

## 1. 五分钟上手

```sh
sh code/sup/build.sh sup
sh code/sup/accept-sup.sh "$PWD/.build/sup/blobd" 18880 sup
sh code/sup/sabotage-sup.sh 18910
```

需要：`cc`（C11）、`sh`、`python3`、`git`、`flock`、`curl`。不需要 Go。

## 2. 待办（按优先级）

### P1 · 把 oracle 加宽：搬出更多纯决定

现在只有 2 个决定（创建结果、端口区间），因为只有它们住在用户面。鉴权（`rt_http.c` 请求循环里）、
版本前置（`rt_blob.c` 返回值里）、快照轮转都在运行时里，样例够不到。

做法：把能表达成“只有条件与常量”的决定搬到用户面（像 `dec_create_result` 那样），
再加 `DP_EXAMPLES` 行。**注意接线检查会要求服务真的引用它** —— 否则构建红，这是设计不是障碍。

验收：`DP_EXAMPLES` 行数上升，且 `build.sh` 的接线对列表同步增长；`mutate-dec.sh` 的变异体数量也跟着涨。

### P2 · 契约导出补全四节

`contract-sup.py` 只对账了 routes / envs / goals 三节的形状。缺一节：成功码投影
（201 / 204 / 200 由声明推出，源码里不出现数字）。

做法：`blobd --contract`（或 `--goals` 旁边再加一个开关）导出 JSON，四个分节齐全；
`contract-sup.py` 逐节核对而不是只 grep 目标行。

### P3 · 候选对比（多实现挑一份）

原来语言侧有 `explore`：同一意图给多份实现，跑同一批样例，报存活与反例对。
超集侧还没有对应物，但材料是现成的：`DP_EXAMPLES` 是封闭样例，`oracle` 已经能逐条跑。

做法：把 `dp_oracle` 扩展成“换一份 `dp_decide.c` 再跑一遍”，对比两份在样例上的分歧；
多于一个存活就是失败，并给出反例对。这是“AI 生成多份、机器挑一份”的最小形态。

### P4 · 长稳

`soak.sh` 已改成用 `build.sh` 出二进制，但 72 小时那条线从未跑过。要一台常开的机器。
判据不变：RSS 斜率 ≤ 64 KB/h，任何一次状态码或内容不符即失败。

### P5 · 部署面

`blobd.service` 与 `blobd.env.example` 是现成的。没做过真实 systemd 环境下的验收。
`DP_COMMIT_CMD` 的 `flock` 写法已经在样例里，别改回裸 `git commit`（见 §4）。

## 3. 一轮工作怎么算完

1. `sh code/sup/build.sh sup` 绿（审计 + 接线）。
2. `accept-sup.sh` 24/0。
3. `contract-sup.py` rc=0，且未证 = 0、孤儿 = 0。
4. 改了检查或决定：`oracle-sup.sh`、`mutate-dec.sh` 绿。
5. 改了检查本身：`sabotage-sup.sh` 仍然 11/11，且每条归因正确。
6. 改动了 `.o` 的引用关系：确认接线检查没有变空（`nm -u` 那两行仍打印“接线绿”）。

## 4. 坑（踩过的）

- **并发拉起的子进程要自己排队**：运行时对每次成功写入并发拉起 `DP_COMMIT_CMD`，
  裸 `git commit` 会抢 `HEAD` 的 ref 锁并静默丢一条提交（CI 上实测挂过 #23）。用 `flock` 串行。
  `flock` 只排开 ref 锁，排不开内容：写入快于提交时两次提交看到同一份文件，先拿到锁的那次提交为空
  （ASan 下稳定 3/4）。验收表因此逐写等提交落地；生产环境开 `DP_COMMIT_DEBOUNCE`，别用 0。
- **端口残留**：上一轮实例没死，新实例 bind 失败但请求照样有响应 —— 测到的不是这份代码。
  `accept-table.sh` 起服务后先 `kill -0` 再断言；手跑前 `pkill -x blobd`（`pkill -f` 会匹配到自己）。
- **脚本搬家要重算 `ROOT`**：`accept-table.sh` 在 `code/webapi/deploy/` 下，`ROOT` 是 `../../..`。
- **破坏测试的“红”必须归因到具体工具**：注入代码少个 `#include` 也会“红”，那是编译错误冒充检查成功。
  每条破坏都断言诊断文本，且必须有一条“本该全绿”的干净参照。
- **表里少一行不会红，实现多一个会红**：`-Wunused-function` 只抓“实现了但没进表”，
  反过来靠 `used but never defined`。两边都要有破坏测试（S1/S2）。
- **`nm -u` 看的是引用，不是源码**：接线检查靠它，所以改名、拆文件都绕不过去 —— 这是有意的。

## 5. 文档地图

| 文件 | 管什么 |
| --- | --- |
| [SPEC.md](SPEC.md) | 形态、四张表、检查面、边界与口子 |
| [VALIDATION.md](VALIDATION.md) | 怎么跑、跑出什么、这些数字不能证明什么 |
| 本文件 | 交接与待办 |
| `code/sup/` | 超集机制 + 旗舰服务 + 全部检查脚本 |
| `code/runtime/` | 服务运行时（1472 行共享 C） |
| `code/webapi/deploy/` | 24 条断言表、soak、systemd 单元与环境样例 |
