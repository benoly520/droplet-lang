/* Droplet 超集：配置加载器（机制所有，唯一一个 TU 可以碰 getenv）。
 *
 * 生成物：每个 DP_ENVS 行 -> dp_conf.<FIELD> = dp_load_<KIND>(NAME, DFLT)
 * 三种 kind 的加载各有失败口径：
 *   port：不在 1..65535 -> 打日志并留 0，dp_serve_sup() 因此拒绝启动；
 *   size：非整数 -> 打日志回落默认值；
 *   str ：未设或空串 -> 用表里的默认值（token 默认 "" = 不鉴权，这条显式写在表里）。
 */
#include "dp_conf.h"
#include "dp_decide.h"

#include <stdio.h>
#include <stdlib.h>

dp_conf_sup dp_conf;

static dp_str dp_load_str(const char* name, const char* dflt) {
  const char* v = getenv(name);
  return dp_str_from_cstr((v != NULL && v[0] != '\0') ? v : dflt);
}

static uint16_t dp_load_port(const char* name, const char* dflt) {
  const char* v = getenv(name);
  if (v == NULL || v[0] == '\0') {
    v = dflt;
  }
  char* end = NULL;
  unsigned long x = strtoul(v, &end, 10);
  /* 区间判断住在 dp_decide.c 的 dec_port_usable：样例打的就是这条，
     改坏了不用等部署，启动即拒绝。 */
  if (end == v || *end != '\0' || !dec_port_usable(x)) {
    fprintf(stderr, "droplet-sup: 配置 %s=%s 不在 1..65535，拒绝启动\n", name, v);
    return 0u;
  }
  return (uint16_t)x;
}

static size_t dp_load_size(const char* name, const char* dflt) {
  const char* v = getenv(name);
  if (v == NULL || v[0] == '\0') {
    v = dflt;
  }
  char* end = NULL;
  unsigned long x = strtoul(v, &end, 10);
  if (end == v || *end != '\0') {
    fprintf(stderr, "droplet-sup: 配置 %s=%s 不是整数，用默认值 %s\n", name, v, dflt);
    x = strtoul(dflt, NULL, 10);
  }
  return (size_t)x;
}

void dp_cfg_load(void) {
#define DP_CONF_ROW(NAME, FIELD, KIND, DFLT) dp_conf.FIELD = dp_load_##KIND(NAME, DFLT);
  DP_ENVS(DP_CONF_ROW)
#undef DP_CONF_ROW
}
