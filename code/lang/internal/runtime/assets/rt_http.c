/* Droplet 运行时 · HTTP/1.1 单线程服务
 *
 * 设计取舍：M3 只做「一个连接一个请求，响应后关闭」，不开 keep-alive。
 * 这是文档 §9 约定的回退位：先保证正确与不可被打穿，连通性优化留到后面。
 *
 * 所有输入都按长度处理，不做 NUL 假设；header 总长、body 长度、单行长度都有硬上限。
 */
#include "rt.h"

#include <arpa/inet.h>
#include <errno.h>
#include <netinet/in.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/time.h>
#include <unistd.h>

#define DP_HDR_MAX 8192u
#define DP_LINE_MAX 4096u
#define DP_RESP_HEAD_MAX 512u
#define DP_IO_TIMEOUT_SEC 30

/* ---------- 小工具 ---------- */

static bool write_all(int fd, const void* p, size_t n) {
  const char* b = (const char*)p;
  size_t off = 0;
  while (off < n) {
    ssize_t w = write(fd, b + off, n - off);
    if (w < 0) {
      if (errno == EINTR) {
        continue;
      }
      return false;
    }
    off += (size_t)w;
  }
  return true;
}

static bool header_is(dp_str name, const char* lit) {
  size_t n = strlen(lit);
  if (name.n != n) {
    return false;
  }
  for (size_t i = 0; i < n; i++) {
    char a = name.p[i];
    char b = lit[i];
    if (a >= 'A' && a <= 'Z') {
      a = (char)(a - 'A' + 'a');
    }
    if (b >= 'A' && b <= 'Z') {
      b = (char)(b - 'A' + 'a');
    }
    if (a != b) {
      return false;
    }
  }
  return true;
}

static bool parse_size(dp_str s, size_t* out) {
  if (s.n == 0u || s.n > 19u) {
    return false;
  }
  size_t v = 0;
  for (size_t i = 0; i < s.n; i++) {
    if (s.p[i] < '0' || s.p[i] > '9') {
      return false;
    }
    v = v * 10u + (size_t)(s.p[i] - '0');
  }
  *out = v;
  return true;
}

