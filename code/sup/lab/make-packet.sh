#!/bin/sh
# 实验题面包：把"从声明写实现"的题面装配出来（可重复、可交给任意模型）。
#
# 为什么单独放一份题：`dp_decide.h` 是主仓库的答案，不能拿来测"从声明生成"。
# 这里的函数在仓库里没有实现、也没有文档，所以被测者读不到答案；样例就是全部判据。
#
# 用法：sh code/sup/lab/make-packet.sh <输出目录>
# 评测：sh code/sup/firstpass.sh --packet <输出目录> <含 attempt-*.c 的目录> [k]
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
if [ "$#" -lt 1 ]; then
  echo "用法：sh code/sup/lab/make-packet.sh <输出目录>" >&2
  exit 2
fi
OUT=$1
rm -rf "$OUT"; mkdir -p "$OUT"
cp "$ROOT/code/sup/lab/dp_decide.h" "$ROOT/code/sup/lab/TASK.md" "$OUT/"
cp "$ROOT/code/sup/dp_oracle.c" "$OUT/"
N=$(grep -cE '^  X\([0-9]+' "$OUT/dp_decide.h" || true)
if [ "$N" -eq 0 ]; then
  echo "挂：题目里没有样例 —— 没有判据" >&2
  exit 1
fi
if [ -e "$OUT/dp_decide.c" ]; then
  echo "挂：题面包里出现了实现文件 dp_decide.c" >&2
  exit 1
fi
echo "题面包：$OUT（$N 条样例；无实现文件）"
