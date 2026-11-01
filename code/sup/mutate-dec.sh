#!/bin/sh
# 机械变异：把算子作用在 dp_decide.c 上，看同一批封闭样例够不够抓住。
# 关键不在算子多，而在**验的是服务真正链接的那一份**（不是抄本）。
# 数字记在 docs/VALIDATION.md。
#
# 三条口径（来自一次假绿换来的教训）：
#   ① 每个变异都断言**预期结局**，实际不符就报"误报"并整轮失败 —— 不许"反正红了就算抓住"；
#   ② 锚点必须唯一（count == 1），否则整轮失败 —— 防止把"根本没碰到目标函数"的假变异当发现；
#   ③ 先跑干净参照，它必须全绿；参照挂了则本轮结论作废。
# 等价变异记成 MISS 是刻意的：报告不替人判断"这处改动有没有意义"。
#
# 用法：sh code/sup/mutate-dec.sh [构建标签]
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
LABEL=${1:-mutate}
B=$ROOT/.build/$LABEL
export TMPDIR=$ROOT/.build
rm -rf "$B"
mkdir -p "$B"

cp "$ROOT"/code/sup/dp_decide.h "$ROOT"/code/sup/dp_decide.c "$ROOT"/code/sup/dp_oracle.c "$B/"
cp "$ROOT"/code/runtime/rt.h "$B/"

echo "== 干净参照 =="
if ! cc -std=c11 -O2 -Wall -Wextra -Werror -I"$B" -c "$B/dp_decide.c" -o "$B/decide.o" \
     || ! cc -std=c11 -O2 -Wall -Wextra -Werror -I"$B" -c "$B/dp_oracle.c" -o "$B/oracle.o" \
     || ! cc -std=c11 -O2 -Wall -Wextra -Werror -I"$B" "$B/oracle.o" "$B/decide.o" -o "$B/bin"; then
  echo "挂：干净参照编不过，本轮结论全部作废" >&2
  exit 1
fi
CLEAN_FAIL=0
N=0
for ID in $("$B/bin" --list | cut -f1); do
  N=$((N + 1))
  if ! "$B/bin" --only "$ID" >/dev/null 2>&1; then
    "$B/bin" --only "$ID" || true
    CLEAN_FAIL=$((CLEAN_FAIL + 1))
  fi
done
if [ "$CLEAN_FAIL" -ne 0 ]; then
  echo "挂：干净参照有 $CLEAN_FAIL 条样例不通过 —— 变异实验的前提不成立，本轮作废" >&2
  exit 1
fi
echo "  过：原件 $N 条样例全绿（表里 $N 行）"

python3 - "$B" "$N" <<'PY'
import pathlib, subprocess, sys

B = pathlib.Path(sys.argv[1])
N = int(sys.argv[2])
orig = (B / "dp_decide.c").read_text()

CREATE = """int dec_create_result(bool fresh, int err) {
  if (err != 0) {
    return err;
  }
  return fresh ? 0 : (int)DP_ERR_CONFLICT;
}"""
PORT = "bool dec_port_usable(unsigned long x) { return x >= 1ul && x <= 65535ul; }"
PRECON = """int dec_precondition_result(int read_err, bool exists, bool tag_matches) {
  if (read_err != 0 && read_err != (int)DP_ERR_NOT_FOUND) {
    return read_err;
  }
  if (!exists) {
    return (int)DP_ERR_PRECONDITION_FAILED;
  }
  return tag_matches ? 0 : (int)DP_ERR_PRECONDITION_FAILED;
}"""
AUTH = """bool dec_auth_ok(bool token_configured, bool is_health, bool header_present, bool header_matches) {
  if (!token_configured || is_health) {
    return true;
  }
  return header_present && header_matches;
}"""
BODY = "bool dec_body_too_large(size_t n, size_t max) { return n > max; }"

