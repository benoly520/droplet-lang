set -eu
# 超集形态的构建：合法 C11 + 声明表，纯 cc，不经过 code/lang（H8 的 7 行余量一行不动）。
# 用法：sh code/sup/build.sh [标签]
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
LABEL=${1:-sup}
B=$ROOT/.build/$LABEL
export TMPDIR=$ROOT/.build
mkdir -p "$B"

RT=$ROOT/code/lang/internal/runtime/assets
cp "$RT"/rt.h "$RT"/rt_core.c "$RT"/rt_http.c "$RT"/rt_blob.c "$RT"/rt_ver.c "$B/"
cp "$ROOT/code/sup/dp_sup.h" "$ROOT/code/sup/blobd.c" "$B/"

# -Wall -Wextra -Werror：上一轮实测"C + -Werror 打平 verify"，这一版直接把它当默认，
# 并且把表与实现的双向对账交给编译器（原型/未用函数），不交给正则。
cc -std=c11 -O2 -Wall -Wextra -Werror -I"$B" \
  "$B/blobd.c" "$B"/rt_core.c "$B"/rt_http.c "$B"/rt_blob.c "$B"/rt_ver.c \
  -o "$B/blobd"

echo "构建成功：$B/blobd"
