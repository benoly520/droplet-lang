/* Droplet：纯决定 + 样例表（用户所有）。
 *
 * 这一层的用途：把"服务会怎么判"放在能被封闭样例求值的地方，而不是埋进运行时。
 * 与常见做法的差别是全部要点：样例验的必须**就是**服务链接的那一份，
 * 不是运行时条件的手抄 —— `dp_sup.h` 的 `dp_created()` 与 `dp_cfg.c` 的
 * `dp_load_port()` 调的就是这些函数，所以样例翻面 = 服务翻面（接线检查在守这条）。
 *
 * 边界（不许漂）：住在这里的只有**纯决定** —— 函数体里只有条件与常量，
 * 不碰环境变量、不碰文件系统、不碰 arena。读旧版本仍在运行时，读完之后
 * 剩下的是判断，判断住在这里。鉴权与 body 上限还没有搬出来（见 docs/SPEC.md §5）。
 */
#ifndef DP_DECIDE_H
#define DP_DECIDE_H

#include <stdbool.h>
#include <stddef.h>

#include "rt.h"

/* 创建的结果投影：错误优先，其次"已存在" -> Conflict。
 * "重复 POST 回 201"那个 bug 的根因是把 dp_blob_create 的 bool 丢了；
 * 这里把它收成一条可验的形状。 */
int dec_create_result(bool fresh, int err);

/* 端口可用区间。返回 false 时 `dp_cfg.c` 留 0，`dp_serve_sup()` 因此拒绝启动，
 * 而不是猜一个端口。 */
bool dec_port_usable(unsigned long x);

/* 条件请求的结果投影：运行时只负责读旧版本与算 ETag，剩下的判断在这里。
 * 三个入参都是"已经发生的事实"，不是"要去查的东西"：
 *   read_err   读旧版本的返回（0 / DP_ERR_NOT_FOUND / 其他）
 *   exists     旧版本是否存在（与 read_err 一致，单独传是为了让样例能钉住这条）
 *   tag_matches 算出来的 ETag 与 If-Match 是否相等
 * 目标不存在时是 412 不是 404：资源本来就在要建的路径上。 */
int dec_precondition_result(int read_err, bool exists, bool tag_matches);

/* ---- 样例表：封闭样例当 oracle ----
 *
 * 每行是一条**封闭**样例：实参与期望全是常量。`dp_oracle.c` 把它展开成逐条求值；
 * 一条都没跑起来算失败，崩溃单列、不算通过。
 * 列：编号 | 调用 | 期望 | 这条在钉什么
 */
#define DP_EXAMPLES(X)                                                                        \
  X(1, dec_create_result(true, 0), 0, "新建成功 -> 无错误，成功投影接手")                    \
  X(2, dec_create_result(false, 0), DP_ERR_CONFLICT, "已存在 -> Conflict")                  \
  X(3, dec_create_result(false, DP_ERR_IO), DP_ERR_IO, "底层先报错时错误优先，不许被冲突盖掉") \
  X(4, dec_port_usable(0), false, "端口 0 不可用 -> 拒绝启动")                               \
  X(5, dec_port_usable(1), true, "下界：1 可用")                                             \
  X(6, dec_port_usable(65535), true, "上界：65535 可用")                                     \
  X(7, dec_port_usable(65536), false, "越界：65536 不可用")                                   \
  X(8, dec_precondition_result(0, true, true), 0, "旧版本存在且 ETag 相符 -> 放行")           \
  X(9, dec_precondition_result(0, true, false), DP_ERR_PRECONDITION_FAILED,                  \
    "ETag 不符 -> 412")                                                                       \
  X(10, dec_precondition_result(DP_ERR_NOT_FOUND, false, false),                             \
    DP_ERR_PRECONDITION_FAILED, "目标不存在 -> 412，不是 404")                                \
  X(11, dec_precondition_result(DP_ERR_IO, false, false), DP_ERR_IO,                         \
    "读旧版本失败时错误优先，不许被 412 盖掉")

#endif /* DP_DECIDE_H */
