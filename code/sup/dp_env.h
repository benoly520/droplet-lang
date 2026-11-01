/* Droplet 超集：配置表（用户所有）。
 *
 * 这张表被两侧 include：dp_conf.h（结构 + extern）与 dp_cfg.c（加载器）。
 * 名字与默认值是用户数据，字段名与类型 kind 决定生成的结构形状。
 */
#ifndef DP_ENV_H
#define DP_ENV_H

/* 第三列 kind 现在是有语义的：str -> dp_str，port -> uint16_t，size -> size_t。
 * 加载器按 kind 展开成 dp_load_##KIND(NAME, DFLT)，默认值因此不再是装饰。 */
#define DP_ENVS(X)                                                                  \
  X("APP_BIND", bind, str, "0.0.0.0")                                               \
  X("APP_TOKEN", token, str, "")                                                    \
  X("APP_ROOT", root, str, "blobs")                                                 \
  X("APP_PORT", port, port, "8787")                                                 \
  X("APP_MAX_BODY", max_body, size, "8388608")

#endif /* DP_ENV_H */
