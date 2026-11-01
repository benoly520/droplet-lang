/* Droplet 超集形态的旗舰服务（SPEC §0.8）：合法 C11 + 两张声明表。
 *
 * 与 code/webapi/src 下 .dlt 那套的关系：同一张 wire 契约，两种介质。这一版是 v0.29 之后
 * 方向调整的主形态；.dlt 那套仍在仓库里，作为对照与待删除候选（docs/NEXT.md P18）。
 *
 * 本文件里**一个状态码数字都没有**，一个环境变量名都只出现在 DP_ENVS 表里。
 * P16 那个 bug（同一 key 连 POST 两次回 201/201）在这里由构造排除：handler 拿到的
 * dp_created() 已经把"已存在"翻译成 DP_ERR_CONFLICT，返回值没有可丢弃的形状。
 */
#include <stdio.h>
#include <string.h>

/* ---- 声明表 1：路由（动词、路径、实现、成功投影、鉴权、预条件、理由）----
 *
 * 最后一列不是注释，是进契约的数据：`DP_GUARD_NONE` 必须给理由，
 * 沉默等于默认不做并发保护这件事再次被要求显式表态（D3 的纪律照搬）。
 */
#define DP_ROUTES(X)                                                                    \
  X("GET", "/health", h_health, DP_OK_JSON, DP_AUTH_PUBLIC, DP_GUARD_NONE,               \
    "探活不鉴权：token 为空或未设时也放行")                                              \
  X("GET", "/v1/blob", h_blob_get, DP_OK_JSON, DP_AUTH_TOKEN, DP_GUARD_NONE,             \
    "只读，无需版本比较")                                                                 \
  X("PUT", "/v1/blob", h_blob_put, DP_OK_NO_CONTENT, DP_AUTH_TOKEN, DP_GUARD_IF_MATCH,   \
    "写必须可比对旧版本：If-Match 带了就比，位置由表决定")                                 \
  X("GET", "/v1/keys", h_keys_list, DP_OK_JSON, DP_AUTH_TOKEN, DP_GUARD_NONE,            \
    "只读")                                                                               \
  X("POST", "/v1/keys", h_keys_create, DP_OK_CREATED, DP_AUTH_TOKEN, DP_GUARD_NONE,      \
    "创建新 key 没有既有版本可比；重复创建由 dp_created 报 409")

/* ---- 声明表 2：配置（环境变量名、字段、类型、默认值）----
 *
 * token 的默认值是空串 = 不鉴权，这件事**显式写在表里**：部署只能补上，不能抹掉。
 */
#define DP_ENVS(X)                          \
  X("APP_BIND", bind, str, "0.0.0.0")        \
  X("APP_TOKEN", token, str, "")             \
  X("APP_ROOT", root, str, "blobs")

/* ---- 声明表 3：目标 ↔ 验收断言 ----
 *
 * 第三列是 accept-sup.sh 的断言编号。写 "" 就是承认"这条没被证明"。
 * contract-sup.py 会去脚本里查这些编号存不存在，不存在 -> 构建失败。
 */
#define DP_GOALS(X)                                                              \
  X(health_open, "探活不鉴权，恒回一行 ok", "1")                                  \
  X(read_back, "同一 key 的 GET 字节等于最近一次成功 PUT 的字节", "3,21")          \
  X(snapshot_before_write, "PUT 前必须存在上一版本快照", "")                       \
  X(list_from_dir, "清单来自目录，不来自任何注册表文件", "2,11")                    \
  X(create_once, "POST 只在 key 不存在时成功；201 空 body", "10,12,20")                            \
  X(error_no_body, "错误响应不携带 body", "7,14,17")                                                 \
  X(not_found_status, "不存在的 key 报 404（错误变体到状态码的映射表）", "13")                  \
  X(single_version, "ETag 由 body 推导，同一份表示只有一个版本号", "4,18")                              \
  X(no_content_framing, "204 不带 Content-Length 也不带 body（RFC 7230）", "8,9")               \
  X(versioned_write, "If-Match 不符必须 412 且空 body；带了且相符就放行", "5,6,7")                    \
  X(method_closed, "未声明的动词一律 405 且空 body", "16,17,18")                   \
  X(path_closed, "非法 key 一律 400，不进 handler", "15")                          \
  X(body_bounded, "超过 APP_MAX_BODY 一律 413", "19")

#include "dp_sup.h"

/* ---- 工具：只借用运行时的类型，不重复实现任何决定 ---- */

/* ---- 五个 handler：没有数字，没有 getenv，没有裸 bool ----
 *
 * 成功状态码只来自 DP_ROUTES 的成功投影列；handler 只填 body 或返回错误 tag。
 */

static int h_health(dp_arena* a, const dp_request* rq, dp_bytes* body) {
  (void)a;
  (void)rq;
  *body = dp_bytes_lit("{\"ok\":true}");
  return 0;
}

static int h_blob_get(dp_arena* a, const dp_request* rq, dp_bytes* body) {
  int err = 0;
  *body = dp_blob_read(a, dp_conf.root, rq->key, &err);
  if (err != 0) {
    return err; /* 状态码与空 body 由运行时按 tag 映射，这里不写数字 */
  }
  return 0;
}

static int h_blob_put(dp_arena* a, const dp_request* rq, dp_bytes* body) {
  (void)body;
  /* If-Match 的比对不在这里：表里声明了 DP_GUARD_IF_MATCH，守卫由展开生在调用之前。 */
  dp_blob_backup(a, dp_conf.root, rq->key);
  int err = 0;
  dp_blob_write(a, dp_conf.root, rq->key, rq->body, &err);
  if (err != 0) {
    return err;
  }
  dp_ver_commit_soon(dp_conf.root, rq->key);
  return 0;
}

static int h_keys_list(dp_arena* a, const dp_request* rq, dp_bytes* body) {
  (void)rq;
  int err = 0;
  *body = dp_blob_list(a, dp_conf.root, &err);
  if (err != 0) {
    return err;
  }
  return 0;
}

static int h_keys_create(dp_arena* a, const dp_request* rq, dp_bytes* body) {
  (void)body;
  int rc = dp_created(a, rq->key);
  if (rc != 0) {
    return rc; /* false -> DP_ERR_CONFLICT -> 409：P16 的那一局在这里不再可能 */
  }
  return 0;
}

/* ---- main ---- */

int main(int argc, char** argv) {
  if (argc > 1 && strcmp(argv[1], "--goals") == 0) {
    /* 契约导出走这里：声明是数据，工具读的是编译产物里的表，不是正则扫源码。 */
#define DP_GOAL_ROW(ID, TEXT, PROVEN) printf("%s\t%s\t%s\n", #ID, TEXT, PROVEN);
    DP_GOALS(DP_GOAL_ROW)
#undef DP_GOAL_ROW
#define DP_ROUTE_ROW(METHOD, PATH, FN, OK, AUTH, GUARD, WHY) \
  printf("route\t%s\t%s\t%s\n", METHOD, PATH, #FN);
    DP_ROUTES(DP_ROUTE_ROW)
#undef DP_ROUTE_ROW
#define DP_ENV_ROW(NAME, FIELD, KIND, DFLT) printf("env\t%s\t%s\t%s\n", NAME, #KIND, DFLT);
    DP_ENVS(DP_ENV_ROW)
#undef DP_ENV_ROW
    return 0;
  }
  dp_cfg_load();
  return dp_serve_sup();
}
