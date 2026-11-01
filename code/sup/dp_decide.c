/* Droplet：纯决定的实现（用户所有）。
 *
 * 这一份就是"服务跑的那一份"：`dp_sup.h` 与 `dp_cfg.c` 调的是这里的函数，
 * oracle 二进制链接的也是这里的对象。不是手抄，所以样例翻面等于服务翻面。
 *
 * 形状纪律：函数体里只有条件与常量，不碰 IO、不碰环境变量、不碰 arena。
 * 一旦需要环境或文件系统，它就不该住在这个文件里（那条决定属于运行时或 handler）。
 */
#include "dp_decide.h"

int dec_create_result(bool fresh, int err) {
  if (err != 0) {
    return err;
  }
  return fresh ? 0 : (int)DP_ERR_CONFLICT;
}

bool dec_port_usable(unsigned long x) { return x >= 1ul && x <= 65535ul; }

int dec_precondition_result(int read_err, bool exists, bool tag_matches) {
  if (read_err != 0 && read_err != (int)DP_ERR_NOT_FOUND) {
    return read_err;
  }
  if (!exists) {
    return (int)DP_ERR_PRECONDITION_FAILED;
  }
  return tag_matches ? 0 : (int)DP_ERR_PRECONDITION_FAILED;
}

bool dec_auth_ok(bool token_configured, bool is_health, bool header_present, bool header_matches) {
  if (!token_configured || is_health) {
    return true;
  }
  return header_present && header_matches;
}

bool dec_body_too_large(size_t n, size_t max) { return n > max; }
