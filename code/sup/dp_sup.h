/* Droplet 超集层（SPEC §0.8，v0.30）：源码是合法 C11，超集只加一样东西 —— 声明表。
 *
 * 用法（唯一形态，见 code/sup/blobd.c）：
 *     #define DP_ROUTES(X) X(GET, "/health", h_health, DP_OK_JSON, DP_AUTH_PUBLIC, DP_GUARD_NONE, "...")
 *     #define DP_ENVS(X)   X("APP_ROOT", root, str, "blobs")
 *     #include "dp_sup.h"
 * 表由预处理器展开成三处：handler 原型、配置结构与加载、请求分发器。
 *
 * 为什么这样就能不要语言层（上一轮 C 对照组的结论）：
 *
 *   ① **表与实现不可漂移，且是编译器把守的，不是正则**：
 *      表里列了而没有函数 -> GCC 报 "declared static but never defined"；
 *      写了函数而表里没列 -> static 未使用 -> -Wunused-function，-Werror 下失败。
 *      两个方向都是**构建失败**，不依赖任何模式匹配，因此重构打不穿它
 *      （对照组那个 45 行正则核对器一次重构就废，实测记在 SPEC 附录 D.10.2）。
 *   ② 状态码、鉴权、If-Match 位置只存在于表的展开里；handler 源码不出现数字。
 *   ③ 未声明的环境变量读取 = 链接期错误（本文件末尾把 getenv 改名成不存在的符号）。
 *   ④ 上一轮输掉的那一局在这里被构造掉：dp_blob_create 的 bool 用户拿不到裸的它，
 *      dp_created() 已经把 false 翻译成 DP_ERR_CONFLICT —— 决定只存在一处，
 *      不需要对照组手抄的 decisions.h（那份"第二个存在"没有任何东西把守）。
 *
 * 运行时零改动：rt.h / rt_*.c 原样 include，H8 的 7 行余量一行没动。
 */
#ifndef DP_SUP_H
#define DP_SUP_H

#include "rt.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifndef DP_ROUTES
#error "超集要求先定义 DP_ROUTES：没有路由表就没有可声明的面"
#endif
#ifndef DP_ENVS
#error "超集要求先定义 DP_ENVS：配置项必须声明，含默认值"
#endif

/* ---- 成功投影：数字只出现在 dp_project() 一处 ---- */

typedef enum {
  DP_OK_JSON,       /* 200 带 body */
  DP_OK_CREATED,    /* 201 空 body */
  DP_OK_NO_CONTENT, /* 204 空 body，运行时按 RFC 7230 不发 Content-Length */
  DP_OK_SENTINEL_   /* 占位：将来加枚举时 -Wswitch 会响，漏一条就编译不过 */
} dp_ok;

/* ---- 预条件守卫：声明了才展开，位置不在人手里 ---- */

#define DP_GUARD_NONE  /* 不放行"忘了写"：表里必须显式写一个，理由写在最后一列 */
#define DP_GUARD_IF_MATCH                                                     \
  if (rq->if_match.n > 0u) {                                                   \
    int _g = dp_precondition_check(a, dp_conf.root, rq->key, rq->if_match);      \
    if (_g != 0) {                                                             \
      return _g;                                                               \
    }                                                                          \
  }

/* ---- 鉴权投影：实现只在 rt_http.c 一处，这里只进契约 ---- */

typedef enum {
  DP_AUTH_PUBLIC, /* 只有 /health 公开 */
  DP_AUTH_TOKEN
} dp_auth;

/* ---- 配置：字段名由 DP_ENVS 展开，环境变量名只出现在表里 ---- */

typedef struct {
#define DP_ENV_ROW(NAME, FIELD, KIND, DFLT) dp_str FIELD;
  DP_ENVS(DP_ENV_ROW)
#undef DP_ENV_ROW
  uint16_t port;
  size_t max_body;
} dp_conf_sup;

/* 名字不能叫 dp_cfg：rt.h 里有同名 typedef，普通标识符命名空间会把它遮掉。 */
static dp_conf_sup dp_conf;

/* 真实 getenv 只在守卫生效（本文件末尾）之前被这几个函数用到。 */
static dp_str dp_env_str(const char* name, const char* dflt) {
  const char* v = getenv(name);
  return dp_str_from_cstr((v != NULL && v[0] != '\0') ? v : dflt);
}

static uint16_t dp_env_port(const char* name, uint16_t dflt) {
  const char* v = getenv(name);
  if (v == NULL || v[0] == '\0') {
    return dflt;
  }
  char* end = NULL;
  unsigned long x = strtoul(v, &end, 10);
  if (end == v || *end != '\0' || x == 0ul || x > 65535ul) {
    fprintf(stderr, "droplet-sup: 配置 %s=%s 不在 1..65535\n", name, v);
    return 0u;
  }
  return (uint16_t)x;
}

