#!/bin/sh
# 候选比较：给定 N 份 dp_decide.c，问"样例能不能把我想要的那一份挑出来"。
#
# 这不是选美，是**样例充分性**的判据：
#   * 0 份通过  -> 无解：没有任何候选满足契约；
#   * 1 份通过  -> 唯一：样例把其余候选都分开了；
#   * >1 份通过 -> 歧义：这些候选在样例覆盖的行为上等价，契约没有指定该选哪一份。
#
# 最后一条最容易误读，所以写清楚：歧义不等于候选有 bug，等于**样例没定义区别**。
# 如果两份实现只在样例之外的行为上不同，交付哪一份都是"猜"。这个工具的作用是把
# "猜"变成一次失败的运行，而不是一次自信的交付。
#
# 判定口径与 build.sh 一致：-std=c11 -O2 -Wall -Wextra -Werror；编译失败算不通过。
# 一条样例一个进程：崩溃只算这一条（与 oracle-sup.sh 同口径）。
#
# 用法：
#   sh code/sup/compare-sup.sh <候选.c> [候选.c ...]
#   sh code/sup/compare-sup.sh <目录>          # 目录下所有 *.c
#   sh code/sup/compare-sup.sh --self-test     # 用已知结局的四个变体自检
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
B=$ROOT/.build/compare
export TMPDIR=$ROOT/.build
FLAGS="-std=c11 -O2 -Wall -Wextra -Werror"
rm -rf "$B"; mkdir -p "$B"

cp "$ROOT"/code/sup/dp_decide.h "$ROOT"/code/sup/dp_oracle.c "$ROOT"/code/runtime/rt.h "$B/"
cc $FLAGS -I"$B" -c "$ROOT/code/sup/dp_oracle.c" -o "$B/oracle.o"
# 干净参照只用来取样例编号列表。
cp "$ROOT/code/sup/dp_decide.c" "$B/clean.c"
cc $FLAGS -I"$B" -c "$B/clean.c" -o "$B/clean.o"
cc $FLAGS -I"$B" "$B/oracle.o" "$B/clean.o" -o "$B/clean-oracle"
IDS=$("$B/clean-oracle" --list | cut -f1)
N=$(printf '%s\n' "$IDS" | grep -c '^[0-9]' || true)
if [ "$N" -eq 0 ]; then
  echo "挂：样例表 0 行 —— 候选比较没有判据" >&2
  exit 1
fi

judge() { # judge <候选.c>...：打印每份候选的结果，并以退出码给出结论
  SURVIVORS=0
  TOTAL=0
  for SRC in "$@"; do
    TOTAL=$((TOTAL + 1))
    NAME=$(basename "$SRC")
    TAG=$(printf '%s' "$NAME" | tr -c 'A-Za-z0-9' '_')
    LOG="$B/$TAG.log"
    if ! cc $FLAGS -I"$B" -c "$SRC" -o "$B/$TAG.o" >"$LOG" 2>&1; then
      echo "  候选 $NAME：不通过（编译失败，见 $LOG）"
      continue
    fi
    if ! cc $FLAGS -I"$B" "$B/oracle.o" "$B/$TAG.o" -o "$B/$TAG-oracle" >>"$LOG" 2>&1; then
      echo "  候选 $NAME：不通过（链接失败，见 $LOG）"
      continue
    fi
    PASS=0
    BAD=""
    CRASH=0
    for ID in $IDS; do
      RC=0
      "$B/$TAG-oracle" --only "$ID" >/dev/null 2>&1 || RC=$?
      if [ "$RC" -eq 0 ]; then
        PASS=$((PASS + 1))
      elif [ "$RC" -eq 1 ]; then
        BAD=${BAD:-$ID}
      else
        CRASH=$((CRASH + 1))
        BAD=${BAD:-$ID(崩溃)}
      fi
    done
    if [ "$PASS" -eq "$N" ] && [ "$CRASH" -eq 0 ]; then
      echo "  候选 $NAME：通过 $N/$N（存活）"
      SURVIVORS=$((SURVIVORS + 1))
    else
      echo "  候选 $NAME：通过 $PASS/$N，首条未过 #$BAD"
    fi
  done
  echo "  合计：$TOTAL 份候选，$SURVIVORS 份通过样例"
  if [ "$SURVIVORS" -eq 1 ]; then
    echo "判：唯一（样例把候选分开了）"
    return 0
  elif [ "$SURVIVORS" -eq 0 ]; then
    echo "判：无解（没有候选满足样例）"
    return 1
  else
    echo "判：歧义（$SURVIVORS 份在样例上等价，契约没指定该选哪一份）"
    return 1
  fi
}

