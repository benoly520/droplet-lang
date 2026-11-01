/* Droplet 超集层。这里有"超集"两个字的字面意思：
 *
 *   **任何合法的 C11 程序，在把本层放进搜索路径之后，仍然编译、仍然按 C 的语义运行。**
 *
 * 上一版做不到这一点，因为它在文件末尾放了
 *      #define getenv(name) dp_env_not_declared_in_DP_ENVS(name)
 * 那行让一份完全合法的 C 程序（stdlib.h + 一次 getenv）在 include 本头之后直接编不过
 * —— 那是 incompatible extension，不是超集。本轮把它删了，未声明的效果检查挪到
 * **构建后的符号审计**（code/sup/audit-sup.sh，由 build.sh 自动调用）：
 *   所有读环境变量的代码都住在 dp_cfg.c 一个 TU 里，所以用户 TU 的 .o 里
 *   出现 getenv / environ 就等于读了一个没声明的东西。
 * 这比改名更强：E1（#undef getenv 后调真 libc）与 E6（extern char **environ）
 * 都必须在符号表里留下名字，改不了。兼容性实测见 code/sup/compat-sup.sh。
 *
 * 代价也记账（不是免费）：检查从"编译器现场"挪到"构建之后"，
 * 绕开 build.sh 直接 cc 就绕开了审计 —— 见 docs/SPEC.md §5 的 E7。
 *
 * 用法（唯一形态，见 code/sup/blobd.c）：
 *     #define DP_ROUTES(X) X("GET", "/health", h_health, DP_OK_JSON, DP_AUTH_PUBLIC, \
 *                                DP_GUARD_NONE, "探活不鉴权")
 *     #include "dp_env.h"          // 用户的配置表
 *     #include "dp_sup.h"
 */
#ifndef DP_SUP_H
#define DP_SUP_H

#include <string.h>
#include <stdio.h>

#include "dp_conf.h"
#include "dp_decide.h"

#ifndef DP_ROUTES
#error "本层要求先定义 DP_ROUTES：没有路由表就没有可声明的面（不 include 本头的普通 C 程序不受任何影响）"
#endif

/* ---- 成功投影：数字只出现在 dp_project() 一处 ---- */

typedef enum {
  DP_OK_JSON,         /* 200 带 body */
  DP_OK_CREATED,      /* 201 空 body */
  DP_OK_NO_CONTENT,   /* 204 空 body，运行时按 RFC 7230 不发 Content-Length */
  DP_OK_SENTINEL_     /* 占位：加枚举而漏改 switch 时 -Wswitch 会响，漏一条就编译不过 */
} dp_ok;

/* ---- 预条件守卫：声明了才展开，位置不在人手里 ----
 *
 * ROOT 现在是显式传进来的，不再是"展开时恰好能看见的某个全局"——
 * 本层要能在任何 TU、任何配置字段名下成立。 */
#define DP_GUARD_NONE
#define DP_GUARD_IF_MATCH(ROOT)                                                   \
  if (rq->if_match.n > 0u) {                                                      \
    int _g = dp_precondition_check(a, (ROOT), rq->key, rq->if_match);             \
    if (_g != 0) {                                                                \
      return _g;                                                                  \
    }                                                                             \
  }

/* ---- 鉴权投影：实现只在 rt_http.c 一处，这里只进契约 ---- */

typedef enum {
  DP_AUTH_PUBLIC, /* 只有 /health 公开 */
  DP_AUTH_TOKEN
} dp_auth;

/* ---- handler 原型由表生成：表与实现两个方向都不可漂移 ----
 *
 * handler 的签名由表生成，只有两种能力：产出 body，或返回错误 tag。
 * dp_response 不在参数里 —— 状态码在结构上就写不了（E3 已闭合）。
 * 表里列了而没有函数 -> GCC 报 "used but never defined"；
 * 写了函数而表里没列 -> static 未使用 -> -Wunused-function，-Werror 下失败。
 * 两个方向都是构建失败，由编译器把守，不依赖模式匹配，重构打不穿它。 */
