#!/bin/sh
# 超集兼容性实测：证明"加一层不改 C"。
#
# 上一版的说法被实测推翻过（SPEC §0.8.2 记着）：一份合法 C11 程序 include dp_sup.h
# 之后编译失败，因为头文件末尾把 getenv 改名成了不存在的符号。那是
# incompatible extension。这一版的要求变成可测的：
#   A) 同一批合法 C 程序，"搜索路径里没有本层" 与 "有本层" 两种口径都要过，输出必须一致；
#   B) 一份 include 本层的普通 C 程序（照 C 的常理调 getenv）必须仍然编译得过；
#   C) 未声明读取仍然会被抓 —— 但抓它的不是编译器，是构建后的符号审计。
# A/B 证明兼容性，C 证明抓手没因为兼容性而丢掉。
#
# 用法：sh code/sup/compat-sup.sh [构建标签]
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
LABEL=${1:-compat}
B=$ROOT/.build/$LABEL
export TMPDIR=$ROOT/.build
mkdir -p "$B"

FLAGS="-std=c11 -O2 -Wall -Wextra -Werror"

PASS=0
FAIL=0
ok() { echo "  过 $1"; PASS=$((PASS + 1)); }
ng() { echo "  挂 $1"; FAIL=$((FAIL + 1)); }

# ---- 测试语料：都是合法 C11，故意覆盖 C 的"脏角落" ----
cat > "$B/p1.c" <<'EOF'
/* 直接按 C 的常理读环境变量：上一版这条会被宏改名打死 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
int main(void) {
  const char* v = getenv("COMPAT_VAR");
  char buf[8];
  snprintf(buf, sizeof buf, "%s", v ? v : "unset");
  printf("p1:%s\n", buf);
  return 0;
}
EOF

cat > "$B/p2.c" <<'EOF'
/* C11 特性堆：_Generic、复合字面量、static_assert、匿名联合体、VLA、指定初始化 */
#include <stdio.h>
#include <assert.h>
int main(void) {
  _Static_assert(sizeof(long) >= 4, "c11");
  int n = 5;
  int vla[n];
  for (int i = 0; i < n; i++) vla[i] = i * i;
  struct s { int a; union { int b; float c; }; } x = { .a = 1, .b = 2 };
  int* p = (int[]){10, 20, 30};
  printf("p2:%d %d %d %d\n", vla[n - 1], x.a + x.b, p[1], _Generic(1, int: 7, default: 0));
  return 0;
}
EOF

cat > "$B/p3.c" <<'EOF'
/* 手工内存、旧式 main、可变参宏、label 与 goto —— C 允许但"不干净"的写法 */
#include <stdio.h>
#include <stdlib.h>
#define LOG(fmt, ...) fprintf(stderr, fmt "\n", __VA_ARGS__)
int main() {
  int* heap = malloc(sizeof(int) * 3u);
  if (heap == NULL) return 1;
  heap[0] = 1; heap[1] = 2; heap[2] = 3;
  int sum = 0;
  for (int i = 0; i < 3; i++) { sum += heap[i]; }
  goto done;
done:
  LOG("p3:%d", sum);
  free(heap);
  return 0;
}
EOF

echo "== A 组：搜索路径里没有本层 / 有本层，两种口径都要过且输出一致 =="
for SRC in p1 p2 p3; do
  if ! cc $FLAGS "$B/$SRC.c" -o "$B/$SRC-base" 2>"$B/$SRC-base.err"; then
    ng "$SRC：纯 C 口径就编不过（语料坏了）"
    cat "$B/$SRC-base.err" >&2
    continue
  fi
  if ! cc $FLAGS -I"$ROOT/code/sup" "$B/$SRC.c" -o "$B/$SRC-iso" 2>"$B/$SRC-iso.err"; then
    ng "$SRC：把本层放进搜索路径后编不过"
    head -3 "$B/$SRC-iso.err" >&2
    continue
  fi
  OUTBASE=$("$B/$SRC-base" 2>&1 || true)
  OUTISO=$("$B/$SRC-iso" 2>&1 || true)
  if [ "$OUTBASE" = "$OUTISO" ]; then
    ok "$SRC：两种口径都过，输出一致（$OUTBASE）"
  else
    ng "$SRC：输出不一致 base=[$OUTBASE] iso=[$OUTISO]"
  fi
done

echo "== B 组：include 本层的普通 C 程序仍按 C 的常理工作 =="
cat > "$B/with.h.probe.c" <<'EOF'
/* 一份 include 本层的程序：表齐全 + main 里按 C 常理调 getenv。
 * 上一版这条编译失败；这一版必须编译通过 —— 检查挪到了链接之后的符号审计。 */
#include <stdio.h>
#include <stdlib.h>
#define DP_ROUTES(X) X("GET", "/x", h_x, DP_OK_JSON, DP_AUTH_TOKEN, DP_GUARD_NONE, "r")
#include "dp_sup.h"
static int h_x(dp_arena* a, const dp_request* rq, dp_bytes* b) {
  (void)a; (void)rq; (void)b; return 0;
}
int main(void) {
  dp_cfg_load();
  const char* v = getenv("APP_BIND");
  printf("probe:%s\n", v ? v : "unset");
  return 0;
}
EOF
cp "$ROOT"/code/sup/dp_sup.h "$ROOT"/code/sup/dp_conf.h "$ROOT"/code/sup/dp_env.h \
   "$ROOT"/code/sup/dp_decide.h "$ROOT"/code/sup/dp_decide.c "$B/"
RT=$ROOT/code/lang/internal/runtime/assets
cp "$RT"/rt.h "$RT"/rt_core.c "$RT"/rt_http.c "$RT"/rt_blob.c "$RT"/rt_ver.c "$B/"
cp "$ROOT"/code/sup/dp_cfg.c "$B/"

if cc -std=c11 -O2 -Wall -I"$B" -c "$B/with.h.probe.c" -o "$B/with.h.probe.o" 2>"$B/probe.err"; then
  ok "include 本层 + 一次合法 getenv：编译通过（上一版在这里编不过）"
else
  ng "include 本层后合法 C 写法被拒"
  head -4 "$B/probe.err" >&2
fi

echo "== C 组：抓手没丢 —— 未声明读取由符号审计抓 =="
if sh "$ROOT/code/sup/audit-sup.sh" "$B/with.h.probe.o" >/dev/null 2>&1; then
  ng "符号审计放过了带 getenv 的用户 TU"
else
  ok "符号审计抓到了带 getenv 的用户 TU（编译过、构建不过）"
fi

echo
echo "兼容性实测：$PASS 过 / $FAIL 挂"
[ "$FAIL" -eq 0 ]
