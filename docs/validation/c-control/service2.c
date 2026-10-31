/* 手写 C 版示例服务（对照组 A / 第二版：把 wire 契约补到 19 条全过）。
 *
 * 与 service.c 的差别只有一处：错误响应不再携带 body（D2/R2「错误不带表示」）。
 * 这一版存在的意义是**最强的对照组**：它是功能全对的 C，四条纪律要在它身上试，
 * 而不是在一个写坏的 C 上试 —— 写坏的 C 说明不了"结构上做不到"。
 *
 * 状态码仍然全部是字面量写在源码里：这不是疏忽，是 C 里唯一能表达
 * "这条路由投 204" 的地方（dp_response 只有一个 status 字段，没有别处可声明）。
 */
#include "rt.h"
#include "decisions.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* ---- 配置：环境变量名直接写在代码里 ---- */

static char* env_root      = "blobs";
static char* env_token     = "";
static char* env_bind      = "0.0.0.0";
static int   env_port      = 8787;
static size_t env_max_body = 8u * 1024u * 1024u;

static const char* cfg_get(const char* name, const char* dflt) {
  const char* v = getenv(name);
  return (v != NULL && v[0] != '\0') ? v : dflt;
}

static long cfg_num(const char* name, long dflt) {
  const char* v = getenv(name);
  if (v == NULL || v[0] == '\0') {
    return dflt;
  }
  char* end = NULL;
  long x = strtol(v, &end, 10);
  if (end == v || end == NULL || *end != '\0') {
    fprintf(stderr, "blobd-c: 配置 %s=%s 不是整数，用默认值\n", name, v);
    return dflt;
  }
  return x;
}

/* ---- 小工具 ---- */

#define IS(s, lit) ((s).n == sizeof(lit) - 1u && memcmp((s).p, lit, (s).n) == 0)

static void reply(dp_response* out, unsigned status) {
  out->status = (uint16_t)status;
  out->content_type = dp_str_lit("application/json");
  out->body = dp_bytes_lit("");
}

static void reply_body(dp_response* out, unsigned status, const char* body) {
  reply(out, status);
  out->body.p = (const uint8_t*)body;
  out->body.n = strlen(body);
}

/* dp_blob_* 的 err 出参用的是 DP_ERR_* tag，这里映射成状态码。 */
static unsigned status_of(int tag) {
  switch ((unsigned)tag) {
    case DP_ERR_NOT_FOUND:           return 404;
    case DP_ERR_BAD_REQUEST:         return 400;
    case DP_ERR_CONFLICT:            return 409;
    case DP_ERR_NOT_ALLOWED:         return 405;
    case DP_ERR_PRECONDITION_FAILED: return 412;
    default:                         return 500;
  }
}

/* ---- 决定逻辑：requires_auth 在 rt_http.c 的请求循环里（第 465 行），
 * create_ok 在 rt_blob.c 的 create 里。这两份 C 实现抽到 decisions.c 是为了让
 * 样例够得着（exp3）—— 抽出来这一步没有任何东西把守，且服务里跑的是那两份，
 * 样例验的是这一份复制品。 ---- */

/* ---- 五个 handler ---- */

static int h_health(dp_arena* a, const dp_request* rq, dp_response* out) {
  (void)a;
  (void)rq;
  reply_body(out, 200, "{\"ok\":true}");
  return 0;
}

static int h_blob_get(dp_arena* a, const dp_request* rq, dp_response* out) {
  int err = 0;
  dp_bytes body = dp_blob_read(a, dp_str_from_cstr(env_root), rq->key, &err);
  if (err != 0) {
    reply(out, status_of(err));
    return 0;
  }
  out->status = 200;
  out->content_type = dp_str_lit("application/json");
  out->body = body;
  return 0;
}