# (编号, 说明, 锚点, 替换, 预期结局)
MUTS = [
    ("M1", "常量 +1：端口上界 65535 改成 65536", PORT,
     "bool dec_port_usable(unsigned long x) { return x >= 1ul && x <= 65536ul; }", "CAUGHT"),
    ("M2", "边界翻转：下界 >= 改成 >", PORT,
     "bool dec_port_usable(unsigned long x) { return x > 1ul && x <= 65535ul; }", "CAUGHT"),
    ("M3", "边界翻转：上界 <= 改成 <", PORT,
     "bool dec_port_usable(unsigned long x) { return x >= 1ul && x < 65535ul; }", "CAUGHT"),
    ("M4", "分支删除：清空 err 守卫的 then", CREATE,
     CREATE.replace("    return err;\n", "", 1), "CAUGHT"),
    ("M5", "取值反转：fresh 的三目两支互换", CREATE,
     CREATE.replace("return fresh ? 0 : (int)DP_ERR_CONFLICT;",
                    "return fresh ? (int)DP_ERR_CONFLICT : 0;", 1), "CAUGHT"),
    ("M6", "等价变异：x >= 1ul 改成 x != 0ul（无符号下同一回事）", PORT,
     "bool dec_port_usable(unsigned long x) { return x != 0ul && x <= 65535ul; }", "MISS"),
    ("M7", "等价变异：err != 0 改成 err > 0 || err < 0", CREATE,
     CREATE.replace("if (err != 0) {", "if (err > 0 || err < 0) {", 1), "MISS"),
    ("M8", "清空函数体：dec_port_usable 什么都不返回（编译期就该响）", PORT,
     "bool dec_port_usable(unsigned long x) { (void)x; }", "COMPILE"),
    ("M9", "取值反转：条件请求忽略 ETag 是否相符，一律放行", PRECON,
     PRECON.replace("return tag_matches ? 0 : (int)DP_ERR_PRECONDITION_FAILED;",
                    "(void)tag_matches;\n  return 0;", 1), "CAUGHT"),
    ("M10", "错误吞掉：读旧版本失败也当通过（错误优先那条样例必须翻面）", PRECON,
     PRECON.replace("    return read_err;", "    return 0;", 1), "CAUGHT"),
    ("M11", "旁路删除：/health 不再公开（配了 token 时探活也要 401）", AUTH,
     AUTH.replace("if (!token_configured || is_health) {",
                  "(void)is_health;\n  if (!token_configured) {", 1), "CAUGHT"),
    ("M12", "鉴权失效：带了不带、对不对都放行", AUTH,
     AUTH.replace("return header_present && header_matches;",
                  "(void)header_present;\n  (void)header_matches;\n  return true;", 1), "CAUGHT"),
    ("M13", "边界翻转：body 上限 8388608 时，等于上限也拒绝（> 改 >=）", BODY,
     "bool dec_body_too_large(size_t n, size_t max) { return n >= max; }", "CAUGHT"),
]

FLAGS = "-std=c11 -O2 -Wall -Wextra -Werror"


def sh(cmd):
    return subprocess.run(cmd, shell=True, capture_output=True, text=True)


caught = miss = compile_hit = wrong = 0
for mid, name, anchor, repl, expect in MUTS:
    if orig.count(anchor) != 1:
        print(f"挂：{mid} 的锚点在 dp_decide.c 里出现 {orig.count(anchor)} 次 —— "
              f"这是假变异风险，整轮失败")
        sys.exit(1)
    d = B / mid
    d.mkdir(exist_ok=True)
    (d / "dp_decide.c").write_text(orig.replace(anchor, repl, 1))
    for f in ("dp_decide.h", "dp_oracle.c", "rt.h"):
        (d / f).write_text((B / f).read_text())

    steps = [sh(f'cc {FLAGS} -I{d} -c {d}/dp_decide.c -o {d}/decide.o'),
             sh(f'cc {FLAGS} -I{d} -c {d}/dp_oracle.c -o {d}/oracle.o'),
             sh(f'cc {FLAGS} -I{d} {d}/oracle.o {d}/decide.o -o {d}/bin')]
    built = all(s.returncode == 0 for s in steps)
    errlog = "\n".join(s.stderr for s in steps if s.stderr)
    (d / "build.log").write_text(errlog)

    if expect == "COMPILE":
        if built:
            print(f"  误报 {mid}：预期编不过，实际编过了 —— 归因不符，不算抓住")
            wrong += 1
            continue
        compile_hit += 1
        first = errlog.strip().splitlines()
        print(f"  抓住 {mid}（编译期 红）：{name}")
        print(f"        诊断：{first[0] if first else '(无)'}")
        continue

    if not built:
        print(f"  误报 {mid}：预期 {expect}，实际编不过 —— 归因不符，不算抓住")
        wrong += 1
        continue

    listing = sh(f'{d}/bin --list').stdout
    ids = [line.split("\t")[0] for line in listing.splitlines() if line.strip()]
    flipped = [i for i in ids if sh(f'{d}/bin --only {i}').returncode != 0]
    got = "CAUGHT" if flipped else "MISS"
    if got != expect:
        print(f"  误报 {mid}：预期 {expect}，实际 {got}（翻面 {flipped}）—— 结论不采信")
        wrong += 1
        continue
    if flipped:
        caught += 1
        print(f"  抓住 {mid}（样例翻面 红）：{name}")
        print(f"        是这几条钉住的：#{'、#'.join(flipped)}")
    else:
        miss += 1
        print(f"  漏抓 {mid}（MISS）：{name}")
        print(f"        没有一条样例翻面 —— 等价变异照实记，不替人判断它有没有意义")

print(f"\nmutate-dec：{caught} 抓住 / {miss} MISS（等价变异）/ {compile_hit} 编译期响 / "
      f"{wrong} 归因不符")
sys.exit(1 if wrong else 0)
PY
