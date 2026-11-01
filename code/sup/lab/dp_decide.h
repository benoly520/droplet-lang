/* Lab 题面：两条纯决定（本仓库里没有实现，也不在任何文档里）。
 *
 * 纪律与主仓库一致：函数体里只能有条件与常量；不读环境变量、不碰文件系统、
 * 不碰 arena、不做 IO。样例即判据。
 */
#ifndef DP_DECIDE_H
#define DP_DECIDE_H

#include <stdbool.h>
#include <stddef.h>

/* 重试等待秒数：第 attempt 次失败后等多久再试（attempt 从 0 计）。
 *   - attempt >= max_attempts -> 0（不再重试）；
 *   - 否则 2^attempt 秒，但上限 60 秒；
 *   - 32 位、64 位 unsigned 上都按这个定义。 */
unsigned dec_retry_delay(unsigned attempt, unsigned max_attempts);

/* 缓存是否新鲜：age < ttl 才算新鲜；相等算过期。 */
bool dec_fresh(unsigned long age_s, unsigned long ttl_s);

#define DP_EXAMPLES(X)                                                          \
  X(1, dec_retry_delay(0, 3), 1, "第一次失败等 1 秒")                            \
  X(2, dec_retry_delay(1, 3), 2, "第二次失败等 2 秒")                            \
  X(3, dec_retry_delay(2, 3), 4, "第三次失败等 4 秒")                            \
  X(4, dec_retry_delay(3, 3), 0, "达到上限 -> 不再重试")                         \
  X(5, dec_retry_delay(9, 3), 0, "超过上限也 -> 0")                              \
  X(6, dec_retry_delay(5, 10), 32, "2^5 = 32")                                   \
  X(7, dec_retry_delay(6, 10), 60, "2^6 = 64，截到 60")                          \
  X(8, dec_retry_delay(7, 10), 60, "更远也只到 60")                              \
  X(9, dec_fresh(0, 60), true, "刚写入 -> 新鲜")                                 \
  X(10, dec_fresh(59, 60), true, "差一秒到期 -> 仍新鲜")                         \
  X(11, dec_fresh(60, 60), false, "恰好到期 -> 过期（边界不含）")                \
  X(12, dec_fresh(61, 60), false, "过期之后 -> 过期")

#endif /* DP_DECIDE_H */
