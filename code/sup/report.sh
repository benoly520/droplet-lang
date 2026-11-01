#!/bin/sh
# 人类验收页：一屏看完"目标有没有证据"，不用读实现。
#
# 为什么是这一张：这个工程的承诺是"人只看结果"。结果不是 26 个数字，而是
# **每条服务目标由哪几条断言证明、这些断言这一版是不是绿的**。所以报告按目标组织，
# 不按断言编号组织 —— 编号是给机器的，目标是给人的。
#
# 口径：
#   * 目标、断言编号、路由、样例全部从编译产物的 `--contract` 里读，不从源码正则猜；
#   * 断言的红绿来自 `accept-table.sh` 的实际运行（--accept 时），没跑就写"未跑"，
#     绝不用"上次是绿的"代替；
#   * 未证目标（proven 为空）单独计数 —— 沉默不算决定。
#
# 用法：
#   sh code/sup/report.sh [二进制] [--accept <端口>]
#   不给了二进制就依次找 .build/check/blobd、.build/sup/blobd；都没有就先构建。
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$ROOT"

BIN=""
PORT=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --accept) PORT=${2:-18203}; shift 2 ;;
    *) BIN=$1; shift ;;
  esac
done

if [ -z "$BIN" ]; then
  for C in .build/check/blobd .build/sup/blobd; do
    if [ -x "$C" ]; then BIN=$C; break; fi
  done
fi
if [ -z "$BIN" ]; then
  sh code/sup/build.sh report >/dev/null
  BIN=.build/report/blobd
fi
if [ ! -x "$BIN" ]; then
  echo "挂：找不到可执行的服务二进制 $BIN（先跑 sh code/sup/build.sh）" >&2
  exit 2
fi

TMP=$ROOT/.build/report
rm -rf "$TMP"; mkdir -p "$TMP"
"$BIN" --contract > "$TMP/contract.json"

ACC=""
if [ -n "$PORT" ]; then
  LOG=$TMP/accept.log
  if sh code/sup/accept-sup.sh "$(CDPATH= cd -- "$(dirname -- "$BIN")" && pwd)/$(basename "$BIN")" "$PORT" report >"$LOG" 2>&1; then
    ACC=green
  else
    ACC=red
  fi
  echo "（验收：$(tail -n 1 "$LOG")）"
fi

python3 - "$TMP/contract.json" "$ACC" "$TMP/accept.log" <<'PY'
import json, re, sys

contract = json.load(open(sys.argv[1], encoding="utf-8"))
acc = sys.argv[2]
status = {}
try:
    with open(sys.argv[3], encoding="utf-8") as f:
        for line in f:
            m = re.match(r"\s+(ok|FAIL)\s+#(\d+)", line)
            if m:
                status[int(m.group(2))] = (m.group(1) == "ok")
except (OSError, IndexError):
    pass

def width(s):
    return sum(2 if ord(c) > 127 else 1 for c in s)

def pad(s, n):
    return s + " " * max(0, n - width(s))

goals = contract["goals"]
routes = contract["routes"]
examples = contract["examples"]
envs = contract["envs"]
unproven = [g for g in goals if not g["proven"]]

print()
print("Droplet 验收报告")
print(f"  介质：合法 C11 + 4 张表   |   路由 {len(routes)}   |   配置 {len(envs)}   |   样例 {len(examples)}   |   目标 {len(goals)}（未证 {len(unproven)}）")
print()
print("  " + pad("目标", 26) + pad("断言", 18) + "结果")
print("  " + "-" * 62)
for g in goals:
    ids = ", ".join("#" + p for p in g["proven"]) or "—"
    if not g["proven"]:
        res = "未证"
    elif acc == "":
        res = "未跑"
    elif any(status.get(int(p)) is False for p in g["proven"]):
        res = "红"
    elif any(int(p) not in status for p in g["proven"]):
        res = "未跑"
    else:
        res = "绿"
    print("  " + pad(g["id"], 26) + pad(ids, 18) + res)
print()
print("  路由：" + " / ".join(f"{r['method']} {r['path']}" for r in routes))
print(f"  样例：{len(examples)} 条封闭样例覆盖纯决定；未覆盖的读写与轮转由 wire 断言兜着")
if acc == "":
    print()
    print("  断言结果未附：加 --accept <端口> 跑一次验收表，这一栏才有红绿。")
    print("  本页只证明契约自洽，不证明服务这一版是好的。")
else:
    ok = sum(1 for v in status.values() if v)
    bad = sum(1 for v in status.values() if not v)
    print()
    print(f"  结论：{len(goals) - len(unproven)}/{len(goals)} 目标有断言；断言 {ok} 绿 / {bad} 红（接的是这一版二进制）")
PY
