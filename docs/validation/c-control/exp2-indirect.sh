#!/bin/sh
# exp2 的下半：方案 B 原型（文本对账）的上限。
# 同一处未声明的环境变量读取，换成 snprintf 拼名 —— 对账器报"全部对得上"，
# 而二进制照读。结论：模式抽取会被一次普通重构击穿，不需要恶意。
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
S=$ROOT/docs/validation/c-control
A=$ROOT/code/lang/internal/runtime/assets
B=$ROOT/.build/c-control/exp2
mkdir -p "$B/data"
export TMPDIR=$ROOT/.build

echo "=== 基线：字面量写法，对账器报出未声明读取 ==="
if python3 "$S/surface-c.py" "$S/service2.c" "$S/manifest.json" >"$B/rc1" 2>&1; then rc=0; else rc=1; fi
head -3 "$B/rc1"; echo "退出码 = $rc（1 = 抓到未声明读取）"

echo
echo "=== 改成拼接名（语义一字未变）==="
python3 - "$S/service2.c" "$B/indirect.c" <<'PY'
import pathlib, sys
s = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
old = '  const char* dbg = getenv("DP_LOG_LEVEL");'
assert s.count(old) == 1
new = ('  static const char* parts[] = { "DP_", "LOG_", "LEVEL" };\n'
       '  char nm[32];\n'
       '  int nn = snprintf(nm, sizeof nm, "%s%s%s", parts[0], parts[1], parts[2]);\n'
       '  const char* dbg = (nn > 0) ? getenv(nm) : NULL;')
pathlib.Path(sys.argv[2]).write_text(s.replace(old, new), encoding="utf-8")
PY
if python3 "$S/surface-c.py" "$B/indirect.c" "$S/manifest.json" >"$B/rc2" 2>&1; then rc=0; else rc=1; fi
head -3 "$B/rc2"; echo "退出码 = $rc（0 = 对账器认为闭合）"

echo
echo "=== 但二进制照读：启动时带 DP_LOG_LEVEL=debug ==="
cc -std=c11 -O2 -Wall -Wextra -Werror -I"$S" -I"$A" \
   "$B/indirect.c" "$A/rt_core.c" "$A/rt_http.c" "$A/rt_blob.c" "$A/rt_ver.c" "$S/decisions.c" \
   -o "$B/blobd-indirect"
APP_BIND=127.0.0.1 APP_PORT=18823 APP_ROOT="$B/data" APP_TOKEN= DP_LOG_LEVEL=debug \
  "$B/blobd-indirect" 2>"$B/log" >/dev/null &
Srv=$!
trap 'kill $Srv 2>/dev/null || true' EXIT
sleep 0.6
head -1 "$B/log"
kill $Srv 2>/dev/null || true
