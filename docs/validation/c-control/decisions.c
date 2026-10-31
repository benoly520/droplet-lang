/* 决定逻辑的实现 —— 手抄自运行时，逐字对照 rt_http.c / rt_blob.c 的条件表达式。
 * 这里就是 exp3 的对象：C 要让样例够得着决定，必须先做这样一次手抄。 */
#include "decisions.h"

bool requires_auth(bool token_set, bool is_health) {
  if (token_set) {
    if (is_health) {
      return false;
    }
    return true;
  }
  return false;
}

bool create_ok(bool exists) {
  if (exists) {
    return false;
  }
  return true;
}
