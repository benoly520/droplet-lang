/* Droplet 超集：样例 oracle 的执行器（机制所有，v0.32 / P18 第 1 条）。
 *
 * 把 DP_EXAMPLES 展开成逐条求值。与 `droplet verify` 对齐的四条纪律：
 *   ① 每条样例都报 PASS / FAIL，不汇总成"通过"；
 *   ② 零条可执行样例 = 未通过（没有证据不等于有证据）。空表在 C11 里本就编不过，
 *      所以真正的门禁在 oracle-sup.sh：`--list` 出 0 行即整轮失败；
 *   ③ **一条样例一个进程**（`--only <编号>`）：崩溃只丢掉这一条，
 *      而且崩溃本身是可比较的结果 —— 这条是从 v0.24 的 `explore` 那里搬过来的；
 *   ④ 样例表就是数据：`--list` 导出编号与文本，供 contract-sup.py 对账。
 *
 * 用法：
 *   dp_oracle --list            导出样例表（编号 \t 调用 \t 期望 \t 说明）
 *   dp_oracle --only N          只跑第 N 条：PASS 退 0 / FAIL 退 1；崩溃由信号退出，
 *                               shell 把它单列成"崩溃"，不当成通过
 *   dp_oracle                   本进程内全跑一遍，给人手看；批量判定请用 oracle-sup.sh
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "dp_decide.h"

#ifndef DP_EXAMPLES
#error "本层要求先定义 DP_EXAMPLES：没有样例表就没有 oracle"
#endif

typedef struct {
  int id;
  const char* call;
  const char* want;
  long (*got)(void);
  long expect;
  const char* why;
} dp_example;

/* 第一遍：每行生成一个取值函数（不能在初始化列表里定义函数）。 */
#define DP_EX_DECL(ID, CALL, EXPECT, WHY) static long dp_got_##ID(void) { return (long)(CALL); }
DP_EXAMPLES(DP_EX_DECL)
#undef DP_EX_DECL

/* 第二遍：同一张表生成注册数组，期望值与调用文本都留在表里。 */
#define DP_EX_ROW(ID, CALL, EXPECT, WHY) \
  { ID, #CALL, #EXPECT, dp_got_##ID, (long)(EXPECT), WHY },
static const dp_example dp_examples[] = {
    DP_EXAMPLES(DP_EX_ROW)
};
#undef DP_EX_ROW

#define DP_EX_N ((int)(sizeof dp_examples / sizeof dp_examples[0]))

static int run_one(const dp_example* e) {
  long g = e->got();
  if (g != e->expect) {
    printf("  FAIL  #%d %s == %s（实际 %ld）：%s\n", e->id, e->call, e->want, g, e->why);
    return 1;
  }
  printf("  ok    #%d %s == %s\n", e->id, e->call, e->want);
  return 0;
}

int main(int argc, char** argv) {
  if (DP_EX_N == 0) {
    printf("oracle: 样例表为空 —— 没有可执行样例，按未通过算\n");
    return 1;
  }

  if (argc > 1 && strcmp(argv[1], "--list") == 0) {
    for (int i = 0; i < DP_EX_N; i++) {
      printf("%d\t%s\t%s\t%s\n", dp_examples[i].id, dp_examples[i].call, dp_examples[i].want,
             dp_examples[i].why);
    }
    return 0;
  }

  if (argc > 2 && strcmp(argv[1], "--only") == 0) {
    int id = atoi(argv[2]);
    for (int i = 0; i < DP_EX_N; i++) {
      if (dp_examples[i].id == id) {
        return run_one(&dp_examples[i]) != 0 ? 1 : 0;
      }
    }
    printf("  FAIL  #%d 不在这张表里（样例表与跑批脚本之间漂移了）\n", id);
    return 1;
  }

  int fails = 0;
  for (int i = 0; i < DP_EX_N; i++) {
    fails += run_one(&dp_examples[i]);
  }
  printf("oracle: %d 条样例，%d 条挂\n", DP_EX_N, fails);
  return fails != 0 ? 1 : 0;
}
