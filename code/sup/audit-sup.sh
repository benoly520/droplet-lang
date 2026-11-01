#!/bin/sh
# 超集的符号审计：把"未声明的效果读取"检查从宏改名换成构建后的事实核对。
#
# 为什么这样才是超集：上一版在 dp_sup.h 末尾写
#     #define getenv(name) dp_env_not_declared_in_DP_ENVS(name)
# 这行让一份合法 C11 程序 include 本头之后编不过 —— 那是 incompatible extension。
# 现在本层不改动任何 C 既有含义，改为：所有读环境变量的代码都住在 dp_cfg.c 一个 TU 里，
# 于是用户 TU 的 .o 里只要出现 getenv / environ，就等于读了没声明的东西。
# 这比改名覆盖更广：E1（#undef getenv 再调真 libc）与 E6（extern char **environ）
# 都必须在符号表里留名字，绕不掉。
#
# 用法：sh code/sup/audit-sup.sh <用户 TU 的 .o>...
set -eu

# 用户代码不该直接碰的东西：配置读取、进程、原始 socket、裸文件与目录操作。
# （运行时 rt_*.c 合法地使用它们，所以只审计用户 TU —— 边界记在 SPEC §0.8.4 E4。）
DENY='getenv environ setenv unsetenv system popen pclose execl execlp execle execv execvp fork vfork socket socketpair bind connect listen accept send sendto recv recvfrom open openat read write unlink rename mkdir rmdir chdir'

if [ "$#" -eq 0 ]; then
  echo "用法：sh code/sup/audit-sup.sh <用户 TU 的 .o>..." >&2
  exit 2
fi

FAIL=0
for OBJ in "$@"; do
  [ -f "$OBJ" ] || { echo "审计失败：找不到目标文件 $OBJ" >&2; exit 1; }
  UNDEF=$(nm -u "$OBJ" 2>/dev/null | awk '{print $2}')
  HIT=''
  for S in $DENY; do
    if printf '%s\n' "$UNDEF" | grep -qx "$S"; then
      HIT="$HIT $S"
    fi
  done
  if [ -n "$HIT" ]; then
    echo "审计红：$OBJ 未声明地引用了$HIT" >&2
    FAIL=1
  else
    echo "审计绿：$OBJ 未声明引用 0 个"
  fi
done

exit $FAIL
