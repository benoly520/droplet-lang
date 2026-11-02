#!/bin/sh
# 作者/维护者的唯一回路：一条命令，一个退出码，红在哪一项就说哪一项。
#
# 为什么要这个：AI 写 C 的失败模式是**静默漂移**，不是不会写语法。所以回路必须短、
# 必须一次给全（构建/样例/契约/登记/冻结/预算），而且红的时候要给出**第一个失败项**，
# 不是让调用者去翻四份日志自己拼。
#
# 口径：
#   * 默认只跑不占端口的六项（约 4 秒）：构建、样例、契约、需求登记、题面冻结、必读面预算；
#   * --full 追加需要起服务的项（验收、变异、破坏、变异覆盖分），约 7 分钟；
#   * 任一项红 -> 立即停，打印该项日志末尾，退出 1。不继续跑后面的项：
#     后面的红很可能是前面的红造成的，一起报等于没说。
#
# 用法：sh code/sup/check.sh [--full]
set -u

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$ROOT"
MODE=fast
if [ "${1:-}" = "--full" ]; then MODE=full; fi

LOGD=$ROOT/.build/check-logs
rm -rf "$LOGD"; mkdir -p "$LOGD"
STEP=0

run() { # run <名字> <命令...>
  STEP=$((STEP + 1))
  NAME=$1
  shift
  LOG=$LOGD/$(printf '%02d' "$STEP")-$NAME.log
  printf '  [%d] %-10s ' "$STEP" "$NAME"
  if "$@" >"$LOG" 2>&1; then
    echo "绿"
    return 0
  fi
  echo "红"
  echo "check: 红在第 $STEP 项（$NAME）—— 后面的项没有跑"
  tail -n 20 "$LOG" | sed 's/^/    /'
  exit 1
}

echo "check（$MODE）："
run build    sh code/sup/build.sh check
run oracle   sh code/sup/oracle-sup.sh check-oracle
run contract python3 code/sup/contract-sup.py .build/check/blobd
run req      python3 code/sup/req-check.py .build/check/blobd
run freeze   sh code/sup/freeze.sh --check .build/check/blobd
run surface  sh code/sup/surface-budget.sh

if [ "$MODE" = "full" ]; then
  run accept  sh code/sup/accept-sup.sh "$ROOT/.build/check/blobd" "${PORT:-18201}" check
  run mutate  sh code/sup/mutate-dec.sh check-mutate
  run compare sh code/sup/compare-sup.sh --self-test
  run firstpass sh code/sup/firstpass.sh --self-test
  run maint   sh code/sup/maint-cost.sh
  run sabotage sh code/sup/sabotage-sup.sh "${SAB_PORT:-18981}"
  run mutscore python3 code/sup/mutation-score.py --binary "$ROOT/.build/check/blobd" \
                 --l-port "${MUT_L_PORT:-18300}" --s-port "${MUT_S_PORT:-18400}"
fi

echo "check: 绿（$STEP 项）"
