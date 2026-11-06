#!/bin/sh
# 登记语言（lang/*.dpt）的一条门：定义、实现、等价性、预算，四段连着跑。
#
# 为什么要成一段：这门语言的全部主张是"完整定义读一遍就会用，且不比五份表弱"。
# 这句话有四个可失败的部件，缺一个主张就不成立：
#   1) 定义里的错误码 = 检查器会吐的错误码（否则定义在骗读它的人）；
#   2) 每条错误码都有一个样本真的打中它（否则码表是装饰）；
#   3) 用这门语言写出的 blobd 登记件与 req/ 五份表逐栏等价（否则换介质会丢东西）；
#   4) 定义本身放得进一次阅读（否则"读一遍就会用"不成立）。
# 任何一段红，check.sh 就停在 lang 这一项。
#
# 用法：sh code/sup/lang-check.sh [二进制]      默认 .build/check/blobd
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$ROOT"
BIN=${1:-.build/check/blobd}

echo "[1/4] 定义 <-> 检查器对账"
python3 code/sup/dpt-check.py --self-check

echo "[2/4] 错误码活性样本"
python3 code/sup/dpt-check.py --self-test

echo "[3/4] 登记件：与 req/ 五份表逐栏等价 + 用工程清单独立判一次"
python3 code/sup/dpt-check.py lang/blobd.dpt --against req/
python3 code/sup/dpt-check.py lang/blobd.dpt --binary "$BIN" --allow-new

echo "[4/4] 定义预算"
sh code/sup/lang-budget.sh

echo "lang: 绿（四段全过）"
