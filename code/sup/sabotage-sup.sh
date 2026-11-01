#!/bin/sh
# 破坏测试（docs/SPEC.md §6 的判据）：每个破坏动作期望"某个工具红"。
#
# 这一版比上一版多一道规矩：**每条都要断言是哪个工具抓的**。上一版里 S3/S7/S8 其实是因为
# 注入的代码少了 #include <stdlib.h> 而编译失败，却被报告成"符号审计抓住了"——那是
# 假绿。现在每条注入都会把构建日志留在 .log 里，并核对该出现的诊断确实出现；不对就整轮
# 失败退出，不允许"反正红了就算抓住"。
#
# 用法：sh code/sup/sabotage-sup.sh [起始端口]
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
PORT0=${1:-18900}
RT=$ROOT/code/runtime
B=$ROOT/.build/sabotage
export TMPDIR=$ROOT/.build
rm -rf "$B"; mkdir -p "$B"

FLAGS="-std=c11 -O2 -Wall -Wextra -Werror"
SOURCES="$RT/rt_core.c $RT/rt_http.c $RT/rt_blob.c $RT/rt_ver.c"
CAUGHT=0
MISS=0

prep() {
  cp "$ROOT"/code/sup/dp_sup.h "$ROOT"/code/sup/dp_conf.h "$ROOT"/code/sup/dp_env.h \
     "$ROOT"/code/sup/dp_decide.h "$ROOT"/code/sup/dp_decide.c \
     "$ROOT"/code/sup/dp_cfg.c "$ROOT"/code/sup/blobd.c "$ROOT"/code/sup/audit-sup.sh "$B/"
  cp "$RT"/rt.h "$B/"
}

# 与 build.sh 同样的三段：编用户 TU -> 符号审计 -> 链接。日志留档。
try_build() {
  NAME=$1
  CFG_NAME=${CFG_NAME:-dp_cfg}
  LOG="$B/$NAME.log"
  : > "$LOG"
  # shellcheck disable=SC2086
  if ! cc $FLAGS -I"$B" -I"$RT" -c "$B/$NAME.c" -o "$B/$NAME.o" >>"$LOG" 2>&1; then
    return 1
  fi
  if [ ! -f "$B/$CFG_NAME.o" ]; then
    # shellcheck disable=SC2086
    cc $FLAGS -I"$B" -I"$RT" -c "$B/$CFG_NAME.c" -o "$B/$CFG_NAME.o" >>"$LOG" 2>&1 || return 1
  fi
  if [ ! -f "$B/dp_decide.o" ]; then
    # shellcheck disable=SC2086
    cc $FLAGS -I"$B" -I"$RT" -c "$B/dp_decide.c" -o "$B/dp_decide.o" >>"$LOG" 2>&1 || return 1
  fi
  # shellcheck disable=SC2086
  if ! sh "$B/audit-sup.sh" "$B/$NAME.o" "$B/dp_decide.o" >>"$LOG" 2>&1; then
    return 1
  fi
  # 与 build.sh 同样的接线检查：两处被样例覆盖的决定都要有引用者
  for PAIR in "$NAME.o dec_create_result" "$CFG_NAME.o dec_port_usable"; do
    OBJ=$(printf '%s' "$PAIR" | cut -d' ' -f1)
    SYM=$(printf '%s' "$PAIR" | cut -d' ' -f2)
    if ! nm -u "$B/$OBJ" | awk '{print $2}' | grep -qx "$SYM"; then
      echo "接线红：$OBJ 没有引用 $SYM —— DP_EXAMPLES 验的就不是服务跑的那份" >>"$LOG" 2>&1
      return 1
    fi
  done
  # shellcheck disable=SC2086
  cc $FLAGS -I"$B" -I"$RT" "$B/$NAME.o" "$B/$CFG_NAME.o" "$B/dp_decide.o" $SOURCES -o "$B/$NAME" >>"$LOG" 2>&1
}

