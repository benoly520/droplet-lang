#!/bin/sh
# 样例 oracle：把 DP_EXAMPLES 当成可执行证据跑起来。
#
# 这一步补的是"封闭样例当可执行证据"：样例不是给人看的例子，是会红会绿的判据。
# 唯一要紧的是**验的是哪一份代码**：这里链接的是
# dp_decide.c —— 服务里的 dp_created() 与 dp_load_port() 调的就是它（接线检查在守）。
#
# 三条口径（写在 docs/SPEC.md §4）：
#   ① 逐条报 ok / FAIL，不汇总成"通过"；
#   ② 一条样例都没有 = 整轮失败（没有证据不算证据）；
#   ③ 一条样例一个进程：崩溃只丢这一条，且崩溃单列，不当成通过。
#
# 用法：sh code/sup/oracle-sup.sh [构建标签]
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
LABEL=${1:-oracle}
B=$ROOT/.build/$LABEL
export TMPDIR=$ROOT/.build
rm -rf "$B"
mkdir -p "$B"

RT=$ROOT/code/runtime
FLAGS="-std=c11 -O2 -Wall -Wextra -Werror"

cp "$ROOT"/code/sup/dp_decide.h "$ROOT"/code/sup/dp_decide.c "$ROOT"/code/sup/dp_oracle.c "$B/"
cp "$RT"/rt.h "$B/"

# oracle 只需要决定层：纯函数不碰运行时，所以这里不链接 rt_*.c。
# 链接不进去也是事实 —— 这些决定里没有 IO，这是"纯决定"的定义。
cc $FLAGS -I"$B" -c "$B/dp_decide.c" -o "$B/dp_decide.o" 2>"$B/build-decide.log"
cc $FLAGS -I"$B" -c "$B/dp_oracle.c" -o "$B/dp_oracle.o" 2>"$B/build-oracle.log"
sh "$ROOT"/code/sup/audit-sup.sh "$B/dp_decide.o" >"$B/audit.log" 2>&1 || {
  cat "$B/audit.log" >&2
  echo "oracle 的决定层里出现了未声明的效果读取，见上" >&2
  exit 1
}
cc $FLAGS -I"$B" "$B/dp_oracle.o" "$B/dp_decide.o" -o "$B/dp_oracle" 2>>"$B/build-oracle.log"

ROWS=$("$B/dp_oracle" --list)
N=$(printf '%s\n' "$ROWS" | grep -c '^[0-9]' || true)
if [ "$N" -eq 0 ]; then
  echo "挂：样例表 0 行 —— 没有可执行样例，按未通过算" >&2
  exit 1
fi

PASS=0
FAIL=0
CRASH=0
# 逐条一个进程：子 shell 里累加变量带不出来，所以用 for 而不是管道。
for ID in $(printf '%s\n' "$ROWS" | cut -f1); do
  OUT=$("$B/dp_oracle" --only "$ID" 2>&1) && RC=$? || RC=$?
  if [ "$RC" -eq 0 ]; then
    PASS=$((PASS + 1))
    printf '%s\n' "$OUT"
  elif [ "$RC" -eq 1 ]; then
    FAIL=$((FAIL + 1))
    printf '%s\n' "$OUT"
  else
    CRASH=$((CRASH + 1))
    echo "  崩溃  #$ID 以信号退出（RC=$RC）—— 单列，不记通过"
  fi
done

echo "oracle: $N 条样例 / $PASS 过 / $FAIL 挂 / $CRASH 崩溃（一条一个进程）"
[ "$FAIL" -eq 0 ] && [ "$CRASH" -eq 0 ]
