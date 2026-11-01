/* Droplet：旗舰服务。合法 C11 + 四张声明表，没有任何自定义前端。
 *
 * 这是仓库里唯一的服务实现（原来那门自造语言已删除，理由见 docs/SPEC.md §2）。
 *
 * 本文件里**一个状态码数字都没有**，一个环境变量名都不出现（配置名只住在 dp_env.h 表里），
 * 而且**一次 getenv 都不写**：写了会被构建后的符号审计抓住，不再是靠宏改名。
 * "同一 key 连 POST 两次回 201"那个 bug 在这里由 dp_created() 的构造排除。
 */
#include <stdio.h>
#include <string.h>

/* ---- 声明表 1：路由（动词、路径、实现、成功投影、鉴权、预条件、理由）----
 *
 * 最后一列不是注释，是进契约的数据。DP_GUARD_NONE 也必须写出来并给理由：
 * "沉默不算决定"这条纪律搬进了 C 的表结构。
 * 守卫现在把根目录显式传进去（DP_GUARD_IF_MATCH(dp_conf.root)），
 * 不再依赖"展开时恰好能看见某个全局"。
 */
#define DP_ROUTES(X)                                                                       \
  X("GET", "/health", h_health, DP_OK_JSON, DP_AUTH_PUBLIC, DP_GUARD_NONE,                 \
    "探活不鉴权：token 为空或未设时也放行")                                                \
  X("GET", "/v1/blob", h_blob_get, DP_OK_JSON, DP_AUTH_TOKEN, DP_GUARD_NONE,               \
    "只读，无需版本比较")                                                                  \
  X("PUT", "/v1/blob", h_blob_put, DP_OK_NO_CONTENT, DP_AUTH_TOKEN,                        \
    DP_GUARD_IF_MATCH(dp_conf.root),                                                       \
    "写必须可比对旧版本：If-Match 带了就比，位置由表决定")                                  \
  X("GET", "/v1/keys", h_keys_list, DP_OK_JSON, DP_AUTH_TOKEN, DP_GUARD_NONE,              \
    "只读")                                                                                \
  X("POST", "/v1/keys", h_keys_create, DP_OK_CREATED, DP_AUTH_TOKEN, DP_GUARD_NONE,        \
    "创建新 key 没有既有版本可比；重复创建由 dp_created 报 409")

/* ---- 声明表 2：目标 ↔ 验收断言 ---- */

#define DP_GOALS(X)                                                                       \
  X(health_open, "探活不鉴权，恒回一行 ok", "1")                                          \
  X(read_back, "同一 key 的 GET 字节等于最近一次成功 PUT 的字节", "3,21")                 \
  X(snapshot_before_write, "PUT 前必须存在上一版本快照", "22")                            \
  X(list_from_dir, "清单来自目录，不来自任何注册表文件", "2,11,24")                       \
  X(create_once, "POST 只在 key 不存在时成功；201 空 body", "10,12,20")                   \
  X(commit_per_write, "每次成功写入都留下一条版本化提交（DP_COMMIT_CMD 的子进程）", "23") \
  X(error_no_body, "错误响应不携带 body", "7,14,17")                                      \
  X(not_found_status, "不存在的 key 报 404（错误变体到状态码的映射表）", "13")            \
  X(single_version, "ETag 由 body 推导，同一份表示只有一个版本号", "4,18")                \
  X(no_content_framing, "204 不带 Content-Length 也不带 body（RFC 7230）", "8,9")         \
  X(versioned_write, "If-Match 不符必须 412 且空 body；带了且相符就放行", "5,6,7")        \
  X(method_closed, "未声明的动词一律 405 且空 body", "16,17,18")                          \
  X(path_closed, "非法 key 一律 400，不进 handler", "15")                                 \
  X(body_bounded, "超过 APP_MAX_BODY 一律 413", "19")

#include "dp_env.h" /* 配置表在这里：名字、字段、kind、默认值 */
#include "dp_sup.h"

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
  /* If-Match 的比对不在这里：表里声明了守卫，展开生在调用之前。 */
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
    return rc; /* false -> DP_ERR_CONFLICT -> 409 */
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
#define DP_ROUTE_ROW(METHOD, PATH, FN, OK, AUTH, GUARD, WHY)              \
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