# 从 $B 里现编一个 oracle（S6 要用它验"样例与 wire 同时抓住同一个 bug"）
try_oracle() { # try_oracle <前缀> -> 0 表示样例表全绿；非 0 表示有样例翻面
  NAME=$1
  LOG="$B/$NAME-oracle.log"
  : > "$LOG"
  cc $FLAGS -I"$B" -I"$RT" -c "$B/dp_decide.c" -o "$B/$NAME-dec.o" >>"$LOG" 2>&1 || return 9
  cc $FLAGS -I"$B" -I"$RT" -c "$ROOT/code/sup/dp_oracle.c" -o "$B/$NAME-orc.o" >>"$LOG" 2>&1 || return 9
  cc $FLAGS -I"$B" -I"$RT" "$B/$NAME-orc.o" "$B/$NAME-dec.o" -o "$B/$NAME-oracle" >>"$LOG" 2>&1 || return 9
  "$B/$NAME-oracle" >>"$LOG" 2>&1
}

# 断言"确实是这个工具抓的"，不接受"反正红了"
require_log() { # require_log <名> <编号> <必须出现的模式>
  if ! grep -q "$3" "$B/$1.log"; then
    echo "  #$2 注入方式不对：失败不是由「$3」造成的，日志如下" >&2
    sed 's/^/      /' "$B/$1.log" >&2
    exit 1
  fi
}

report_caught() { # report_caught <名> <编号> <工具> <描述>
  echo "  抓住 #$2（$3 红）：$4"
  CAUGHT=$((CAUGHT + 1))
}
report_miss() {
  echo "  假阴性 #$1：$2 —— 没有任何工具红" >&2
  MISS=$((MISS + 1))
}

# ---- S1：表里删一行，handler 留着（路由被"重构掉"） ----
prep
sed '/X("POST", "\/v1\/keys"/,+1d' "$B/blobd.c" > "$B/s1.c"
grep -q 'X("POST", "/v1/keys"' "$B/s1.c" && { echo "S1 注入失败：POST 那行没被删掉（拒绝假装抓住）" >&2; exit 1; }
grep -q 'static int h_keys_create' "$B/s1.c" || { echo "S1 注入失败：handler 没留下，这一条要测的是'表里没列但函数还在'" >&2; exit 1; }
if try_build s1; then report_miss 1 "从 DP_ROUTES 删掉 POST 那行"; else
  require_log s1 1 "defined but not used"
  report_caught s1 1 "编译器 -Wunused-function" "从 DP_ROUTES 删掉 POST 那行，handler 留着不动"
fi

# ---- S2：表里列一个不存在的实现 ----
sed 's/h_keys_list, DP_OK_JSON/h_keys_gone, DP_OK_JSON/' "$B/blobd.c" > "$B/s2.c"
grep -q 'h_keys_gone' "$B/s2.c" || { echo "S2 注入失败：不存在的实现名没写进表" >&2; exit 1; }
if try_build s2; then report_miss 2 "表里列不存在的实现"; else
  require_log s2 2 "used but never defined"
  report_caught s2 2 "编译器 used-but-never-defined" "表里列一个不存在的实现"
fi

# ---- S3：读一个没声明的环境变量（写成合法 C：自带 <stdlib.h>，所以编译一定过） ----
prep
python3 - "$B" <<'PY3'
import sys
b = sys.argv[1]
s = open(b + "/blobd.c", encoding="utf-8").read()
s = s.replace('#include <stdio.h>', '#include <stdio.h>\n#include <stdlib.h>')
s = s.replace('''static int h_keys_list(dp_arena* a, const dp_request* rq, dp_bytes* body) {
  (void)rq;''', '''static int h_sneaky(dp_arena* a, const dp_request* rq) {
  const char* v = getenv("DP_LOG_LEVEL"); /* 没在任何表里声明过 */
  (void)a; (void)rq;
  return v != NULL;
}

static int h_keys_list(dp_arena* a, const dp_request* rq, dp_bytes* body) {
  if (h_sneaky(a, rq)) {
    return (int)DP_ERR_IO;
  }''')
