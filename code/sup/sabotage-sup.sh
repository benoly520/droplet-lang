#!/bin/sh
# 超集形态的"改坏必有工具红"检查（docs/NEXT.md P16 的验收标准）。
# 每一行是一个破坏动作，期望是**某个工具失败**；实际"全绿"就是 MISS。
#
# 用法：sh code/sup/sabotage-sup.sh [起始端口]
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
PORT0=${1:-18850}
RT=$ROOT/code/lang/internal/runtime/assets
B=$ROOT/.build/sabotage
export TMPDIR=$ROOT/.build
rm -rf "$B"; mkdir -p "$B"

BUILD="cc -std=c11 -O2 -Wall -Wextra -Werror -I$B"
SOURCES="$RT/rt_core.c $RT/rt_http.c $RT/rt_blob.c $RT/rt_ver.c"
CAUGHT=0
MISS=0

prep() { # prep <dst> ：把超集三件套复制到沙箱目录
  cp "$ROOT/code/sup/dp_sup.h" "$ROOT/code/sup/blobd.c" "$B/"
  cp "$ROOT/code/sup/blobd.c" "$dst.c"
}

try_build() { # try_build <名> -> 0 表示构建成功
  cc -std=c11 -O2 -Wall -Wextra -Werror -I"$B" -I"$RT" "$B/$1.c" $SOURCES -o "$B/$1" >/dev/null 2>&1
}

report() { # report <编号> <破坏动作> <期望失败的工具> <结果 0=失败即抓住>
  if [ "$4" = "caught" ]; then
    CAUGHT=$((CAUGHT+1)); echo "  ok   S$1 $2 -> $3 抓住了"
  else
    MISS=$((MISS+1));  echo "  FAIL S$1 $2 -> 没有任何工具失败（假阴性）"
  fi
}

# ---- S1：表里删一行，handler 留着（路由被"重构掉"） ----
dst=$B/s1; prep
python3 - "$B" <<'PY'
import sys
b = sys.argv[1]
s = open(b + "/s1.c", encoding="utf-8").read()
lines = s.split("\n"); out = []; i = 0
while i < len(lines):
    if 'X("POST", "/v1/keys"' in lines[i]:
        i += 2; continue
    out.append(lines[i]); i += 1
open(b + "/s1.c", "w", encoding="utf-8").write("\n".join(out))
PY
if grep -q 'POST", "/v1/keys' "$B/s1.c"; then echo "S1 注入失败：POST 那行没被删掉（拒绝假装抓住）" >&2; exit 1; fi
if try_build s1; then r=miss; else r=caught; fi
report 1 "路由表里删掉 POST（实现还在）" "编译器（-Wunused-function）" "$r"

# ---- S2：表里列一个不存在的实现 ----
dst=$B/s2; prep
sed 's/h_keys_create, DP_OK_CREATED/h_keys_gone, DP_OK_CREATED/' "$B/blobd.c" > "$B/s2.c"
grep -q 'h_keys_gone' "$B/s2.c" || { echo "S2 注入失败：不存在的实现名没写进表" >&2; exit 1; }
if try_build s2; then r=miss; else r=caught; fi
report 2 "表里列一个不存在的实现" "编译器（used but never defined）" "$r"

# ---- S3：读一个没声明的环境变量 ----
dst=$B/s3; prep
python3 - "$B" <<'PY'
import sys
b = sys.argv[1]
s = open(b + "/s3.c", encoding="utf-8").read()
s = s.replace("static int h_keys_list(dp_arena* a", """static int h_sneaky(dp_arena* a, const dp_request* rq, dp_bytes* body) {
  const char* v = getenv("DP_LOG_LEVEL"); /* 没在任何表里声明过 */
  (void)a; (void)rq; (void)body; (void)v;
  return 0;
}

static int h_keys_list(dp_arena* a""", 1)
open(b + "/s3.c", "w", encoding="utf-8").write(s)
PY
# 让 h_sneaky 真的被调用：挂到 health 之前
python3 - "$B" <<'PY'
import sys
b = sys.argv[1]
s = open(b + "/s3.c", encoding="utf-8").read()
s = s.replace('''static int h_health(dp_arena* a, const dp_request* rq, dp_bytes* body) {
  (void)a;
  (void)rq;''', '''static int h_health(dp_arena* a, const dp_request* rq, dp_response* out) {
  (void)a;
  if (h_sneaky(a, rq, body) != 0) {
    return (int)DP_ERR_IO;
  }''')
