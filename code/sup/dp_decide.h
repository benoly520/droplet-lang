/* Droplet 超集：纯决定 + 样例表（用户所有，v0.32 / P18 第 1 条）。
 *
 * 这一层补的是超集侧唯一没有的语言能力：`droplet verify` 那种"封闭样例当 oracle"。
 * 与对照组那套（`docs/validation/c-control/decisions.c`）的差别是全部要点：
 * 那份是运行时条件的手抄，样例验的是复制品、服务跑的是原件（VALIDATION §7.3 ③）；
 * 这里的 `dp_decide.c` 是**服务链接的那一份** —— `dp_sup.h` 的 `dp_created()`
 * 与 `dp_cfg.c` 的 `dp_load_port()` 调的就是这些函数。所以样例翻面 = 服务翻面。
 *
 * 边界（不许漂）：住在这里的只有**纯决定** —— 函数体里只有条件与常量，
 * 不碰环境变量、不碰文件系统、不碰 arena。鉴权与 body 上限住在两种介质共用的
 * 运行时那 1472 行里，本层不动它们（记 SPEC §0.8.6 与 D0）。
 */
#ifndef DP_DECIDE_H
#define DP_DECIDE_H

#include <stdbool.h>
#include <stddef.h>

#include "rt.h"

/* 创建的结果投影：错误优先，其次"已存在" -> Conflict。
 * P16 输掉的那一局（语言版把 dp_blob_create 的 bool 丢了，重复 POST 回 201）
 * 在这里被收成一条可验的形状。 */
int dec_create_result(bool fresh, int err);

/* 端口可用区间。返回 false 时 `dp_cfg.c` 留 0，`dp_serve_sup()` 因此拒绝启动，
 * 而不是猜一个端口。 */
bool dec_port_usable(unsigned long x);

/* ---- 样例表：超集形态的 `example` ----
 *
 * 每行是一条**封闭**样例：实参与期望全是常量。`dp_oracle.c` 把它展开成逐条求值；
 * 一条都没跑起来算失败（与 verify 的"零可执行样例算未通过"同一条纪律）。
 * 列：编号 | 调用 | 期望 | 这条在钉什么
 */
#define DP_EXAMPLES(X)                                                                        \
  X(1, dec_create_result(true, 0), 0, "新建成功 -> 无错误，成功投影接手")                    \
  X(2, dec_create_result(false, 0), DP_ERR_CONFLICT, "已存在 -> Conflict（P16 那一局）")     \
  X(3, dec_create_result(false, DP_ERR_IO), DP_ERR_IO, "底层先报错时错误优先，不许被冲突盖掉") \
  X(4, dec_port_usable(0), false, "端口 0 不可用 -> 拒绝启动")                               \
  X(5, dec_port_usable(1), true, "下界：1 可用")                                             \
  X(6, dec_port_usable(65535), true, "上界：65535 可用")                                     \
  X(7, dec_port_usable(65536), false, "越界：65536 不可用")

#endif /* DP_DECIDE_H */