open(b + "/s3.c", "w", encoding="utf-8").write(s)
PY3
grep -q 'h_sneaky' "$B/s3.c" || { echo "S3 注入失败：h_sneaky 没插进源码" >&2; exit 1; }
if try_build s3; then report_miss 3 "读未声明的环境变量"; else
  require_log s3 3 "审计红"
  report_caught s3 3 "符号审计（U getenv）" "读未声明的环境变量（合法 C 写法，编译通过）"
fi

# ---- S4：目标谎报一个不存在的断言编号（构建必须能过，核对必须红） ----
cp "$B/blobd.c" "$B/s4.c"
python3 "$ROOT/code/sup/sabotage_s4.py" "$B/s4.c" || { echo "S4 注入失败" >&2; exit 1; }
if ! try_build s4; then
  echo "  S4 注入方式不对：谎报断言号不该影响构建，但构建失败了" >&2
  sed 's/^/      /' "$B/s4.log" >&2
  exit 1
fi
if python3 "$ROOT/code/sup/contract-sup.py" "$B/s4" >/dev/null 2>&1
then report_miss 4 "目标谎报断言编号 #99"; else
  report_caught s4 4 "contract-sup.py" "目标谎报一个不存在的断言编号 #99（构建照过，核对必红）"
fi

# ---- S5：删掉那条断言，它声称证明的目标就站不住 ----
cp "$B/blobd.c" "$B/clean.c"
try_build clean || { echo "参照构建失败，S5 无从比对" >&2; exit 1; }
TABLE=$ROOT/code/webapi/deploy/accept-table.sh
grep -q '^ck 20 ' "$TABLE" || { echo "S5 注入失败：表里没有 ck 20" >&2; exit 1; }
sed '/^ck 20 /d' "$TABLE" > "$B/accept-no20.sh"
if python3 "$ROOT/code/sup/contract-sup.py" "$B/clean" "$B/accept-no20.sh" >/dev/null 2>&1
then report_miss 5 "删掉断言 #20"; else
  report_caught s5 5 "contract-sup.py" "从验收表里删掉断言 #20（create_once 失去证明）"
fi

# ---- S6：把"已存在也报成功"那个 bug 塞回决定层 ----
# v0.32 起这个 bug 住在 dp_decide.c，所以同一个破坏会被**两个**工具各自抓住：
# 样例 #2 在单元级翻面，断言 #20 在 wire 级翻面。两个都必须红，只红一个算归因不全。
prep
python3 - "$B" <<'PY6'
import sys
b = sys.argv[1]
s = open(b + "/dp_decide.c", encoding="utf-8").read()
s = s.replace("  return fresh ? 0 : (int)DP_ERR_CONFLICT;",
              "  (void)fresh; return 0; /* 把 bool 丢掉：原始错误回来了 */")
open(b + "/dp_decide.c", "w", encoding="utf-8").write(s)
PY6
grep -q '(void)fresh' "$B/dp_decide.c" || { echo "S6 注入失败：dec_create_result 的写法变了，脚本要跟着改" >&2; exit 1; }
rm -f "$B/dp_decide.o"
cp "$B/blobd.c" "$B/s6.c"
if ! try_build s6; then
  echo "  S6 连构建都没过，注入方式不对" >&2; sed 's/^/      /' "$B/s6.log" >&2; exit 1
fi
ORACLE_RED=0
try_oracle s6 || ORACLE_RED=$?
WIRE_RED=0
if sh "$ROOT/code/sup/accept-sup.sh" "$B/s6" "$((PORT0+6))" sab6 >/dev/null 2>&1; then WIRE_RED=0; else WIRE_RED=1; fi
if grep -q '#2' "$B/s6-oracle.log" && grep -q 'FAIL  #2' "$B/s6-oracle.log"; then
  if [ "$WIRE_RED" -eq 1 ]; then
    report_caught s6 6 "oracle 样例 #2 + accept-sup #20" "把「丢弃 create 的 bool」塞回决定层（单元级与 wire 级同时红）"
  else
    echo "  假阴性 #6：样例翻了面但 wire 断言 #20 没红 —— 两级证据不一致，归因不成立" >&2
    MISS=$((MISS + 1))
  fi