#define DP_ROUTE_ROW(METHOD, PATH, FN, OK, AUTH, GUARD, WHY) \
  static int FN(dp_arena* a, const dp_request* rq, dp_bytes* body);
DP_ROUTES(DP_ROUTE_ROW)
#undef DP_ROUTE_ROW

/* ---- bool 收成错误码：上一轮输掉的那一局 ----
 *
 * 用 static inline：用户不创建 key 就不会有"定义了没用"的告警
 * —— 上一版这里是一条 -Werror 硬失败，同样属于"合法 C 被拒"。 */
static inline int dp_created(dp_arena* a, dp_str key) {
  int err = 0;
  bool fresh = dp_blob_create(a, dp_conf.root, key, &err);
  /* 决定住在 dp_decide.c —— 与 oracle 跑的是同一份，不是第二份抄本。 */
  return dec_create_result(fresh, err);
}

/* ---- 成功投影：数字只在这一个 switch 里 ---- */

static inline void dp_project(dp_response* out, dp_ok ok, dp_bytes body) {
  out->content_type = dp_str_lit("application/json");
  switch (ok) {
    case DP_OK_JSON:
      out->status = 200u;
      out->body = body; /* 唯一能把 body 送上线的路径 */
      break;
    case DP_OK_CREATED:
      out->status = 201u;
      out->body = dp_bytes_lit(""); /* 投影声明了"无体"，handler 写了也没用 */
      break;
    case DP_OK_NO_CONTENT:
      out->status = 204u;
      out->body = dp_bytes_lit("");
      break;
    case DP_OK_SENTINEL_:
      break;
  }
}

/* ---- 分发器：整张表展开，源码里不存在第二份路由 ---- */

static inline int dp_dispatch(dp_arena* a, const dp_request* rq, dp_response* out) {
#define DP_ROUTE_ROW(METHOD, PATH, FN, OK, AUTH, GUARD, WHY)                            \
  if (dp_str_eq(rq->method, dp_str_lit(METHOD)) && dp_str_eq(rq->path, dp_str_lit(PATH))) { \
    GUARD                                                                                \
    dp_bytes _body = dp_bytes_lit("");                                                    \
    int _rc = FN(a, rq, &_body);                                                          \
    if (_rc != 0) {                                                                       \
      return _rc;                                                                         \
    }                                                                                     \
    dp_project(out, (OK), _body);                                                         \
    (void)(AUTH); /* 鉴权实现只在 rt_http.c，这里只作为契约声明存在 */                     \
    return 0;                                                                             \
  }
  DP_ROUTES(DP_ROUTE_ROW)
#undef DP_ROUTE_ROW
  return (int)DP_ERR_NOT_ALLOWED;
}

/* ---- 入口胶水：main 需要的两件事（同样是 static inline，不用就不产生约束） ---- */

static inline int dp_serve_sup(void) {
  dp_cfg cfg;
  memset(&cfg, 0, sizeof cfg);
  cfg.port = dp_conf.port;
  cfg.bind = dp_conf.bind;
  cfg.root = dp_conf.root;
  cfg.token = dp_conf.token;
  cfg.max_body = dp_conf.max_body;
  if (cfg.port == 0u) {
    return 1; /* 端口加载失败 = 拒绝启动，不猜一个端口 */
  }
  return dp_serve(dp_dispatch, &cfg);
}

/* ---- 纪律③：目标 ↔ 验收断言绑定 ----
 *
 * DP_GOALS 每行的 PROVEN 是 accept-sup.sh 的断言编号（逗号分隔）；写 "" 表示人明确
 * 承认"这条没被证明"。contract-sup.py 拿它和脚本里的编号对账：
 * 编号不存在 = 声明在承诺一个不存在的检查 -> 退出码 1；
 * 写 "" = 不失败，但进未证清单。沉默不是决定，这条也是。 */
#ifndef DP_GOALS
#define DP_GOALS(X)
#endif

#define DP_GOAL_ROW(ID, TEXT, PROVEN) static const char dp_goal_##ID[] = TEXT "\x01" PROVEN;
DP_GOALS(DP_GOAL_ROW)
#undef DP_GOAL_ROW

#endif /* DP_SUP_H */
