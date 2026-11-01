#!/bin/sh
# 维护成本：三种典型改动各要动几个文件、几个 +/- 行。
#
# 这不是"感觉便宜"，是数出来的：在 code/ 的副本上做脚本化改动，用 diff 数文件与行。
# 只有**局部性**这一个结论：改配置默认值不该牵动实现，改边界策略必须同时改样例。
# 每个改动带预算；超了就红。预算取自本轮实测，目的是防回归，不是宣称最优。
#
# 用法：sh code/sup/maint-cost.sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
B=$ROOT/.build/maint
export TMPDIR=$ROOT/.build
rm -rf "$B"; mkdir -p "$B"

run_change() { # run_change <名字> <预算文件数> <预算行数>
  NAME=$1
  BF=$2
  BL=$3
  T=$B/$NAME
  rm -rf "$T"; mkdir -p "$T"
  cp -a "$ROOT/code" "$T/before"
  cp -a "$ROOT/code" "$T/after"
  python3 - "$T/after" "$NAME" <<'PY'
import pathlib, sys

d = pathlib.Path(sys.argv[1])
name = sys.argv[2]

def sub(rel, old, new, count=1):
    p = d / rel
    s = p.read_text(encoding="utf-8")
    got = s.count(old)
    if got != count:
        sys.exit(f"{name}: {rel} 里锚点出现 {got} 次（期望 {count}）——改动脚本要跟着改")
    p.write_text(s.replace(old, new), encoding="utf-8")

if name == "port-default":
    # 改默认端口：表 + 部署样板各一行
    sub("sup/dp_env.h",
        'X("APP_PORT", port, port, "8787")',
        'X("APP_PORT", port, port, "9797")')
    sub("webapi/deploy/blobd.env.example", "APP_PORT=8787", "APP_PORT=9797")

elif name == "add-env":
    # 新增一个配置项：只加表里一行，加载器与结构由表展开
    sub("sup/dp_env.h",
        '  X("APP_MAX_BODY", max_body, size, "8388608")',
        '  X("APP_MAX_BODY", max_body, size, "8388608")                                 \\\n'
        '  X("APP_LOG", log, str, "")')

elif name == "body-boundary":
    # 策略：等于上限也拒绝。"决定 + 样例 + 目标措辞"必须一起改 —— 这正是这套结构要暴露的耦合
    sub("sup/dp_decide.c", "return n > max;", "return n >= max;")
    sub("sup/dp_decide.h",
        'X(17, dec_body_too_large(8388608, 8388608), false, "等于上限 -> 允许（边界含在区间内）")',
        'X(17, dec_body_too_large(8388608, 8388608), true, "等于上限 -> 也拒绝（边界不含）")')
    sub("sup/blobd.c", "超过 APP_MAX_BODY 一律 413", "达到 APP_MAX_BODY 一律 413")

else:
    sys.exit(f"未知改动 {name}")
PY
  DIFF="$T/diff"
  diff -ru "$T/before" "$T/after" >"$DIFF" 2>&1 || true
  FILES=$(grep -c '^diff -ru ' "$DIFF" || true)
  LINES=$(awk '/^\+[^+]/ && !/^\+\+\+ / {n++} /^-[^-]/ && !/^--- / {n++} END {print n+0}' "$DIFF")
  if [ "$FILES" -eq 0 ]; then
    echo "挂：$NAME 没有产生改动 —— 数出来的 0 是假绿" >&2
    return 1
  fi
  if [ "$FILES" -le "$BF" ] && [ "$LINES" -le "$BL" ]; then
    echo "  改动 $NAME：$FILES 个文件 / $LINES 个 +/- 行（预算 $BF/$BL）—— 在预算内"
    return 0
  fi
  echo "  改动 $NAME：$FILES 个文件 / $LINES 个 +/- 行（预算 $BF/$BL）—— 超预算" >&2
  return 1
}

FAILS=0
run_change port-default 2 4 || FAILS=$((FAILS + 1))
run_change add-env 1 3 || FAILS=$((FAILS + 1))
run_change body-boundary 3 6 || FAILS=$((FAILS + 1))

if [ "$FAILS" -ne 0 ]; then
  echo "maint-cost: $FAILS 个改动超预算" >&2
  exit 1
fi
echo "maint-cost: 3 个改动都在预算内（每个改动只落在一个很小的面上）"
