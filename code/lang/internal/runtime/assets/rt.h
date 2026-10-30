/* Droplet 运行时
 *
 * 分工：
 *   rt_core.c  arena、字符串/字节、日志与整数格式化
 *   rt_http.c  单线程 HTTP/1.1 服务与请求解析
 *   rt_blob.c  文件哑存储（read / write / backup / create / list）
 *   rt_ver.c   版本化钩子（防抖 + fork/exec，不阻塞响应）
 */
#ifndef DP_RT_H
#define DP_RT_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

typedef struct {
  const char* p;
  size_t n;
} dp_str;

typedef struct {
  const uint8_t* p;
  size_t n;
} dp_bytes;

typedef struct {
  uint16_t tag;
} dp_error;

typedef struct {
  int _;
} dp_unit;

/* ---- 错误 tag 常量 ----
 *
 * 必须与契约里错误变体的声明顺序一致：
 *   type ApiError = NotFound | BadRequest | IoError | Conflict
 * checker 从 100 起按声明顺序编号，改顺序就得同步改这里。
 */
#define DP_ERR_NOT_FOUND 100u
#define DP_ERR_BAD_REQUEST 101u
#define DP_ERR_IO 102u
#define DP_ERR_CONFLICT 103u

/* ---- arena：按块分配，整块释放 ---- */
typedef struct dp_arena dp_arena;

dp_arena* dp_arena_new(size_t cap);
void*     dp_alloc(dp_arena* a, size_t n);
void      dp_arena_free(dp_arena* a);

/* ---- 字符串与字节 ---- */
dp_str dp_str_from_cstr(const char* s);
dp_str dp_str_dup(dp_arena* a, dp_str s);
bool   dp_str_eq(dp_str a, dp_str b);
bool   dp_bytes_eq(dp_bytes a, dp_bytes b);
bool   dp_val_eq(uint64_t a, uint64_t b);

/* ---- host Sys 能力 ---- */
uint64_t dp_sys_hash64(dp_bytes body);
dp_str   dp_sys_u64_str(dp_arena* a, uint64_t x);
int64_t  dp_sys_now_unix(void); /* 仓内唯一调用点是快照文件名的时间戳 */
void     dp_sys_log(dp_str msg);

/* ---- HTTP ---- */
typedef struct {
  dp_str method;
  dp_str path;  /* /v1/blob/abc 会归一到 /v1/blob，尾部 key 单独取 */
  dp_str key;
  dp_str query;
  dp_str authorization;
  dp_str if_match;
  dp_bytes body;
} dp_request;

typedef struct {
  uint16_t status;
  dp_str content_type;
  dp_bytes body;
  dp_str etag;
} dp_response;

typedef struct {
  uint16_t port;
  dp_str bind; /* 监听地址，默认 "0.0.0.0"；放反代后面时给 "127.0.0.1" */
  dp_str root;
  dp_str token; /* 为空表示不校验 Authorization */
  size_t max_body;
} dp_cfg;

/* 返回 0 表示已填 out；返回 100+ 表示错误 tag，由运行时映射状态码。 */
typedef int (*dp_handler)(dp_arena* a, dp_str root, const dp_request* rq, dp_response* out);

dp_cfg dp_cfg_from_env(void);
int    dp_serve(dp_handler h, const dp_cfg* cfg);

/* ---- 文件哑存储 ---- */
bool       dp_valid_key(dp_str key);
dp_bytes   dp_blob_read(dp_arena* a, dp_str root, dp_str key, int* err);
uint64_t   dp_blob_write(dp_arena* a, dp_str root, dp_str key, dp_bytes body, int* err);
/* 写前备一份旧版本：.snapshots/<key>.<unix>.<seq>，每个 key 只留最近
 * DP_SNAPSHOT_KEEP 份（默认 8，0 = 不轮转）。 */
void       dp_blob_backup(dp_arena* a, dp_str root, dp_str key);
bool       dp_blob_create(dp_arena* a, dp_str root, dp_str key, int* err);
dp_bytes   dp_blob_list(dp_arena* a, dp_str root, int* err);

/* ---- 版本化钩子 ----
 *
 * 语义是“这份 key 的新版本已经落盘，可以提交”。实现：DP_COMMIT_CMD 给出提交命令
 * （在 root 目录下用 sh -c 执行，子进程环境里有 DP_COMMIT_KEY），
 * DP_COMMIT_DEBOUNCE 给防抖秒数（默认 60，0 表示不防抖）。
 * 未设 DP_COMMIT_CMD 时什么都不做。
 *
 * 之所以把 key 传进来：提交只能 add 这一个路径，扫全树会撞上正在 rename 的临时文件。
 */
void dp_ver_commit_soon(dp_str root, dp_str key);

#define dp_str_lit(s)   ((dp_str){ (s), sizeof(s) - 1u })
#define dp_bytes_lit(s) ((dp_bytes){ (const uint8_t*)(s), sizeof(s) - 1u })

#endif /* DP_RT_H */
