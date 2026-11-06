#!/bin/sh
# 语言定义预算：一门"读完就会用"的语言，第一指标是**完整定义的字节数**。
#
# 为什么要有这个门：登记语言的全部卖点是"AI 不必眼熟，读一遍就懂"。这句话只有在
# 定义足够短时才成立 —— 一旦它能长到几 KB，"读一遍"就退化成"背一套约定"，
# 于是这门语言相对五份 TSV 就没有任何优势了，只有成本。
# 所以预算不是风格偏好，是这个提法本身的成立条件：超了就得有人当场删掉点什么。
#
# 口径（与 surface-budget.sh 同一把尺）：
#   * 必读面 = lang/droplet.dpt 一份文件（完整定义 + 错误码表，它同时是检查器的对账对象）；
#   * token 是估算：ASCII 4 字节/token，非 ASCII 3 字节/token；换分词器会变；
#   * 预算 = 1500 token / 8192 字节。护栏不是目标值：撞线说明定义在膨胀。
#
# 用法：sh code/sup/lang-budget.sh [定义文件，默认 <仓库根>/lang/droplet.dpt]
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
BUDGET_TOK=1500
BUDGET_BYTES=8192
# 可以指着别的树量（破坏测试 #22 要把定义加胖，得在临时树里量）
FILE=${1:-$ROOT/lang/droplet.dpt}
case $FILE in /*) : ;; *) FILE=$PWD/$FILE ;; esac
cd "$ROOT"

if [ ! -s "$FILE" ]; then
  echo "挂：语言定义缺失 $FILE（预算无从谈起）" >&2
  exit 1
fi

python3 - "$BUDGET_TOK" "$BUDGET_BYTES" "$FILE" <<'PY'
import math, os, sys
ROOT_PLACEHOLDER = os.getcwd()

budget_tok, budget_bytes, path = int(sys.argv[1]), int(sys.argv[2]), sys.argv[3]
b = open(path, "rb").read()
non_ascii = sum(1 for x in b if x > 127)
tok = math.ceil((len(b) - non_ascii) / 4 + non_ascii / 3)
lines = b.count(b"\n")

print("语言定义（完整定义 + 错误码表）：")
print("  %-40s %8d B  %8d token(估算)  %3d 行" % (path.replace(ROOT_PLACEHOLDER, "") or path, len(b), tok, lines))
print("  预算 %d token / %d B -> 占用 %.1f%% / %.1f%%"
      % (budget_tok, budget_bytes, tok * 100.0 / budget_tok, len(b) * 100.0 / budget_bytes))

if tok > budget_tok or len(b) > budget_bytes:
    print("挂：定义超出预算 —— 这个提法的成立条件已经没了。"
          "必须删构造或删规则；抬预算要写在 docs/LANG2.md 里并由人签字。", file=sys.stderr)
    sys.exit(1)
print("绿：完整定义放得进一次阅读。")
PY
