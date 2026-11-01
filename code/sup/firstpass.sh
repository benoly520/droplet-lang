#!/bin/sh
# 首次成功率（pass@k）的量尺：一串按顺序到达的候选，第几次才过样例。
#
# **当前仓库没有模型在环，所以这里没有"AI 的首次成功率"这个数。** 这个脚本是量尺，
# 不是测量结果：给它一串候选（attempt-01.c, attempt-02.c, ...），它给出
# first-pass = 第几次通过、pass@1、pass@k。没有候选输入时，文档里的对应格子写"未测量"。
#
# 口径：
#   * 候选按文件名字典序（attempt-01 < attempt-02 ...）；
#   * 一次候选 = 一份完整 dp_decide.c；编译失败也算一次失败（与 build.sh 同口径）；
#   * 一条样例一个进程；全过才算这次候选通过；
#   * pass@k：前 k 次里只要有一次全过就是 1，否则 0。first-pass 记录实际第几次。
#
# 用法：
#   sh code/sup/firstpass.sh <目录> [k]
#   sh code/sup/firstpass.sh --self-test
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
B=$ROOT/.build/firstpass
export TMPDIR=$ROOT/.build
FLAGS="-std=c11 -O2 -Wall -Wextra -Werror"
rm -rf "$B"; mkdir -p "$B"

cp "$ROOT"/code/sup/dp_decide.h "$ROOT"/code/sup/dp_oracle.c "$ROOT/code/runtime/rt.h" "$B/"
cc $FLAGS -I"$B" -c "$ROOT/code/sup/dp_oracle.c" -o "$B/oracle.o"
cp "$ROOT/code/sup/dp_decide.c" "$B/clean.c"
cc $FLAGS -I"$B" -c "$B/clean.c" -o "$B/clean.o"
cc $FLAGS -I"$B" "$B/oracle.o" "$B/clean.o" -o "$B/clean-oracle"
IDS=$("$B/clean-oracle" --list | cut -f1)
N=$(printf '%s\n' "$IDS" | grep -c '^[0-9]' || true)
if [ "$N" -eq 0 ]; then
  echo "挂：样例表 0 行 —— 首次成功率没有判据" >&2
  exit 1
fi

# run_attempts <目录> <k>：打印每次候选的结果与 pass@k；有通过则退 0
run_attempts() {
  DIR=$1
  K=$2
  IDX=0
  FIRST=0
  PASS_AT_K=0
  for SRC in "$DIR"/attempt-*.c; do
    [ -f "$SRC" ] || continue
    IDX=$((IDX + 1))
    NAME=$(basename "$SRC")
    TAG=$(printf '%s' "$NAME" | tr -c 'A-Za-z0-9' '_')
    if ! cc $FLAGS -I"$B" -c "$SRC" -o "$B/$TAG.o" >"$B/$TAG.log" 2>&1; then
      echo "  第 $IDX 次 $NAME：编译失败"
      continue
    fi
    cc $FLAGS -I"$B" "$B/oracle.o" "$B/$TAG.o" -o "$B/$TAG-oracle" >>"$B/$TAG.log" 2>&1
    GOT=0
    BAD=""
    for ID in $IDS; do
      RC=0
      "$B/$TAG-oracle" --only "$ID" >/dev/null 2>&1 || RC=$?
      if [ "$RC" -eq 0 ]; then
        GOT=$((GOT + 1))
      else
        BAD=${BAD:-$ID}
      fi
    done
    if [ "$GOT" -eq "$N" ]; then
      echo "  第 $IDX 次 $NAME：过 $N/$N"
      if [ "$FIRST" -eq 0 ]; then
        FIRST=$IDX
        if [ "$IDX" -le "$K" ]; then PASS_AT_K=1; fi
      fi
    else
      echo "  第 $IDX 次 $NAME：挂 #$BAD（$GOT/$N）"
    fi
  done
  if [ "$IDX" -eq 0 ]; then
    echo "挂：目录里没有 attempt-*.c —— 没有尝试就没有首次成功率" >&2
    return 2
  fi
  P1=0
  [ "$FIRST" -eq 1 ] && P1=1
  if [ "$IDX" -lt "$K" ]; then
    echo "（候选只有 $IDX 次，k=$K 按实际次数算）"
  fi
  if [ "$FIRST" -eq 0 ]; then
    echo "first-pass: 无（前 $K 次都没过）；pass@1=0 pass@$K=0"
    return 1
  fi
  echo "first-pass: 第 $FIRST 次；pass@1=$P1 pass@$K=$PASS_AT_K"
  return 0
}

if [ "${1:-}" = "--self-test" ]; then
  T=$B/self; mkdir -p "$T"
  python3 - "$ROOT" "$T" <<'PY'
import pathlib, sys
root, t = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2])
src = (root / "code/sup/dp_decide.c").read_text()
bad = src.replace("  return fresh ? 0 : (int)DP_ERR_CONFLICT;",
                  "  (void)fresh; /* 丢掉 bool */\n  return 0;")
flip = src.replace("return n > max;", "return n >= max;")
assert bad != src and flip != src
for sub, names in [
    ("t1", [("attempt-01.c", bad), ("attempt-02.c", flip), ("attempt-03.c", src)]),
    ("t2", [("attempt-01.c", src)]),
    ("t3", [("attempt-01.c", bad), ("attempt-02.c", flip)]),
]:
    d = t / sub
    d.mkdir(parents=True, exist_ok=True)
    for name, text in names:
        (d / name).write_text(text)
PY
  FAILS=0
  check() { # check <标签> <期望rc> <必须出现的片段> <目录> <k>
    LABEL=$1; WANT_RC=$2; NEED=$3; DIR=$4; K=$5
    OUT=$(run_attempts "$DIR" "$K" 2>&1) && RC=0 || RC=$?
    printf '%s\n' "$OUT" | sed 's/^/    /'
    if [ "$RC" -ne "$WANT_RC" ] || ! printf '%s\n' "$OUT" | grep -q "$NEED"; then
      echo "自检失败：$LABEL 期望（rc=$WANT_RC，含「$NEED」），实得 rc=$RC" >&2
      FAILS=$((FAILS + 1))
    else
      echo "  自检通过：$LABEL"
    fi
  }
  check "坏 -> 坏 -> 好：第 3 次过" 0 "first-pass: 第 3 次；pass@1=0 pass@5=1" "$T/t1" 5
  check "一次就过：pass@1=1" 0 "first-pass: 第 1 次；pass@1=1 pass@5=1" "$T/t2" 5
  check "两次都不过：pass@5=0" 1 "first-pass: 无" "$T/t3" 5
  if [ "$FAILS" -ne 0 ]; then
    echo "firstpass 自检：$FAILS 条与预期不符" >&2
    exit 1
  fi
  echo "firstpass 自检：3/3"
  exit 0
fi

if [ "$#" -lt 1 ] || [ ! -d "$1" ]; then
  echo "用法：sh code/sup/firstpass.sh <含 attempt-*.c 的目录> [k] | --self-test" >&2
  exit 2
fi
run_attempts "$1" "${2:-5}"