static int h_blob_put(dp_arena* a, const dp_request* rq, dp_response* out) {
  /* If-Match 守卫：写路由必须跑，位置由人决定。删掉这三行会怎样，见 exp1。 */
  if (rq->if_match.n > 0u) {
    int pc = dp_precondition_check(a, dp_str_from_cstr(env_root), rq->key, rq->if_match);
    if (pc != 0) {
      reply(out, status_of(pc));
      return 0;
    }
  }
  dp_blob_backup(a, dp_str_from_cstr(env_root), rq->key);
  int err = 0;
  dp_blob_write(a, dp_str_from_cstr(env_root), rq->key, rq->body, &err);
  if (err != 0) {
    reply(out, status_of(err));
    return 0;
  }
  dp_ver_commit_soon(dp_str_from_cstr(env_root), rq->key);
  reply(out, 204);
  return 0;
}

static int h_keys_list(dp_arena* a, const dp_request* rq, dp_response* out) {
  (void)rq;
  int err = 0;
  dp_bytes body = dp_blob_list(a, dp_str_from_cstr(env_root), &err);
  if (err != 0) {
    reply(out, status_of(err));
    return 0;
  }
  out->status = 200;
  out->content_type = dp_str_lit("application/json");
  out->body = body;
  return 0;
}

static int h_keys_create(dp_arena* a, const dp_request* rq, dp_response* out) {
  char path[1024];
  int n = snprintf(path, sizeof path, "%s/%.*s", env_root, (int)rq->key.n, rq->key.p);
  bool exists = false;
  if (n > 0 && n < (int)sizeof path) {
    FILE* f = fopen(path, "rb");
    if (f != NULL) {
      exists = true;
      fclose(f);
    }
  }
  if (!create_ok(exists)) {
    reply(out, 409);
    return 0;
  }
  int err = 0;
  dp_blob_create(a, dp_str_from_cstr(env_root), rq->key, &err);
  if (err != 0) {
    reply(out, status_of(err));
    return 0;
  }
  reply(out, 201);
  return 0;
}

/* ---- dispatcher：路由表就是这串 if ---- */

static int handle(dp_arena* a, const dp_request* rq, dp_response* out) {
  if (IS(rq->method, "GET") && IS(rq->path, "/health")) {
    return h_health(a, rq, out);
  }
  if (IS(rq->method, "GET") && IS(rq->path, "/v1/blob")) {
    return h_blob_get(a, rq, out);
  }
  if (IS(rq->method, "PUT") && IS(rq->path, "/v1/blob")) {
    return h_blob_put(a, rq, out);
  }
  if (IS(rq->method, "GET") && IS(rq->path, "/v1/keys")) {
    return h_keys_list(a, rq, out);
  }
  if (IS(rq->method, "POST") && IS(rq->path, "/v1/keys")) {
    return h_keys_create(a, rq, out);
  }
  reply(out, status_of((int)DP_ERR_NOT_ALLOWED));
  return 0;
}

int main(void) {
  env_root = (char*)cfg_get("APP_ROOT", env_root);
  env_token = (char*)cfg_get("APP_TOKEN", env_token);
  env_bind = (char*)cfg_get("APP_BIND", env_bind);
  env_port = (int)cfg_get("APP_PORT", "8787")[0] ? (int)cfg_num("APP_PORT", 8787) : 8787;
  env_max_body = (size_t)cfg_num("APP_MAX_BODY", (long)env_max_body);
  /* 读一个没有任何地方声明过的变量：能不能被发现，见 exp2。 */
  const char* dbg = getenv("DP_LOG_LEVEL");
  if (dbg != NULL && dbg[0] != '\0') {
    fprintf(stderr, "blobd-c: log level %s\n", dbg);
  }

  dp_cfg c;
  memset(&c, 0, sizeof c);
  c.port = (uint16_t)env_port;
  c.bind = dp_str_from_cstr(env_bind);
  c.root = dp_str_from_cstr(env_root);
  c.token = dp_str_from_cstr(env_token);
  c.max_body = env_max_body;
  return dp_serve(handle, &c);
}
