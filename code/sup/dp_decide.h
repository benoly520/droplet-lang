/* Droplet：纯决定 + 样例表（用户所有）。
 *
 * 这一层的用途：把"服务会怎么判"放在能被封闭样例求值的地方，而不是埋进运行时。
 * 与常见做法的差别是全部要点：样例验的必须**就是**服务链接的那一份，
 * 不是运行时条件的手抄 —— `dp_sup.h` 的 `dp_created()` 与 `dp_cfg.c` 的
 * `dp_load_port()` 调的就是这些函数，所以样例翻面 = 服务翻面（接线检查在守这条）。
 *
 * 边界（不许漂）：住在这里的只有**纯决定** —— 函数体里只有条件与常量，
 * 不碰环境变量、不碰文件系统、不碰 arena。读旧版本、读 header、算 ETag 仍在运行时，
 * 读完之后剩下的是判断，判断住在这里（条件请求、鉴权、body 上限都已搬出）。
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

/* 鉴权投影：运行时只负责从 header 里取出“有没有 / 对不对”，放不放行在这里。
 * 两条旁路必须写成显式条件，否则样例钉不住：
 *   - 没配 token（空串）= 不鉴权（这是表里的默认值，不是遗漏）；
 *   - /health 公开，便于探活。
 * token_configured 为真时，必须“带了 token 且相符”才放行。 */
bool dec_auth_ok(bool token_configured, bool is_health, bool header_present, bool header_matches);

/* body 上限：等于上限允许，超一个字节就拒绝（413）。 */
bool dec_body_too_large(size_t n, size_t max);

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
    "读旧版本失败时错误优先，不许被 412 盖掉")                                                \
  X(12, dec_auth_ok(false, false, false, false), true, "没配 token -> 不鉴权")                \
  X(13, dec_auth_ok(true, true, false, false), true, "/health 公开：配了 token 也放行")        \
  X(14, dec_auth_ok(true, false, false, false), false, "配了 token 且没带 -> 401")             \
  X(15, dec_auth_ok(true, false, true, false), false, "带了但不相符 -> 401")                   \
  X(16, dec_auth_ok(true, false, true, true), true, "带了且相符 -> 放行")                      \
  X(17, dec_body_too_large(8388608, 8388608), false, "等于上限 -> 允许（边界含在区间内）")     \
  X(18, dec_body_too_large(8388609, 8388608), true, "超一个字节 -> 413")                       \
  X(19, dec_body_too_large(0, 8388608), false, "空 body -> 允许")

#endif /* DP_DECIDE_H */