else
  if [ "$WIRE_RED" -eq 1 ]; then
    echo "  假阴性 #6：wire 红了但样例没翻面（ORACLE 退出码 $ORACLE_RED）—— oracle 没接住这个 bug" >&2
    sed 's/^/      /' "$B/s6-oracle.log" >&2
    MISS=$((MISS + 1))
  else
    report_miss 6 "把丢弃 bool 塞回决定层"
  fi
fi

# ---- S7：拼接出来的环境变量名（上一轮击穿 45 行正则核对器的那个写法）----
prep
python3 - "$B" <<'PY7'
import sys
b = sys.argv[1]
s = open(b + "/blobd.c", encoding="utf-8").read()
s = s.replace('#include <stdio.h>', '#include <stdio.h>\n#include <stdlib.h>')
s = s.replace('''static int h_health(dp_arena* a, const dp_request* rq, dp_bytes* body) {
  (void)a;
  (void)rq;''', '''static int h_health(dp_arena* a, const dp_request* rq, dp_bytes* body) {
  char nm[32];
  snprintf(nm, sizeof nm, "%s%s%s", "DP_", "LOG_", "LEVEL");
  (void)a;
  (void)rq;
  if (getenv(nm) != NULL) {
    return (int)DP_ERR_IO;
  }''')
open(b + "/s7.c", "w", encoding="utf-8").write(s)
PY7
grep -q 'getenv(nm)' "$B/s7.c" || { echo "S7 注入失败：拼接名那段没插进去" >&2; exit 1; }
if try_build s7; then report_miss 7 "拼接环境变量名再读"; else
  require_log s7 7 "审计红"
  report_caught s7 7 "符号审计" "用 snprintf 拼出未声明的环境变量名再读"
fi

# ---- S8：E1 的写法 —— 宏不存在了，但"绕过声明面"照样留符号 ----
prep
python3 - "$B" <<'PY8'
import sys
b = sys.argv[1]
s = open(b + "/blobd.c", encoding="utf-8").read()
s = s.replace('#include <stdio.h>', '#include <stdio.h>\n#include <stdlib.h>')
s = s.replace('''static int h_keys_list(dp_arena* a, const dp_request* rq, dp_bytes* body) {
  (void)rq;''', '''static int h_keys_list(dp_arena* a, const dp_request* rq, dp_bytes* body) {
  /* E1 的对应写法：先 setenv 再 getenv，全程只用 libc 的正规名字 */
  if (getenv("DP_LOG_LEVEL") != NULL) {
    return (int)DP_ERR_IO;
  }
  (void)rq;''')
open(b + "/s8.c", "w", encoding="utf-8").write(s)
PY8
grep -q 'getenv("DP_LOG_LEVEL")' "$B/s8.c" || { echo "S8 注入失败：getenv 那段没插进去" >&2; exit 1; }
if try_build s8; then report_miss 8 "直接调 libc getenv"; else
  require_log s8 8 "审计红"
  report_caught s8 8 "符号审计" "不碰任何宏，直接按 libc 正规名字读没声明的变量（E1 类）"
fi

# ---- S9：E6 的写法 —— 一次 getenv 都不写，直接读 environ ----
prep
python3 - "$B" <<'PY9'
import sys
b = sys.argv[1]
s = open(b + "/blobd.c", encoding="utf-8").read()
s = s.replace('#include <string.h>', '#include <string.h>\nextern char **environ; /* E6：一次 getenv 都不写 */')
s = s.replace('''static int h_keys_list(dp_arena* a, const dp_request* rq, dp_bytes* body) {
  (void)rq;''', '''static int h_keys_list(dp_arena* a, const dp_request* rq, dp_bytes* body) {
  for (char** e = environ; e != NULL && *e != NULL; e++) {
    if (strncmp(*e, "DP_LOG_", 7u) == 0) {
      return (int)DP_ERR_IO;
    }
  }
  (void)rq;''')
