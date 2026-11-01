#!/bin/sh
# 必读面预算：写一个服务时，AI 到底要读多少东西。
#
# 这不是"AI 能力"的测量，是一种回归护栏：面一旦悄悄变大（表里塞进新列、参考实现长胖），
# 这里先红。数字本身没有褒贬 —— 报告只说面有多大、占多少上下文。
#
# 口径：
#   * 必读面 = 四张表 + 类型 + 参考实现（下面的 FILES），不含测试脚本与文档；
#   * 命令开销 = 构建/样例/验收/契约四条命令的字面长度（AI 也要知道怎么跑）；
#   * token 是**估算**：ASCII 4 字节/token，非 ASCII（中文注释）3 字节/token。
#     换任何分词器都会变；所以预算按估算值给，报告中标注"估算"。
#   * 预算 = 9000 token / 32 KB。这是护栏，不是目标值：超过就说明面在膨胀。
#
# 用法：sh code/sup/surface-budget.sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$ROOT"

BUDGET_TOK=9000
BUDGET_BYTES=32768

FILES="code/sup/dp_env.h \
       code/sup/dp_decide.h \
       code/sup/dp_decide.c \
       code/sup/dp_sup.h \
       code/runtime/rt.h \
       code/sup/blobd.c"

CMDS='sh code/sup/build.sh sup; sh code/sup/oracle-sup.sh; \
sh code/sup/accept-sup.sh "$PWD/.build/sup/blobd" 18080 sup; \
python3 code/sup/contract-sup.py .build/sup/blobd'

for F in $FILES; do
  if [ ! -s "$F" ]; then
    echo "挂：必读面缺文件 $F（面不完整，预算无从谈起）" >&2
    exit 1
  fi
done

printf '%-28s %8s %8s\n' 文件 字节 '估算token'
RC=0
ROWS=$(python3 - "$BUDGET_TOK" "$BUDGET_BYTES" "$CMDS" $FILES <<'PY'
import math, sys

budget_tok = int(sys.argv[1])
budget_bytes = int(sys.argv[2])
cmds = sys.argv[3]
files = sys.argv[4:]

def est(b: bytes) -> int:
    non_ascii = sum(1 for x in b if x > 127)
    return math.ceil((len(b) - non_ascii) / 4 + non_ascii / 3)

total_b = total_t = 0
for f in files:
    b = open(f, "rb").read()
    t = est(b)
    total_b += len(b)
    total_t += t
    print(f"{f:28s} {len(b):8d} {t:8d}")

cb, ct = len(cmds.encode()), est(cmds.encode())
print(f"{'（命令开销）':28s} {cb:8d} {ct:8d}")
total_b += cb
total_t += ct
print(f"{'合计':28s} {total_b:8d} {total_t:8d}")
ok = total_t <= budget_tok and total_b <= budget_bytes
print(f"预算：{budget_tok} token / {budget_bytes} B —— "
      f"{'在预算内' if ok else '超预算'}（占 200k 上下文的 {100.0 * total_t / 200000:.1f}%）")
sys.exit(0 if ok else 1)
PY
) || RC=$?
printf '%s\n' "$ROWS"
exit $RC
