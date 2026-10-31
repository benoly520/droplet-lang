/* 手写 C 版示例服务（对照组 A / 第一版：AI 直接照 C 写，不过任何语言）。
 *
 * 链接的库与 droplet 生成的二进制**完全相同**：rt_core.c / rt_http.c / rt_blob.c /
 * rt_ver.c（1472 行运行时，两边都是白盒共享，不算这门语言的功劳）。差别只在
 * dispatcher、配置加载、五个 handler 和状态码由人手写。
 *
 * 这一版是"第一遍就写出来"的样子，没有刻意对齐契约细节；
 * 对齐后的版本见 service2.c。两版都要跑，是为了分清"写得不好"与"结构上做不到"。
 */
#include "rt.h"

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

static void reply(dp_response* out, unsigned status, const char* body) {
  out->status = (uint16_t)status;
  out->content_type = dp_str_lit("application/json");
  if (body != NULL) {
    out->body.p = (const uint8_t*)body;
    out->body.n = strlen(body);
  } else {
    out->body = dp_bytes_lit("");
  }
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

static void reply_err(dp_response* out, int tag) {
  unsigned st = status_of(tag);
  const char* msg = "error";
  if (st == 404u) { msg = "not found"; }
  else if (st == 409u) { msg = "already exists"; }
  else if (st == 412u) { msg = "precondition failed"; }
  else if (st == 500u) { msg = "io error"; }
  char buf[128];
  int n = snprintf(buf, sizeof buf, "{\"error\":\"%s\"}", msg);
  if (n < 0) {
    n = 0;
  }
  reply(out, st, n > 7 ? buf : NULL);
}

/* ---- 五个 handler ---- */

static int h_health(dp_arena* a, const dp_request* rq, dp_response* out) {
  (void)a;
  (void)rq;
  reply(out, 200, "{\"ok\":true}");
  return 0;
}

static int h_blob_get(dp_arena* a, const dp_request* rq, dp_response* out) {
  int err = 0;
  dp_bytes body = dp_blob_read(a, dp_str_from_cstr(env_root), rq->key, &err);
  if (err != 0) {
    reply_err(out, err);
    return 0;
  }
  out->status = 200;
  out->content_type = dp_str_lit("application/json");
  out->body = body;
  return 0;
}

static int h_blob_put(dp_arena* a, const dp_request* rq, dp_response* out) {
  /* If-Match：这一版按"该写的都写了"的样子手写一遍。漏写会怎样，见 exp1。 */
  if (rq->if_match.n > 0u) {
    int pc = dp_precondition_check(a, dp_str_from_cstr(env_root), rq->key, rq->if_match);
    if (pc != 0) {
      reply_err(out, pc);
      return 0;
    }
  }
  dp_blob_backup(a, dp_str_from_cstr(env_root), rq->key);
  int err = 0;
  dp_blob_write(a, dp_str_from_cstr(env_root), rq->key, rq->body, &err);
  if (err != 0) {
    reply_err(out, err);
    return 0;
  }
  dp_ver_commit_soon(dp_str_from_cstr(env_root), rq->key);
  reply(out, 204, NULL);
  return 0;
}

static int h_keys_list(dp_arena* a, const dp_request* rq, dp_response* out) {
  (void)rq;
  int err = 0;
  dp_bytes body = dp_blob_list(a, dp_str_from_cstr(env_root), &err);
  if (err != 0) {
    reply_err(out, err);
    return 0;
  }
  out->status = 200;
  out->content_type = dp_str_lit("application/json");
  out->body = body;
  return 0;
}

static int h_keys_create(dp_arena* a, const dp_request* rq, dp_response* out) {
  int err = 0;
  dp_blob_create(a, dp_str_from_cstr(env_root), rq->key, &err);
  if (err != 0) {
    reply_err(out, err);
    return 0;
  }
  reply(out, 201, "");
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
  reply_err(out, (int)DP_ERR_NOT_ALLOWED);
  return 0;
}

int main(void) {
  env_root = (char*)cfg_get("APP_ROOT", env_root);
  env_token = (char*)cfg_get("APP_TOKEN", env_token);
  env_bind = (char*)cfg_get("APP_BIND", env_bind);
  env_port = (int)cfg_num("APP_PORT", (long)env_port);
  env_max_body = (size_t)cfg_num("APP_MAX_BODY", (long)env_max_body);
  /* DP_LOG_LEVEL：读一个没有任何地方声明过的变量。能不能被发现，见 exp2。 */
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
