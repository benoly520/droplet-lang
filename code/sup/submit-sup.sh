#!/bin/sh
# 提交口：别人（通常是 AI）交上来的一份 dp_decide.c，能不能进这个服务。
#
# 为什么不是只跑 oracle：样例只覆盖纯决定。一份候选可以样例全过、却让服务编不过、
# 让接线断开、让 wire 断言翻面。所以提交口跑的是**整条面**，而且是在一份临时树里跑：
# 候选不碰工作区，拒绝之后现场留着给人看。
#
# 判据（顺序即优先级）：
#   1) 构建：编译器对账 + 符号审计 + 接线检查（候选必须真的被服务引用）
#   2) 题面：与 req/FROZEN 一致（验收表/声明头/契约没被改过）
#   3) 样例：19 条全过，一条一个进程
#   4) 验收：30 条 wire 断言全过
#   5) 契约：四节自洽、编号不悬空
# 任何一项红 -> 拒绝，并指名红在哪一项。全绿 -> 接受。
# 题面一项单列在前面：题被改过时，后面全绿也不作数。
#
# 用法：sh code/sup/submit-sup.sh <候选.c> [端口]
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
if [ "$#" -lt 1 ]; then
  echo "用法：sh code/sup/submit-sup.sh <候选.c> [端口]" >&2
  exit 2
fi
CAND=$1
PORT=${2:-18202}
if [ ! -f "$CAND" ]; then
  echo "挂：找不到候选 $CAND" >&2
  exit 2
fi

T=$ROOT/.build/submit
rm -rf "$T"
mkdir -p "$T"
cp -a "$ROOT/code" "$T/code"
cp -a "$ROOT/req" "$T/req"
cp "$CAND" "$T/code/sup/dp_decide.c"
LOGD=$T/logs
mkdir -p "$LOGD"

step() { # step <名字> <命令...>：失败即拒绝
  NAME=$1
  shift
  printf '  [%s] %-8s ' "$NAME" "$NAME"
  if "$@" >"$LOGD/$NAME.log" 2>&1; then
    echo "绿"
    return 0
  fi
  echo "红"
  echo "提交结果：拒绝（$NAME）"
  tail -n 20 "$LOGD/$NAME.log" | sed 's/^/    /'
  echo "  （现场保留在 $T，候选已拷入 $T/code/sup/dp_decide.c）"
  exit 1
}

echo "submit：$CAND"
step build    sh "$T/code/sup/build.sh" submit
step freeze   sh "$T/code/sup/freeze.sh" --check "$T/.build/submit/blobd"
step oracle   sh "$T/code/sup/oracle-sup.sh" submit-oracle
step accept   sh "$T/code/sup/accept-sup.sh" "$T/.build/submit/blobd" "$PORT" submit
step contract python3 "$T/code/sup/contract-sup.py" "$T/.build/submit/blobd"

# 数字从临时树自己数出来，不写死：写死的结论会在加一条断言的那天变成假话。
NASSERT=$(awk '/^ck / { if ($2 + 0 > m) m = $2 + 0 } END { print m + 0 }' \
  "$T/code/webapi/deploy/accept-table.sh")
NSAMP=$(awk '/^  X\([0-9]+,/ { n++ } END { print n + 0 }' "$T/code/sup/dp_decide.h")
echo "提交结果：接受（构建 / 题面冻结 / $NSAMP 样例 / $NASSERT 断言 / 契约 全绿）"
