/* 决定逻辑（从运行时里手抄出来的两份，见 exp3）。
 *
 * requires_auth 对应 rt_http.c 的鉴权段（cfg->token.n > 0u && !is_health）；
 * create_ok 对应 rt_blob.c:356 dp_blob_create 的"已存在返回 false"。
 * 手抄 = 同一语义第二次存在，编译器与运行时都不知道这两份和那两份必须一致。
 */
#ifndef DP_DECISIONS_H
#define DP_DECISIONS_H

#include <stdbool.h>

bool requires_auth(bool token_set, bool is_health);
bool create_ok(bool exists);

#endif
