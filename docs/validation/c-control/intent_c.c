/* C 侧的意图执行器：与 code/webapi/intents.dlt 同一批 6 条样例，逐条对齐。
 *
 * 这 14 行是 C 里"可执行意图"的全部载体 —— 但它跑的是 decisions.c 那份手抄，
 * 不是服务里真正执行的那两份（见 exp3 结论）。 */
#include "decisions.h"

#include <stdio.h>

static int fails = 0;

#define CK(expr, want) do { \
  bool got_ = (expr); \
  if (got_ != (want)) { \
    printf("  FAIL  %s == %s（实际 %s）\n", #expr, (want) ? "true" : "false", got_ ? "true" : "false"); \
    fails++; \
  } else { \
    printf("  ok    %s == %s\n", #expr, (want) ? "true" : "false"); \
  } \
} while (0)

int main(void) {
  CK(requires_auth(false, false), false);
  CK(requires_auth(false, true), false);
  CK(requires_auth(true, true), false);
  CK(requires_auth(true, false), true);
  CK(create_ok(true), false);
  CK(create_ok(false), true);
  printf("intent_c: 6 条样例，%d 条挂\n", fails);
  return fails != 0 ? 1 : 0;
}
