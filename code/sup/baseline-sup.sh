#!/bin/sh
# 基线构建：把“上一个实现 + 当前题面”编成一份二进制，给 req-check 的先红后绿用。
#
# 为什么要把题面覆盖上去：先红后绿比的是**同一道题**在旧实现上的表现。
# 直接拿旧 commit 整棵树跑，跑的是旧题（断言条数都不一样）。所以这里：
#   git archive <基线 commit>  ->  旧实现
#   再用当前题面（freeze.sh 钉的那 11 份）覆盖上去  ->  新题
# 覆盖的只有题面：`blobd.c`（四张表 + 薄 handler）、`dp_decide.c`、`rt_*.c` 都留在旧版本 ——
# 那是“答案”那一侧，带过来就等于把这次的实现当成基线。
#
# 输出：最后一行是基线二进制的绝对路径；其余是过程日志。
# 用法：sh code/sup/baseline-sup.sh <commit> [标签]（默认 reqbase）
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$ROOT"

if [ "$#" -lt 1 ]; then
  echo "用法：sh code/sup/baseline-sup.sh <commit> [标签]" >&2
  exit 2
fi
SHA=$1
LABEL=${2:-reqbase}

T=$ROOT/.build/baseline-$LABEL
BIN=$T/.build/blobd/blobd

# 题面清单只有一份来源：freeze.sh 里的 FILES。两处各列一遍迟早会对不上。
FILES=$(sed -n "/^FILES='/,/'$/p" "$ROOT/code/sup/freeze.sh" | sed -e "s/^FILES='//" -e "s/'$//")

# 基线门现在被 check.sh 自动跑，一次全检可能调它好几回。
# 旧实现 + 题面内容都没动就复用上次那份二进制；--force 强制重建。
FORCE=${3:-}
STAMP=$(cat -- $FILES | cksum | tr -d ' ')
if [ "$FORCE" != "--force" ] && [ -x "$BIN" ] && [ "$(cat "$T/.baselined" 2>/dev/null)" = "$SHA $STAMP" ]; then
  echo "基线：$SHA（旧实现）+ 当前题面 -> $BIN（复用上次构建；要重建就传 --force）"
  printf '%s\n' "$BIN"
  exit 0
fi

rm -rf "$T"
mkdir -p "$T"

if ! git archive "$SHA" | tar -x -C "$T"; then
  echo "基线红：导不出 $SHA（浅 clone？跑 git fetch --unshallow，或手跑 req-check --baseline <二进制>）" >&2
  exit 1
fi

n=0
for F in $FILES; do
  if [ ! -f "$ROOT/$F" ]; then
    echo "基线红：题面清单里的 $F 在工作区不存在（freeze.sh 的 FILES 与仓库不一致）" >&2
    exit 1
  fi
  mkdir -p "$T/$(dirname "$F")"
  cp "$ROOT/$F" "$T/$F"
  n=$((n + 1))
done

if ! sh "$T/code/sup/build.sh" blobd >"$T/build.log" 2>&1; then
  echo "基线红：旧实现 + 当前题面编不过（题面与旧实现不兼容时不能假装验过）。日志：" >&2
  tail -n 12 "$T/build.log" >&2
  exit 1
fi

BIN=$T/.build/blobd/blobd
if [ ! -x "$BIN" ]; then
  echo "基线红：构建说成功了，但 $BIN 不在" >&2
  exit 1
fi
printf '%s %s\n' "$SHA" "$STAMP" > "$T/.baselined"
echo "基线：$SHA（旧实现）+ 当前题面 $n 份 -> $BIN"
printf '%s\n' "$BIN"
