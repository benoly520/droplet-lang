#!/bin/sh
# exp3 的后半：把 droplet verify 的变异算子（分支删除=清空 then；提前返回=删掉 return）
# 逐个作用到 decisions.c 上，用同一批 6 条样例去抓 —— 与 droplet 的
# "6 处变异，0 处没被抓住" 对照。
#
# 锚点都带足上下文：早先一版用裸 "    return false;" 当锚点，结果匹配到
# requires_auth 里 6 空格那行的后半截，产出一个根本没碰 create_ok 的假变异。
# 变异实验里最cheap的失败就是把假变异当成发现，所以这里全部用整块锚点。
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
SRC=$ROOT/docs/validation/c-control
B=$ROOT/.build/c-control/mut
A=$ROOT/code/lang/internal/runtime/assets
rm -rf "$B"
mkdir -p "$B"

python3 - "$SRC/decisions.c" "$B" <<'PY'
import pathlib, sys
src = pathlib.Path(sys.argv[1]).read_text()
out = pathlib.Path(sys.argv[2])

RA_ALL = """  if (token_set) {
    if (is_health) {
      return false;
    }
    return true;
  }
  return false;
"""
CO_ALL = """  if (exists) {
    return false;
  }
  return true;
"""

# (编号, 说明, 原锚点, 替换) —— 每条锚点都落在唯一一个函数唯一一个位置
muts = [
    ("R1", "分支删除：清空 requires_auth 外层 then",
     RA_ALL, "  if (token_set) {\n  }\n  return false;\n"),
    ("R2", "分支删除：清空 requires_auth 内层 then",
     RA_ALL, "  if (token_set) {\n    if (is_health) {\n    }\n    return true;\n  }\n  return false;\n"),
    ("R3", "提前返回：删掉 requires_auth 内层 return false（与 R2 同形）",
     RA_ALL, "  if (token_set) {\n    if (is_health) {\n    }\n    return true;\n  }\n  return false;\n"),
    ("R4", "提前返回：删掉 requires_auth 的 return true",
     RA_ALL, "  if (token_set) {\n    if (is_health) {\n      return false;\n    }\n  }\n  return false;\n"),
    ("R5", "提前返回：删掉 requires_auth 末尾 return false",
     RA_ALL, "  if (token_set) {\n    if (is_health) {\n      return false;\n    }\n    return true;\n  }\n"),
    ("C1", "分支删除：清空 create_ok 的 then",
     CO_ALL, "  if (exists) {\n  }\n  return true;\n"),
    ("C2", "提前返回：删掉 create_ok 的 return false（与 C1 同形）",
     CO_ALL, "  if (exists) {\n  }\n  return true;\n"),
    ("C3", "提前返回：删掉 create_ok 末尾 return true",
     CO_ALL, "  if (exists) {\n    return false;\n  }\n"),
]

seen = {}
rows = []
for mid, name, old, new in muts:
    if src.count(old) != 1:
        rows.append((mid, name, "ANCHOR", "锚点命中 %d 次" % src.count(old)))
        continue
    variant = src.replace(old, new, 1)
    if variant in seen:
        rows.append((mid, name, "DUP", "与 %s 同形（C 里 then 块只有一条语句，两个算子落到同一处）" % seen[variant]))
        continue
    seen[variant] = mid
    path = out / ("decisions_%s.c" % mid)
    path.write_text(variant)
    rows.append((mid, name, "OK", ""))

for mid, name, st, msg in rows:
    print("%s\t%s\t%s\t%s" % (mid, name, st, msg))
PY

echo "--- 逐个编译 + 跑同一批 6 条样例（编译参数与 droplet 一致：-Wall -Wextra，无 -Werror）---"
CAUGHT=0
MISS=0
DONE=0
for f in "$B"/decisions_*.c; do
  n=$(basename "$f" .c | sed 's/decisions_//')
  exe="$B/intent_$n"
  if ! cc -std=c11 -O1 -Wall -Wextra -I"$SRC" -I"$A" "$f" "$SRC/intent_c.c" -o "$exe" 2>"$B/$n.log"; then
    echo "  CAUGHT $n 编译不过：$(head -1 "$B/$n.log" | cut -c1-80)"
    CAUGHT=$((CAUGHT+1)); DONE=$((DONE+1)); continue
  fi
  DONE=$((DONE+1))
  w=""
  if grep -q 'warning' "$B/$n.log"; then
    w="｜编译器有 warning：$(grep -m1 -o 'warning:.*' "$B/$n.log" | cut -c1-58)"
  fi
  if "$exe" >"$B/$n.out" 2>&1; then
    echo "  MISS   $n 6 条样例全过$w"
    MISS=$((MISS+1))
  else
    echo "  CAUGHT $n $(grep -m1 'FAIL' "$B/$n.out" | cut -c1-52)$w"
    CAUGHT=$((CAUGHT+1))
  fi
done

echo "mutate_c: $DONE 个不同变异，$MISS 处没被样例抓住"
echo "参考：droplet verify code/webapi/intents.dlt → 6 处变异，0 处没被抓住"
echo "补测：若 C 也开 -Werror（droplet 的 cc 参数里没有这一项），MISS 的还剩几个："
for f in "$B"/decisions_*.c; do
  n=$(basename "$f" .c | sed 's/decisions_//')
  if cc -std=c11 -O1 -Wall -Wextra -Werror -I"$SRC" -I"$A" "$f" "$SRC/intent_c.c" -o "$B/e_$n" 2>/dev/null; then
    if "$B/e_$n" >/dev/null 2>&1; then echo "    MISS $n（-Werror 也活下来）"; else echo "    CAUGHT $n（样例抓住）"; fi
  else
    echo "    CAUGHT $n（-Werror 编译期就拦下）"
  fi
done