open(b + "/s9.c", "w", encoding="utf-8").write(s)
PY9
grep -q 'extern char \*\*environ' "$B/s9.c" || { echo "S9 注入失败：environ 那段没插进去" >&2; exit 1; }
if try_build s9; then report_miss 9 "extern char **environ"; else
  require_log s9 9 "审计红"
  report_caught s9 9 "符号审计（U environ）" "不提 getenv，直接 extern char **environ 读环境表（E6 闭合）"
fi

# ---- S10：把决定就地重写回胶水层，绕开被样例覆盖的那一份（v0.32 新口子 E9） ----
# 兼容性没有限制"在 dp_sup.h 里直接写条件"，所以样例可能验的是一份没人跑的复制品。
# 接住它的不是 grep，是符号表：blobd.o 不再引用 dec_create_result -> 接线红。
prep
python3 - "$B" <<'PY10'
import sys
b = sys.argv[1]
s = open(b + "/dp_sup.h", encoding="utf-8").read()
s = s.replace("  return dec_create_result(fresh, err);",
              "  (void)dec_port_usable; /* 假装还用着决定层 */\n"
              "  return (err != 0) ? err : (fresh ? 0 : (int)DP_ERR_CONFLICT);")
open(b + "/dp_sup.h", "w", encoding="utf-8").write(s)
PY10
grep -q '(void)dec_port_usable' "$B/dp_sup.h" \
  || { echo "S10 注入失败：dp_created 的写法变了，脚本要跟着改" >&2; exit 1; }
cp "$B/blobd.c" "$B/s10.c"
if try_build s10; then report_miss 10 "把决定就地重写回胶水层（绕开样例）"; else
  require_log s10 10 "接线红"
  report_caught s10 10 "接线检查（nm -u）" "把 create 的决定就地写回胶水层，服务不再引用被样例覆盖的那份"
fi

# ---- S11：把区间的决定就地抄回机制 TU（第二条口子，接法与 S10 相同） ----
# 端口那条决定住在 dp_cfg.c（机制 TU）里被调用。行为完全等价，21 条断言照样全过 ——
# 被绕开的不是 bug 检查，是"样例验的是跑的那一份"这件事本身。所以这里的红必须是接线红。
prep
python3 - "$B" <<'PY11'
import sys
b = sys.argv[1]
s = open(b + "/dp_cfg.c", encoding="utf-8").read()
s = s.replace("!dec_port_usable(x)", "!(x >= 1ul && x <= 65535ul)")
open(b + "/s11-cfg.c", "w", encoding="utf-8").write(s)
PY11
grep -q '!(x >= 1ul && x <= 65535ul)' "$B/s11-cfg.c" \
  || { echo "S11 注入失败：dp_load_port 的写法变了，脚本要跟着改" >&2; exit 1; }
grep -q 'dec_port_usable(' "$B/s11-cfg.c" && { echo "S11 注入不彻底：抄本里还在调用决定层" >&2; exit 1; }
cp "$B/blobd.c" "$B/s11.c"
CFG_NAME=s11-cfg
if try_build s11; then report_miss 11 "把区间的决定抄回机制 TU（行为等价、样例失效）"; else
  require_log s11 11 "接线红：s11-cfg.o 没有引用 dec_port_usable"
  report_caught s11 11 "接线检查（nm -u）" "把端口区间就地抄回 dp_cfg.c：构建行为不变、24 断言不变，但样例验的不再是跑的那份"
fi
CFG_NAME=dp_cfg

echo "sabotage-sup: $CAUGHT 抓住 / $MISS 假阴性"
[ "$MISS" -eq 0 ]
