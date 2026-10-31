# c-control：对照组实验的全部工件（v0.29）

一句话说清这个目录：**把旗舰服务（blob 存储 Web API）不通过语言、直接用 C 再写一遍，
然后逐条试着让"人只审目标"依赖的四条纪律在纯 C 里机械成立。**
结果与解释在 `docs/VALIDATION.md` §7，这里只放可重跑的工件。

| 文件 | 是什么 | 关键数字 |
| --- | --- | --- |
| `service.c` | 第一版：AI 直接照 C 写的样子 | 195 行，19 条断言 **15/19**（挂 #7 #14 #17 #18，全是"错误不带表示"无处安放） |
| `service2.c` | 第二版：功能全对的 C（四条纪律在它身上试） | 201 行，**19/19** |
| `decisions.h` / `decisions.c` / `intent_c.c` | C 里要"可执行意图"必须补的脚手架 | 65 行；且它验的是**手抄的复制品**，不是服务真正跑的那两份 |
| `accept-c.sh` | 19 条断言的验收表，两组共用、可跑任意二进制 | 原 `deploy/accept.sh` 的 17 行打印 + 「404/405 空 body」两条断言 |
| `exp1-noguard.sh` | 纪律①：删掉 If-Match 守卫 | C：`-Werror` 全过、18/19；droplet：删声明 → `E1` 编译失败 |
| `manifest.json` + `surface-c.py` | 纪律②：外部面对账（方案 B 的 L1 原型） | 45 行；抓到 `DP_LOG_LEVEL`，也被一次拼接名重构击穿 |
| `exp2-indirect.sh` | 上面那个洞的可重跑证明 | 对账 rc=0，二进制照读（stderr 打印 `log level debug`） |
| `mutate-c.sh` | 纪律③：把 `verify` 的变异算子搬到 C | 8 算子 → 6 程序；样例抓 5，`-Werror` 抓 6（droplet 侧同批 6/6） |
| `exp4-invariant.sh` | 7.4：同一 key 连 POST 两次 | **lang 201/201（违反自己写的 invariant），C 201/409** |
| `OPTION-B.md` | "C + 清单 + 检查器"的一页设计与改投判据 | L1 已跑，L2 未量（登记 P17），L3 就是这门语言本身 |

重跑命令见 `docs/VALIDATION.md` §7.6。端口占用段 18801–18826。
前置：`go build -o .build/droplet ./code/lang/cmd/droplet`（脚本会自己补），以及 `cc`、`curl`、`git`。