expect() { # expect <标签> <期望退出码> <期望摘要> <候选...>
  LABEL=$1; WANT_RC=$2; WANT=$3; shift 3
  OUT=$(judge "$@" 2>&1) && RC=0 || RC=$?
  printf '%s\n' "$OUT" | sed 's/^/    /'
  if [ "$RC" -ne "$WANT_RC" ] || ! printf '%s\n' "$OUT" | grep -q "判：$WANT"; then
    echo "自检失败：$LABEL 期望（rc=$WANT_RC, 判：$WANT），实得 rc=$RC" >&2
    return 1
  fi
  echo "  自检通过：$LABEL"
  return 0
}

if [ "${1:-}" = "--self-test" ]; then
  T=$B/self; mkdir -p "$T"
  cp "$ROOT/code/sup/dp_decide.c" "$T/a-clean.c"
  python3 - "$ROOT" "$T" <<'PY'
import pathlib, sys
root, t = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2])
src = (root / "code/sup/dp_decide.c").read_text()
# b：行为等价的端口区间改写（样例区分不了 -> 应当报歧义）
b = src.replace("return x >= 1ul && x <= 65535ul;",
                "return x != 0ul && x < 65536ul;")
# c：把 create 的 bool 丢掉（样例 #2 会翻面 -> 应当被拒）
c = src.replace("  return fresh ? 0 : (int)DP_ERR_CONFLICT;",
                "  (void)fresh; /* 丢掉 bool */\n  return 0;")
# d：边界翻转（样例 #17 会翻面 -> 应当被拒）
d = src.replace("return n > max;", "return n >= max;")
for name, text in [("b-port-rewrite.c", b), ("c-drop-bool.c", c), ("d-boundary.c", d)]:
    assert text != src, name
    (t / name).write_text(text)
PY
  FAILS=0
  expect "单份原件 -> 唯一" 0 唯一 "$T/a-clean.c" || FAILS=$((FAILS + 1))
  expect "原件 + 等价改写 -> 歧义" 1 歧义 "$T/a-clean.c" "$T/b-port-rewrite.c" || FAILS=$((FAILS + 1))
  expect "原件 + 两个错版 -> 唯一" 0 唯一 \
         "$T/a-clean.c" "$T/c-drop-bool.c" "$T/d-boundary.c" || FAILS=$((FAILS + 1))
  expect "只有错版 -> 无解" 1 无解 "$T/c-drop-bool.c" || FAILS=$((FAILS + 1))
  if [ "$FAILS" -ne 0 ]; then
    echo "compare-sup 自检：$FAILS 条与预期不符" >&2
    exit 1
  fi
  echo "compare-sup 自检：4/4"
  exit 0
fi

if [ "$#" -eq 0 ]; then
  echo "用法：sh code/sup/compare-sup.sh <候选.c | 目录> ... | --self-test" >&2
  exit 2
fi

CAND=""
for A in "$@"; do
  if [ -d "$A" ]; then
    for F in "$A"/*.c; do
      if [ -f "$F" ]; then CAND="$CAND $F"; fi
    done
  elif [ -f "$A" ]; then
    CAND="$CAND $A"
  else
    echo "挂：找不到候选 $A" >&2
    exit 2
  fi
done
# shellcheck disable=SC2086
judge $CAND
