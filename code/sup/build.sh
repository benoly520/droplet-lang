#!/bin/sh
# 超集形态的构建：合法 C11 + 声明表，纯 cc，不经过 code/lang（H8 的 7 行余量一行不动）。
#
# v0.31 起这一步分三段，顺序是有意义的：
#   1) 用户 TU 单独编成 .o（这样才看得见它引用了哪些外部符号）；
#   2) 符号审计：用户 .o 里出现 getenv/environ 等就是"读了没声明的东西" -> 构建失败；
#   3) 链接：机制 TU（dp_cfg.c，唯一合法的 getenv 使用者）+ 运行时 + 用户 TU。
#
# 为什么把检查挪到第 2 步：上一版是在头文件末尾 #define getenv(...) 改名，
# 那让一份合法 C 程序 include 本层之后编不过 —— 按定义就不是"超集"。
# 用法：sh code/sup/build.sh [标签]
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
LABEL=${1:-sup}
B=$ROOT/.build/$LABEL
export TMPDIR=$ROOT/.build
mkdir -p "$B"

RT=$ROOT/code/lang/internal/runtime/assets
cp "$RT"/rt.h "$RT"/rt_core.c "$RT"/rt_http.c "$RT"/rt_blob.c "$RT"/rt_ver.c "$B/"
cp "$ROOT"/code/sup/dp_sup.h "$ROOT"/code/sup/dp_conf.h "$ROOT"/code/sup/dp_cfg.c \
   "$ROOT"/code/sup/dp_env.h "$ROOT"/code/sup/blobd.c "$B/"
cp "$ROOT"/code/sup/audit-sup.sh "$B/"

# -Wall -Wextra -Werror：上一轮实测"C + -Werror 打平 verify"，这一版直接把它当默认，
# 并且把表与实现的双向对账交给编译器（原型/未用函数），不交给正则。
# 用户 TU 与机制 TU 都严格编译；本层不再对 C 的既有用法加任何限制。
cc -std=c11 -O2 -Wall -Wextra -Werror -I"$B" -c "$B/blobd.c" -o "$B/blobd.o"
cc -std=c11 -O2 -Wall -Wextra -Werror -I"$B" -c "$B/dp_cfg.c" -o "$B/dp_cfg.o"

sh "$B/audit-sup.sh" "$B/blobd.o"

cc -std=c11 -O2 -Wall -Wextra -Werror -I"$B" \
   "$B/blobd.o" "$B/dp_cfg.o" "$B"/rt_core.c "$B"/rt_http.c "$B"/rt_blob.c "$B"/rt_ver.c \
   -o "$B/blobd"

echo "构建成功：$B/blobd"