/* ETag 只允许 [A-Za-z0-9_-]：既防响应头注入，也保证引号语义明确。 */
static bool etag_safe(dp_str v) {
  if (v.n == 0u || v.n > 64u) {
    return false;
  }
  for (size_t i = 0; i < v.n; i++) {
    char c = v.p[i];
    bool ok = (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') ||
              (c >= 'A' && c <= 'Z') || c == '_' || c == '-';
    if (!ok) {
      return false;
    }
  }
  return true;
}

static bool header_value_safe(dp_str v) {
  if (v.n > 128u) {
    return false;
  }
  for (size_t i = 0; i < v.n; i++) {
    if (v.p[i] == '\r' || v.p[i] == '\n') {
      return false;
    }
  }
  return true;
}

static const char* reason(uint16_t s) {
  switch (s) {
    case 200: return "OK";
    case 400: return "Bad Request";
    case 401: return "Unauthorized";
    case 404: return "Not Found";
    case 405: return "Method Not Allowed";
    case 408: return "Request Timeout";
    case 409: return "Conflict";
    case 412: return "Precondition Failed";
    case 413: return "Payload Too Large";
    case 500: return "Internal Server Error";
    case 501: return "Not Implemented";
    default: return "OK";
  }
}

static uint16_t status_for_tag(int tag) {
  switch (tag) {
    case (int)DP_ERR_NOT_FOUND: return 404;
    case (int)DP_ERR_BAD_REQUEST: return 400;
    case (int)DP_ERR_CONFLICT: return 409;
    default: return 500;
  }
}

/* ---------- 响应 ---------- */

static void send_response(int fd, uint16_t status, const char* ct, dp_bytes body, dp_str etag) {
  char head[DP_RESP_HEAD_MAX];
  int n = snprintf(head, sizeof head, "HTTP/1.1 %u %s\r\n", (unsigned)status, reason(status));
  if (n < 0) {
    return;
  }
  if (ct && n < (int)sizeof head) {
    n += snprintf(head + n, sizeof head - (size_t)n, "Content-Type: %s\r\n", ct);
  }
  if (n < (int)sizeof head) {
    n += snprintf(head + n, sizeof head - (size_t)n, "Content-Length: %zu\r\n", body.n);
  }
  if (etag_safe(etag) && n < (int)sizeof head) {
    n += snprintf(head + n, sizeof head - (size_t)n, "ETag: \"%.*s\"\r\n", (int)etag.n, etag.p);
  }
  if (n < (int)sizeof head) {
    n += snprintf(head + n, sizeof head - (size_t)n, "Connection: close\r\n\r\n");
  }
  if (n < 0 || n >= (int)sizeof head) {
    return;
  }
  if (!write_all(fd, head, (size_t)n)) {
    return;
  }
  if (body.n > 0u) {
    write_all(fd, body.p, body.n);
  }
}

static void send_status(int fd, uint16_t status, const char* msg) {
  dp_bytes body;
  body.p = (const uint8_t*)msg;
  body.n = msg ? strlen(msg) : 0u;
  send_response(fd, status, "text/plain; charset=utf-8", body, dp_str_lit(""));
}

/* ---------- 请求解析 ---------- */

typedef struct {
  dp_str method;
  dp_str target;
  dp_str content_length_raw;
  dp_str if_match;
  dp_str authorization;
  bool has_content_length;
  bool chunked;
} raw_head;

static size_t find_hdr_end(const char* b, size_t n) {
  for (size_t i = 0; i + 3u < n; i++) {
    if (b[i] == '\r' && b[i + 1] == '\n' && b[i + 2] == '\r' && b[i + 3] == '\n') {
      return i;
    }
  }
  return (size_t)-1;
}

static dp_str trim_ows(dp_str s) {
  while (s.n > 0u && (s.p[0] == ' ' || s.p[0] == '\t')) {
    s.p++;
    s.n--;
  }
  while (s.n > 0u && (s.p[s.n - 1u] == ' ' || s.p[s.n - 1u] == '\t')) {
    s.n--;
  }
  return s;
}

static bool parse_head(const char* buf, size_t hdr_end, raw_head* out) {
  memset(out, 0, sizeof *out);

  size_t line_end = 0;
  while (line_end < hdr_end && !(buf[line_end] == '\r' && buf[line_end + 1] == '\n')) {
    line_end++;
  }
  if (line_end == 0u || line_end > DP_LINE_MAX) {
    return false;
  }

  /* METHOD SP TARGET SP VERSION */
  size_t i = 0;
  while (i < line_end && buf[i] != ' ') {
    i++;
  }
  if (i == 0u || i >= line_end) {
    return false;
  }
  out->method.p = buf;
  out->method.n = i;

  size_t j = i + 1;
  size_t k = j;
  while (k < line_end && buf[k] != ' ') {
    k++;
  }
  if (k == j) {
    return false;
  }
  out->target.p = buf + j;
  out->target.n = k - j;

  /* 逐行解析 header */
  size_t pos = line_end + 2u;
  while (pos < hdr_end) {
    size_t end = pos;
    while (end < hdr_end && !(buf[end] == '\r' && buf[end + 1] == '\n')) {
      end++;
    }
    if (end == pos) {
      break;
    }
    if (end - pos > DP_LINE_MAX) {
      return false;
    }

    size_t colon = pos;
    while (colon < end && buf[colon] != ':') {
      colon++;
    }
    if (colon == pos || colon >= end) {
      return false;
    }

    dp_str name;
    name.p = buf + pos;
    name.n = colon - pos;

    dp_str value;
    value.p = buf + colon + 1;
    value.n = end - colon - 1;
    value = trim_ows(value);

    if (header_is(name, "content-length")) {
      out->content_length_raw = value;
      out->has_content_length = true;
    } else if (header_is(name, "if-match")) {
      out->if_match = value;
    } else if (header_is(name, "authorization")) {
      out->authorization = value;
    } else if (header_is(name, "transfer-encoding")) {
      /* 不支持分块：出现即拒绝，避免请求走私 */
      out->chunked = true;
    }

    pos = end + 2u;
  }
  return true;
}

/* target → path / query，再拆出 /v1/blob/<key> 的 key。 */
static void split_target(dp_str target, dp_str* path, dp_str* query) {
  size_t i = 0;
  while (i < target.n && target.p[i] != '?') {
    i++;
  }
  path->p = target.p;
  path->n = i;
  if (i < target.n) {
    query->p = target.p + i + 1u;
    query->n = target.n - i - 1u;
  } else {
    query->p = "";
    query->n = 0u;
  }
}

static void split_key(dp_str* path, dp_str* key) {
  /* 形如 /v1/<group>/<key>：第三个斜杠之后是 key，path 归一到 /v1/<group>。
   * /v1/keys 无 key 时保持原样，交给 handler 判断。 */
  size_t slashes = 0;
  size_t cut = 0;
  for (size_t i = 0; i < path->n; i++) {
    if (path->p[i] == '/') {
      slashes++;
      if (slashes == 3u) {
        cut = i;
        break;
      }
    }
  }
  if (slashes < 3u) {
    key->p = "";
    key->n = 0u;
    return;
  }
  key->p = path->p + cut + 1u;
  key->n = path->n - cut - 1u;
  path->n = cut;
}

/* 剥掉 If-Match 两侧的引号（弱校验标记 W/ 直接拒绝）。 */
static dp_str strip_quotes(dp_str v) {
  if (v.n >= 2u && v.p[0] == '"' && v.p[v.n - 1u] == '"') {
    v.p++;
    v.n -= 2u;
  }
  return v;
}

/* ---------- 单连接 ---------- */

static void handle_conn(int fd, dp_handler h, const dp_cfg* cfg) {
  char buf[DP_HDR_MAX];
  size_t used = 0;
  size_t hdr_end = (size_t)-1;

  while (used < sizeof buf) {
    ssize_t n = read(fd, buf + used, sizeof buf - used);
    if (n < 0) {
      if (errno == EINTR) {
        continue;
      }
      /* SO_RCVTIMEO 到点：告诉对端为什么被断，而不是默默关掉 */
      if (errno == EAGAIN || errno == EWOULDBLOCK) {
        send_status(fd, 408, "request timeout");
      }
      return;
    }
    if (n == 0) {
      break;
    }
    used += (size_t)n;
    hdr_end = find_hdr_end(buf, used);
    if (hdr_end != (size_t)-1) {
      break;
    }
  }
  if (hdr_end == (size_t)-1) {
    send_status(fd, 400, "bad request");
    return;
  }

  raw_head head;
  if (!parse_head(buf, hdr_end, &head)) {
    send_status(fd, 400, "bad request");
    return;
  }
  if (head.chunked) {
    send_status(fd, 501, "chunked not supported");
    return;
  }

  bool is_put = header_is(head.method, "put");

  dp_str path;
  dp_str query;
  split_target(head.target, &path, &query);
  dp_str key;
  split_key(&path, &key);

  /* Content-Length：缺失视作 0（GET 无 body 是常态） */
  size_t content_len = 0;
  if (head.has_content_length) {
    if (!parse_size(head.content_length_raw, &content_len)) {
      send_status(fd, 400, "bad content-length");
      return;
    }
  }
  if (content_len > cfg->max_body) {
    send_status(fd, 413, "payload too large");
    return;
  }

  size_t body_off = hdr_end + 4u;
  size_t have = used - body_off;
  if (have > content_len) {
    /* 多余的字节说明请求不合法（或试图走私），直接拒绝 */
    send_status(fd, 400, "trailing bytes");
    return;
  }

  dp_arena* a = dp_arena_new(cfg->max_body + DP_HDR_MAX);
  if (!a) {
    send_status(fd, 500, "out of memory");
    return;
  }

  uint8_t* body = (uint8_t*)dp_alloc(a, content_len + 1u);
  if (have > 0u) {
    memcpy(body, buf + body_off, have);
  }
  size_t got = have;
  while (got < content_len) {
    ssize_t n = read(fd, body + got, content_len - got);
    if (n < 0) {
      if (errno == EINTR) {
        continue;
      }
      if (errno == EAGAIN || errno == EWOULDBLOCK) {
        send_status(fd, 408, "request timeout");
      }
      dp_arena_free(a);
      return;
    }
    if (n == 0) {
      dp_arena_free(a);
      send_status(fd, 400, "short body");
      return;
    }
    got += (size_t)n;
  }

  /* 鉴权：/health 放行，便于探活 */
  bool is_health = header_is(path, "/health");
  if (cfg->token.n > 0u && !is_health) {
    dp_str want = dp_str_lit("Bearer ");
    bool ok = head.authorization.n == want.n + cfg->token.n &&
              memcmp(head.authorization.p, want.p, want.n) == 0 &&
              memcmp(head.authorization.p + want.n, cfg->token.p, cfg->token.n) == 0;
    if (!ok) {
      dp_arena_free(a);
      send_status(fd, 401, "unauthorized");
      return;
    }
  }

  /* key 合法性是安全边界：非法 key 一律 400，不进 handler */
  if (key.n > 0u && !dp_valid_key(key)) {
    dp_arena_free(a);
    send_status(fd, 400, "bad key");
    return;
  }

  /* If-Match 由运行时把关：D0 无法表达「条件失败」这个返回值 */
  if (is_put && head.if_match.n > 0u && key.n > 0u) {
    int err = 0;
    dp_bytes cur = dp_blob_read(a, cfg->root, key, &err);
    if (err == (int)DP_ERR_NOT_FOUND) {
      dp_arena_free(a);
      send_status(fd, 412, "precondition failed");
      return;
    }
    if (err != 0) {
      dp_arena_free(a);
      send_status(fd, 500, "internal error");
      return;
    }
    dp_str cur_tag = dp_sys_u64_str(a, dp_sys_hash64(cur));
    dp_str want = strip_quotes(head.if_match);
    if (!dp_str_eq(cur_tag, want)) {
      dp_arena_free(a);
      send_status(fd, 412, "precondition failed");
      return;
    }
  }

  dp_request rq;
  rq.method = head.method;
  rq.path = path;
  rq.key = key;
  rq.query = query;
  rq.authorization = head.authorization;
  rq.if_match = head.if_match;
  rq.body.p = body;
  rq.body.n = content_len;

  dp_response resp;
  memset(&resp, 0, sizeof resp);
  int rc = h(a, cfg->root, &rq, &resp);
  if (rc != 0) {
    send_status(fd, status_for_tag(rc), "error");
    dp_arena_free(a);
    return;
  }

  const char* ct = "application/json";
  if (resp.content_type.n > 0u && header_value_safe(resp.content_type)) {
    resp.content_type = dp_str_dup(a, resp.content_type);
    ct = resp.content_type.p;
  }
  /* ETag 从 handler 的 body 推导：同一份表示只能有一个版本号，让 handler 再写
   * 一遍就是重复书写（SPEC §2.2 H7）。空 body 没有表示，就不发这个头；
   * 运行时自己的错误页走 send_status，那是常量文本，也不该有版本。 */
  dp_str etag = dp_str_lit("");
  if (resp.body.n > 0u) {
    etag = dp_sys_u64_str(a, dp_sys_hash64(resp.body));
  }
  send_response(fd, resp.status, ct, resp.body, etag);
  dp_arena_free(a);
}

/* ---------- 入口 ---------- */

dp_cfg dp_cfg_from_env(void) {
  dp_cfg c;
  const char* port = getenv("DP_PORT");
  const char* bind_addr = getenv("DP_BIND");
  const char* root = getenv("DP_ROOT");
  const char* token = getenv("DP_TOKEN");
  const char* max_body = getenv("DP_MAX_BODY");

  c.port = port ? (uint16_t)strtoul(port, NULL, 10) : (uint16_t)8787;
  c.bind = dp_str_from_cstr(bind_addr && bind_addr[0] ? bind_addr : "0.0.0.0");
  c.root = dp_str_from_cstr(root ? root : "./data");
  c.token = dp_str_from_cstr(token ? token : "");
  c.max_body = max_body ? (size_t)strtoul(max_body, NULL, 10) : (8u << 20);
  if (c.port == 0u) {
    c.port = 8787;
  }
  if (c.max_body == 0u) {
    c.max_body = 8u << 20;
  }
  return c;
}

int dp_serve(dp_handler h, const dp_cfg* cfg) {
  signal(SIGPIPE, SIG_IGN);
  /* commit 子进程只 fork 不 wait，忽略 SIGCHLD 让内核自动收割，不留僵尸 */
  signal(SIGCHLD, SIG_IGN);

  /* 数据目录不存在就别开机：否则服务能起来、每个写请求都 500，
   * 那就是「启动成功、运行全废」的静默降级。宁可当场失败。 */
  char rootbuf[4096];
  if (cfg->root.n == 0u || cfg->root.n >= sizeof rootbuf) {
    fprintf(stderr, "droplet: DP_ROOT 长度 %zu 不在 1..%zu\n",
            (size_t)cfg->root.n, sizeof rootbuf - 1u);
    return 1;
  }
  memcpy(rootbuf, cfg->root.p, cfg->root.n);
  rootbuf[cfg->root.n] = '\0';
  struct stat st;
  if (stat(rootbuf, &st) != 0 || !S_ISDIR(st.st_mode)) {
    fprintf(stderr, "droplet: 数据目录不可用：%s（先 mkdir -p）\n", rootbuf);
    return 1;
  }

  int s = socket(AF_INET, SOCK_STREAM, 0);
  if (s < 0) {
    perror("droplet: socket");
    return 1;
  }
  int one = 1;
  setsockopt(s, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);

  struct sockaddr_in addr;
  memset(&addr, 0, sizeof addr);
  addr.sin_family = AF_INET;
  addr.sin_addr.s_addr = htonl(INADDR_ANY);
  addr.sin_port = htons(cfg->port);

  /* 监听地址：默认全网；放反代后面时用 DP_BIND=127.0.0.1 收窄 */
  char bindbuf[64];
  if (cfg->bind.n > 0u && cfg->bind.n < sizeof bindbuf) {
    memcpy(bindbuf, cfg->bind.p, cfg->bind.n);
    bindbuf[cfg->bind.n] = '\0';
    if (inet_pton(AF_INET, bindbuf, &addr.sin_addr) != 1) {
      fprintf(stderr, "droplet: DP_BIND=%s 不是合法 IPv4，回退 0.0.0.0\n", bindbuf);
      addr.sin_addr.s_addr = htonl(INADDR_ANY);
    }
  }

  if (bind(s, (struct sockaddr*)&addr, sizeof addr) < 0) {
    perror("droplet: bind");
    close(s);
    return 1;
  }
  if (listen(s, 64) < 0) {
    perror("droplet: listen");
    close(s);
    return 1;
  }

  fprintf(stderr, "droplet: listening on %.*s:%u root=%.*s max_body=%zu\n",
          (int)cfg->bind.n, cfg->bind.p, (unsigned)cfg->port,
          (int)cfg->root.n, cfg->root.p, cfg->max_body);

  for (;;) {
    int c = accept(s, NULL, NULL);
    if (c < 0) {
      if (errno == EINTR) {
        continue;
      }
      break;
    }
    /* 单线程服务：不设超时的话，一个只发半截 header 的连接就能把进程钉死。 */
    struct timeval tv;
    tv.tv_sec = DP_IO_TIMEOUT_SEC;
    tv.tv_usec = 0;
    setsockopt(c, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof tv);
    setsockopt(c, SOL_SOCKET, SO_SNDTIMEO, &tv, sizeof tv);
    handle_conn(c, h, cfg);
    close(c);
  }
  close(s);
  return 0;
}
