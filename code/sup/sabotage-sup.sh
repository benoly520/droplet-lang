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
CAUGHT=0
MISS=0

prep() {
  cp "$ROOT"/code/sup/dp_sup.h "$ROOT"/code/sup/dp_conf.h "$ROOT"/code/sup/dp_env.h \
     "$ROOT"/code/sup/dp_decide.h "$ROOT"/code/sup/dp_decide.c \
     "$ROOT"/code/sup/dp_cfg.c "$ROOT"/code/sup/blobd.c "$ROOT"/code/sup/audit-sup.sh "$B/"
  cp "$RT"/rt.h "$B/"
}

# 与 build.sh 同样的三段：编用户 TU -> 符号审计 -> 链接。日志留档。
# 运行时也逐 TU 编成 .o（与 build.sh 同步），这样接线检查能看见运行时引用了
# 哪一份决定；HTTP_NAME / HTTP_SRC 允许某条破坏换掉 rt_http.c。
try_build() {
  NAME=$1
  CFG_NAME=${CFG_NAME:-dp_cfg}
  HTTP_NAME=${HTTP_NAME:-rt_http}
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
  for T in rt_core rt_blob rt_ver; do
    if [ ! -f "$B/$T.o" ]; then
      # shellcheck disable=SC2086
      cc $FLAGS -I"$B" -I"$RT" -c "$RT/$T.c" -o "$B/$T.o" >>"$LOG" 2>&1 || return 1
    fi
  done
  if [ ! -f "$B/$HTTP_NAME.o" ]; then
    # shellcheck disable=SC2086
    cc $FLAGS -I"$B" -I"$RT" -c "${HTTP_SRC:-$RT/rt_http.c}" -o "$B/$HTTP_NAME.o" >>"$LOG" 2>&1 || return 1
  fi
  # shellcheck disable=SC2086
  if ! sh "$B/audit-sup.sh" "$B/$NAME.o" "$B/dp_decide.o" >>"$LOG" 2>&1; then
    return 1
  fi
  # 与 build.sh 同样的接线检查：五处被样例覆盖的决定都要有引用者
  for PAIR in "$NAME.o dec_create_result" "$CFG_NAME.o dec_port_usable" \
              "$HTTP_NAME.o dec_precondition_result" "$HTTP_NAME.o dec_auth_ok" \
              "$HTTP_NAME.o dec_body_too_large"; do
    OBJ=$(printf '%s' "$PAIR" | cut -d' ' -f1)
    SYM=$(printf '%s' "$PAIR" | cut -d' ' -f2)
    if ! nm -u "$B/$OBJ" | awk '{print $2}' | grep -qx "$SYM"; then
      echo "接线红：$OBJ 没有引用 $SYM —— DP_EXAMPLES 验的就不是服务跑的那份" >>"$LOG" 2>&1
      return 1
    fi
  done
  # shellcheck disable=SC2086
  cc $FLAGS -I"$B" -I"$RT" "$B/$NAME.o" "$B/$CFG_NAME.o" "$B/dp_decide.o" \
     "$B/$HTTP_NAME.o" "$B/rt_core.o" "$B/rt_blob.o" "$B/rt_ver.o" -o "$B/$NAME" >>"$LOG" 2>&1
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
  report_caught s11 11 "接线检查（nm -u）" "把端口区间就地抄回 dp_cfg.c：构建行为不变、27 断言不变，但样例验的不再是跑的那份"
fi
CFG_NAME=dp_cfg

# ---- S12：把条件请求的判断就地抄回运行时（第三条接线，接法与 S10/S11 相同） ----
# 判断从运行时搬进用户面之后，运行时只做"读"和"算"。把它抄回去，行为完全等价、
# 27 条断言照样全过 —— 被绕开的仍然是"样例验的是跑的那一份"这件事本身。
prep
python3 - "$B" "$RT/rt_http.c" <<'PY12'
import sys
b, src = sys.argv[1], sys.argv[2]
s = open(src, encoding="utf-8").read()
old = "  return dec_precondition_result(err, exists, match);"
new = ("  (void)dec_precondition_result; /* 假装还用着 */\n"
       "  if (err != 0 && err != (int)DP_ERR_NOT_FOUND) {\n"
       "    return err;\n"
       "  }\n"
       "  if (!exists) {\n"
       "    return (int)DP_ERR_PRECONDITION_FAILED;\n"
       "  }\n"
       "  return match ? 0 : (int)DP_ERR_PRECONDITION_FAILED;")
if old not in s:
    sys.exit("S12 注入失败：dp_precondition_check 的写法变了，脚本要跟着改")
open(b + "/s12-http.c", "w", encoding="utf-8").write(s.replace(old, new, 1))
PY12
grep -q '(void)dec_precondition_result' "$B/s12-http.c" \
  || { echo "S12 注入失败：抄本没写进 rt_http.c" >&2; exit 1; }
cp "$B/blobd.c" "$B/s12.c"
HTTP_NAME=s12-http
HTTP_SRC="$B/s12-http.c"
if try_build s12; then report_miss 12 "把条件请求的判断抄回运行时（行为等价、样例失效）"; else
  require_log s12 12 "接线红：s12-http.o 没有引用 dec_precondition_result"
  report_caught s12 12 "接线检查（nm -u）" "把条件请求判断抄回 rt_http.c：行为不变，但样例验的不再是跑的那份"
fi
HTTP_NAME=rt_http
HTTP_SRC=

# ---- S13：把鉴权判断就地抄回运行时（第四条接线）----
# 行为完全等价：配了 token 且不是 /health 时，没带头或头不对就 401。27 条断言照样全过，
# 被绕开的仍是"样例验的是跑的那一份"。
prep
python3 - "$B" "$RT/rt_http.c" <<'PY13'
import sys
b, src = sys.argv[1], sys.argv[2]
s = open(src, encoding="utf-8").read()
old = "  if (!dec_auth_ok(cfg->token.n > 0u, is_health, header_present, header_matches)) {"
new = ("  (void)dec_auth_ok; /* 假装还用着 */\n"
       "  if (cfg->token.n > 0u && !is_health && !(header_present && header_matches)) {")
if old not in s:
    sys.exit("S13 注入失败：鉴权那行的写法变了，脚本要跟着改")
open(b + "/s13-http.c", "w", encoding="utf-8").write(s.replace(old, new, 1))
PY13
grep -q '(void)dec_auth_ok' "$B/s13-http.c" \
  || { echo "S13 注入失败：抄本没写进 rt_http.c" >&2; exit 1; }
cp "$B/blobd.c" "$B/s13.c"
HTTP_NAME=s13-http
HTTP_SRC="$B/s13-http.c"
if try_build s13; then report_miss 13 "把鉴权判断抄回运行时（行为等价、样例失效）"; else
  require_log s13 13 "接线红：s13-http.o 没有引用 dec_auth_ok"
  report_caught s13 13 "接线检查（nm -u）" "把鉴权判断抄回 rt_http.c：行为不变，但样例验的不再是跑的那份"
fi
HTTP_NAME=rt_http
HTTP_SRC=

# ---- S14：把 body 上限判断就地抄回运行时（第五条接线）----
prep
python3 - "$B" "$RT/rt_http.c" <<'PY14'
import sys
b, src = sys.argv[1], sys.argv[2]
s = open(src, encoding="utf-8").read()
old = "  if (dec_body_too_large(content_len, cfg->max_body)) {"
new = ("  (void)dec_body_too_large; /* 假装还用着 */\n"
       "  if (content_len > cfg->max_body) {")
if old not in s:
    sys.exit("S14 注入失败：body 上限那行的写法变了，脚本要跟着改")
open(b + "/s14-http.c", "w", encoding="utf-8").write(s.replace(old, new, 1))
PY14
grep -q '(void)dec_body_too_large' "$B/s14-http.c" \
  || { echo "S14 注入失败：抄本没写进 rt_http.c" >&2; exit 1; }
cp "$B/blobd.c" "$B/s14.c"
HTTP_NAME=s14-http
HTTP_SRC="$B/s14-http.c"
if try_build s14; then report_miss 14 "把 body 上限判断抄回运行时（行为等价、样例失效）"; else
  require_log s14 14 "接线红：s14-http.o 没有引用 dec_body_too_large"
  report_caught s14 14 "接线检查（nm -u）" "把 body 上限判断抄回 rt_http.c：行为不变，但样例验的不再是跑的那份"
fi
HTTP_NAME=rt_http
HTTP_SRC=

# ---- S15/S16/S17：题面与译稿的破坏（翻译层，v0.36）----
# 这三条测的不是实现，是“题能不能被悄悄改简单”。所以先建一份**干净的临时树**证明三道门
# 原本都绿，再逐条注入 —— 注入本身坏了（比如文件没被改）必须当场退出，不许算抓住。
QT=$B/qtree
rm -rf "$QT"; mkdir -p "$QT"
cp -a "$ROOT/code" "$QT/code"
cp -a "$ROOT/req" "$QT/req"
QT_REF=$B/clean   # S5 已经建好的干净参照二进制

if sh "$QT/code/sup/freeze.sh" --check "$QT_REF" >/dev/null 2>&1    && python3 "$QT/code/sup/req-check.py" "$QT_REF" >/dev/null 2>&1; then
  echo "  参照：临时树 freeze + req-check 全绿（破坏才有比对基准）"
else
  echo "  参照临时树本身就红，破坏测试无从比对" >&2
  sh "$QT/code/sup/freeze.sh" --check "$QT_REF" 2>&1 | sed 's/^/      /' >&2 || true
  exit 1
fi

# S15：从验收表里删掉 ck 27 —— 新增条款的断言没了，题被改简单
grep -q '^ck 27 ' "$QT/code/webapi/deploy/accept-table.sh" \
  || { echo "S15 注入失败：表里没有 ck 27" >&2; exit 1; }
sed -i '/^ck 27 /d' "$QT/code/webapi/deploy/accept-table.sh"
if sh "$QT/code/sup/freeze.sh" --check "$QT_REF" >/dev/null 2>&1
then report_miss 15 "从验收表删掉断言 #27（题被改简单）"
else
  sh "$QT/code/sup/freeze.sh" --check "$QT_REF" 2>&1 | grep -q 'accept-table.sh 被改过' \
    || { echo "  S15 红了但不是 freeze 指名 accept-table.sh（拒绝把别的红算成抓住）" >&2; exit 1; }
  report_caught s15 15 "freeze.sh --check" "从验收表删掉断言 #27：哈希不符 -> “题被改过”"
fi

# S16：删掉 R17 的译法记录 —— 检查还在、还是绿的，但没人说过它为什么这么写
rm -rf "$QT/code" "$QT/req"
cp -a "$ROOT/code" "$QT/code"
cp -a "$ROOT/req" "$QT/req"
grep -q '^R17' "$QT/req/TRANS.tsv" || { echo "S16 注入失败：TRANS 里没有 R17" >&2; exit 1; }
sed -i '/^R17/d' "$QT/req/TRANS.tsv"
if python3 "$QT/code/sup/req-check.py" "$QT_REF" >/dev/null 2>&1
then report_miss 16 "删掉 R17 的 TRANS 行（没有译法记录）"
else
  python3 "$QT/code/sup/req-check.py" "$QT_REF" 2>&1 | grep -q 'R17 在 TRANS.tsv 里没有译法记录' \
    || { echo "  S16 红了但不是 req-check 指名缺译法记录" >&2; exit 1; }
  report_caught s16 16 "req-check.py（TRANS 台账）" "删掉 R17 的译法记录：条款没有“为什么这么检查”的账 -> 红"
fi

# S17：把一条歧义从“已定”改成“未定” —— 读法没定就先去实现，等于让 AI 替人拍板
rm -rf "$QT/req"
cp -a "$ROOT/req" "$QT/req"
grep -q '^A2' "$QT/req/AMBIG.tsv" || { echo "S17 注入失败：AMBIG 里没有 A2" >&2; exit 1; }
sed -i '/^A2/s/\t已定$/\t未定/' "$QT/req/AMBIG.tsv"
awk -F '\t' '$1 == "A2" && $NF == "未定" { f = 1 } END { exit f ? 0 : 1 }' "$QT/req/AMBIG.tsv" \
  || { echo "S17 注入失败：A2 状态没改成未定" >&2; exit 1; }
if python3 "$QT/code/sup/req-check.py" "$QT_REF" >/dev/null 2>&1
then report_miss 17 "把歧义 A2 改成未定"
else
  python3 "$QT/code/sup/req-check.py" "$QT_REF" 2>&1 | grep -q '还没有定论' \
    || { echo "  S17 红了但不是 req-check 指名歧义未定" >&2; exit 1; }
  report_caught s17 17 "req-check.py（AMBIG 台账）" "歧义改成未定：读法没定论就不许往下走 -> 红"
fi

echo "sabotage-sup: $CAUGHT 抓住 / $MISS 假阴性"
[ "$MISS" -eq 0 ]
