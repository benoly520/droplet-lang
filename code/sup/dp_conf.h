/* Droplet 超集：配置结构与加载入口（机制所有）。
 *
 * 这里是超集兼容性的关键一刀：读环境变量的代码全部住在 dp_cfg.c 这一个 TU 里，
 * 因此**用户 TU 的 .o 里出现 getenv 就等于"读了一个没声明的东西"** —— 审计能测。
 * 上一版的 #define getenv(...) 改名没了：那行让合法 C 程序 include 本头之后编不过。
 */
#ifndef DP_CONF_H
#define DP_CONF_H

#include <stddef.h>
#include <stdint.h>
#include "rt.h"
#include "dp_env.h"

#ifndef DP_ENVS
#error "本层要求先定义 DP_ENVS：配置项必须声明，含默认值"
#endif

/* kind -> C 类型。第三列从"打印出来看看"变成真正决定结构形状的东西。 */
#define dp_type_str dp_str
#define dp_type_port uint16_t
#define dp_type_size size_t

typedef struct {
#define DP_CONF_ROW(NAME, FIELD, KIND, DFLT) dp_type_##KIND FIELD;
  DP_ENVS(DP_CONF_ROW)
#undef DP_CONF_ROW
} dp_conf_sup;

extern dp_conf_sup dp_conf;

/* 在 dp_cfg.c 里由同一张表展开生成；唯一合法的 getenv 使用者。 */
void dp_cfg_load(void);

#endif /* DP_CONF_H */
