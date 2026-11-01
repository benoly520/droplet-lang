#!/bin/sh
# 题面包：把一个"写决定层"的任务交给 AI 时，它到底该拿到什么。
#
# 这个包是**声明面**，不是仓库：不给参考实现（`dp_decide.c` 不在包里），不给测试脚本，
# 不给历史。给的是：四张表与类型、机器可读契约、任务判据、以及四条命令。
#
# 两条断言让"包"本身可失败：
#   ① 包里不许出现实现文件（dp_decide.c / blobd.c / rt_*.c）—— 那是答案，不是题面；
#   ② 包的字节与估算 token 必须在预算内（与 surface-budget.sh 同一口径）。
#
# 用法：sh code/sup/brief.sh <输出目录>
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
if [ "$#" -lt 1 ]; then
  echo "用法：sh code/sup/brief.sh <输出目录>" >&2
  exit 2
fi
OUT=$1
BUDGET_TOK=9000

rm -rf "$OUT"
mkdir -p "$OUT/surface"

# 契约从编译产物里导出，不从源码正则里猜。
sh "$ROOT/code/sup/build.sh" brief >/dev/null
"$ROOT/.build/brief/blobd" --contract > "$OUT/contract.json"

cp "$ROOT/code/sup/dp_env.h" "$ROOT/code/sup/dp_decide.h" "$ROOT/code/sup/dp_sup.h" \
   "$ROOT/code/sup/dp_conf.h" "$ROOT/code/runtime/rt.h" "$OUT/surface/"

cat > "$OUT/TASK.md" <<'EOS'
# 任务：写决定层（dp_decide.c）

交付物是**一个 C 文件**：实现 `surface/dp_decide.h` 里声明的那些纯决定。
判据不是你的解释，是下面四条命令的输出。

## 约束

- 函数体里只能有条件与常量：不读环境变量、不碰文件系统、不碰 arena、不做 IO。
- 不许修改 `surface/` 下的任何文件，也不许改样例表：声明是题面，不是草稿。
- 不要去找仓库里的参考实现。找到了也不算通过 —— 判据只认你交的文件。

## 判据（按顺序，第一条红就停）

```sh
sh code/sup/check.sh                 # 构建 + 样例 + 契约 + 必读面预算
sh code/sup/submit-sup.sh 你的.c     # 整条面：构建 / 19 样例 / 26 断言 / 契约
```

`check.sh` 会在 `.build/check-logs/` 留下每一项的日志；红了只看第一项。
`submit-sup.sh` 会在一份临时树里跑，不接受就不碰工作区。

## 你会拿到什么

- `surface/`：四张表与类型（`dp_env.h`、`dp_decide.h`、`dp_sup.h`、`dp_conf.h`、`rt.h`）
- `contract.json`：路由 / 配置 / 目标 / 样例四节的机器可读版本
- 本文件：任务与判据

包里**没有** `dp_decide.c`、`blobd.c`、`rt_*.c`。缺的那份就是你要写的。
EOS

# 断言 ①：包里不许有实现文件。
LEAK=0
for F in dp_decide.c blobd.c rt_core.c rt_http.c rt_blob.c rt_ver.c dp_cfg.c; do
  if [ -e "$OUT/$F" ] || [ -e "$OUT/surface/$F" ]; then
    echo "挂：题面包里出现了实现文件 $F —— 那是答案，不是题面" >&2
    LEAK=1
  fi
done
[ "$LEAK" -eq 0 ] || exit 1

# 断言 ②：包必须在预算内（与 surface-budget.sh 相同估算：ASCII/4 + 非ASCII/3）。
python3 - "$OUT" "$BUDGET_TOK" <<'PY'
import math, pathlib, sys

out, budget = pathlib.Path(sys.argv[1]), int(sys.argv[2])

def est(b: bytes) -> int:
    na = sum(1 for x in b if x > 127)
    return math.ceil((len(b) - na) / 4 + na / 3)

files = sorted(p for p in out.rglob("*") if p.is_file())
total_b = total_t = 0
print(f"{'题面包':<28}{'字节':>8}{'估算token':>10}")
for p in files:
    b = p.read_bytes()
    total_b += len(b)
    total_t += est(b)
    print(f"{str(p.relative_to(out)):<28}{len(b):>8}{est(b):>10}")
print(f"{'合计':<28}{total_b:>8}{total_t:>10}")
if total_t > budget:
    print(f"挂：题面包 {total_t} token 超过预算 {budget}", file=sys.stderr)
    sys.exit(1)
print(f"题面包在预算内（{total_t}/{budget} token，估算）")
PY

echo "题面包：$OUT（surface 5 个文件 + contract.json + TASK.md；无实现文件）"