open(b + "/s3.c", "w", encoding="utf-8").write(s)
PY
grep -q 'h_sneaky' "$B/s3.c" || { echo "S3 注入失败：h_sneaky 没插进源码" >&2; exit 1; }
if try_build s3; then r=miss; else r=caught; fi
report 3 "读未声明的环境变量" "编译/链接期（getenv 被改名）" "$r"

# ---- S4：目标谎报一个不存在的断言编号（构建能过，契约核对必须红） ----
dst=$B/s4; prep
python3 "$ROOT/code/sup/sabotage_s4.py" "$B/s4.c"
if try_build s4; then
  if python3 "$ROOT/code/sup/contract-sup.py" "$B/s4" "$ROOT/code/sup/accept-sup.sh" >/dev/null 2>&1
  then r=miss; else r=caught; fi
else
  r=miss
fi
report 4 "目标谎称被一条不存在的断言证明" "contract-sup.py" "$r"

# ---- S5：删掉那条断言，声称它证明的目标就站不住 ----
grep -v '^ck 20 ' "$ROOT/code/sup/accept-sup.sh" > "$B/accept-no20.sh"
if python3 "$ROOT/code/sup/contract-sup.py" "$ROOT/.build/sup/blobd" "$B/accept-no20.sh" >/dev/null 2>&1
then r=miss; else r=caught; fi
report 5 "从验收表里删掉断言 #20" "contract-sup.py" "$r"

# ---- S6：把 P16 那个 bug 塞回实现里（bool 不消费）----
dst=$B/s6; prep
python3 - "$B" <<'PY6'
import sys
b = sys.argv[1]
s = open(b + "/dp_sup.h", encoding="utf-8").read()
s = s.replace("  return fresh ? 0 : (int)DP_ERR_CONFLICT;", "  (void)fresh; return 0; /* 把 bool 丢掉：P16 的原始错误 */")
open(b + "/dp_sup.h", "w", encoding="utf-8").write(s)
PY6
grep -q '(void)fresh' "$B/dp_sup.h" || { echo "S6 注入失败：dp_created 的写法变了，脚本要跟着改" >&2; exit 1; }
if try_build s6; then
  if sh "$ROOT/code/sup/accept-sup.sh" "$B/s6" "$((PORT0+5))" sab6 >/dev/null 2>&1
  then r=miss; else r=caught; fi
else
  r=miss
fi
report 6 "把「丢弃 create 的 bool」塞回运行时胶水层" "accept-sup.sh #20" "$r"


# ---- S7：拼接出来的环境变量名（上一轮击穿 45 行正则核对器的那个写法）----
dst=$B/s7; prep
python3 - "$B" <<'PY7'
import sys
b = sys.argv[1]
s = open(b + "/s7.c", encoding="utf-8").read()
s = s.replace("#include \"dp_sup.h\"", "#include \"dp_sup.h\"\n#include <stdarg.h>")
s = s.replace("""static int h_health(dp_arena* a, const dp_request* rq, dp_bytes* body) {
  (void)a;
  (void)rq;""", """static int h_health(dp_arena* a, const dp_request* rq, dp_bytes* body) {
  char nm[32];
  snprintf(nm, sizeof nm, "%s%s%s", "DP_", "LOG_", "LEVEL");
  (void)a;
  (void)rq;
  if (getenv(nm) != NULL) {
    return (int)DP_ERR_IO;
  }""")
open(b + "/s7.c", "w", encoding="utf-8").write(s)
PY7
grep -q 'getenv(nm)' "$B/s7.c" || { echo "S7 注入失败：拼接名那段没插进去" >&2; exit 1; }
if try_build s7; then r=miss; else r=caught; fi
report 7 "用 snprintf 拼出未声明的环境变量名再读" "编译期（宏改的是函数名，不看名字怎么来）" "$r"

echo "sabotage-sup: $CAUGHT 抓住 / $MISS 假阴性"
[ "$MISS" -eq 0 ]
