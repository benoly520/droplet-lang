/* Droplet 运行时 · 版本化钩子
 *
 * 契约里的交点只有一句话：这份 key 的新版本已经落盘，可以提交了。
 *
 * 实现方式是「防抖 + fork/exec」：
 *   - 同一个窗口内只起一个子进程，避免每写一次就 fork 一次；
 *   - 只把 DP_COMMIT_KEY 这一个路径交给 git，不扫全树（扫全树会撞上正在 rename 的
 *     `<key>.tmp.*`，git 会报 fatal: unable to stat）；
 *   - 父进程不 wait，响应不等 git；子进程由 SIGCHLD=SIG_IGN 自动收割，不留僵尸；
 *   - 没配 DP_COMMIT_CMD 就当没有版本控制，什么都不做。
 *
 * 这是唯一一个「故意不关心结果」的运行时能力：提交失败只影响历史，不影响当前请求。
 */
/* setenv 是 POSIX；-std=c11 下不显式声明特性宏就拿不到原型 */
#define _POSIX_C_SOURCE 200809L

#include "rt.h"

#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/types.h>
#include <time.h>
#include <unistd.h>

#define DP_PATH_MAX 4096u
#define DP_DEBOUNCE_DEFAULT 60

/* dp_str 不是 NUL 结尾，进 syscall 前必须落到栈缓冲。 */
static bool copy_cstr(char* dst, size_t cap, dp_str s) {
  if (s.n == 0u || s.n >= cap) {
    return false;
  }
  memcpy(dst, s.p, s.n);
  dst[s.n] = '\0';
  return true;
}

/* 防抖窗口，单位秒；0 表示不防抖（测试用），越界值回落到默认。 */
static int debounce_sec(void) {
  const char* v = getenv("DP_COMMIT_DEBOUNCE");
  if (!v || v[0] == '\0') {
    return DP_DEBOUNCE_DEFAULT;
  }
  long n = strtol(v, NULL, 10);
  if (n < 0 || n > 86400) {
    return DP_DEBOUNCE_DEFAULT;
  }
  return (int)n;
}

void dp_ver_commit_soon(dp_str root, dp_str key) {
  static time_t last = 0;

  const char* cmd = getenv("DP_COMMIT_CMD");
  if (!cmd || cmd[0] == '\0') {
    return;
  }

  time_t now = time(NULL);
  int window = debounce_sec();
  if (last != 0 && window > 0 && now - last < (time_t)window) {
    return;
  }

  char dir[DP_PATH_MAX];
  if (!copy_cstr(dir, sizeof dir, root)) {
    fprintf(stderr, "droplet: commit 跳过：root 路径不合法或过长\n");
    return;
  }
  /* key 已由 dp_valid_key 限过字符集，长度上限 32，64 的缓冲够用 */
  char keybuf[64];
  if (!copy_cstr(keybuf, sizeof keybuf, key)) {
    fprintf(stderr, "droplet: commit 跳过：key 不合法或过长\n");
    return;
  }

  pid_t pid = fork();
  if (pid < 0) {
    fprintf(stderr, "droplet: commit fork 失败：%s\n", strerror(errno));
    return;
  }
  if (pid == 0) {
    /* 子进程：只看得到自己的 stderr，父进程的响应早就写回去了 */
    setenv("DP_COMMIT_KEY", keybuf, 1);
    if (chdir(dir) != 0) {
      fprintf(stderr, "droplet: commit chdir %s 失败：%s\n", dir, strerror(errno));
      _exit(127);
    }
    execl("/bin/sh", "sh", "-c", cmd, (char*)NULL);
    _exit(127);
  }

  last = now;
  fprintf(stderr, "droplet: commit spawned pid=%ld key=%s\n", (long)pid, keybuf);
}