static size_t dp_env_size(const char* name, size_t dflt) {
  const char* v = getenv(name);
  if (v == NULL || v[0] == '\0') {
    return dflt;
  }
  char* end = NULL;
  unsigned long x = strtoul(v, &end, 10);
  if (end == v || *end != '\0') {
    fprintf(stderr, "droplet-sup: 配置 %s=%s 不是整数，用默认值\n", name, v);
    return dflt;
  }
  return (size_t)x;
}

static void dp_cfg_load(void) {
#define DP_ENV_ROW(NAME, FIELD, KIND, DFLT) dp_conf.FIELD = dp_env_str(NAME, DFLT);
  DP_ENVS(DP_ENV_ROW)
#undef DP_ENV_ROW
  dp_conf.port = dp_env_port("APP_PORT", 8787u);
  dp_conf.max_body = dp_env_size("APP_MAX_BODY", 8u * 1024u * 1024u);
}

/* ---- handler 原型由表生成：表与实现两个方向都不可漂移 ---- */

/* handler 的签名由表生成，只有两种能力：产出 body，或返回错误 tag。
 * dp_response 不在参数里 —— 状态码在结构上就写不了（超集实测的第一个逃逸口，
 * 见 SPEC §0.8.4：让 handler 拿到 out 指针，等于把"数字不进源码"降级成约定）。*/
#define DP_ROUTE_ROW(METHOD, PATH, FN, OK, AUTH, GUARD, WHY) \
  static int FN(dp_arena* a, const dp_request* rq, dp_bytes* body);
DP_ROUTES(DP_ROUTE_ROW)
#undef DP_ROUTE_ROW

/* ---- bool 收成错误码：上一轮输掉的那一局 ---- */

static int dp_created(dp_arena* a, dp_str key) {
  int err = 0;
  bool fresh = dp_blob_create(a, dp_conf.root, key, &err);
  if (err != 0) {
    return err;
  }
  return fresh ? 0 : (int)DP_ERR_CONFLICT;
}

/* ---- 成功投影：数字只在这一个 switch 里 ---- */

static void dp_project(dp_response* out, dp_ok ok, dp_bytes body) {
  out->content_type = dp_str_lit("application/json");
  switch (ok) {
    case DP_OK_JSON:
      out->status = 200u;
      out->body = body; /* 唯一能把 body 送上线的路径 */
      break;
    case DP_OK_CREATED:
      out->status = 201u;
      out->body = dp_bytes_lit(""); /* 成功投影声明了"无体"，handler 写了也没用 */
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

static int dp_dispatch(dp_arena* a, const dp_request* rq, dp_response* out) {
#define DP_ROUTE_ROW(METHOD, PATH, FN, OK, AUTH, GUARD, WHY)                            \
  if (dp_str_eq(rq->method, dp_str_lit(METHOD)) && dp_str_eq(rq->path, dp_str_lit(PATH))) { \
    GUARD                                                                                \
    dp_bytes _body = dp_bytes_lit("");                                                     \
    int _rc = FN(a, rq, &_body);                                                           \
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

/* ---- 入口胶水：main 需要的两件事 ---- */

static int dp_serve_sup(void) {
  dp_cfg cfg;
  memset(&cfg, 0, sizeof cfg);
  cfg.port = dp_conf.port;
  cfg.bind = dp_conf.bind;
  cfg.root = dp_conf.root;
  cfg.token = dp_conf.token;
  cfg.max_body = dp_conf.max_body;
  if (cfg.port == 0u) {
    return 1;
  }
  return dp_serve(dp_dispatch, &cfg);
}

/* ---- 纪律③：目标 ↔ 验收断言绑定 ----
 *
 * DP_GOALS 每行的 PROVEN 是 accept-c.sh 的断言编号（逗号分隔）；写 "" 表示人明确
 * 承认"这条没被证明"。contract-sup.py 拿它和脚本里的编号对账：
 * 编号不存在 = 声明在承诺一个不存在的检查 -> 构建失败；
 * 写 "" = 不失败，但进未证清单。沉默不是决定，这条也是。
 */
#ifndef DP_GOALS
#define DP_GOALS(X)
#endif

#define DP_GOAL_ROW(ID, TEXT, PROVEN) static const char dp_goal_##ID[] = TEXT "\x01" PROVEN;
DP_GOALS(DP_GOAL_ROW)
#undef DP_GOAL_ROW

/* ---- 纪律①的链接期形态：本行之后 getenv 不存在 ----
 *
 * 运行时自己的三个环境变量（DP_SNAPSHOT_KEEP / DP_COMMIT_DEBOUNCE / DP_COMMIT_CMD）
 * 在 rt_*.c 里，那些 TU 不 include 本头，所以不受影响 —— 它们属于运行时的声明面，
 * 另算（SPEC §0.8.4 记这条边界）。
 */
#define getenv(name) dp_env_not_declared_in_DP_ENVS(name)

#endif /* DP_SUP_H */
