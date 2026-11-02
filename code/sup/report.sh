#!/bin/sh
# 人类验收页：一屏看完“需求条款有没有证据”，不用读实现。
#
# 为什么按条款组织：这个工程的承诺是"人只看结果"。结果不是几十个数字，而是
# **每一条需求由哪几条断言/样例证明、这一版是不是绿的**。断言编号是给机器的，
# 需求条款是给人的；中间那层（DP_GOALS）已经由 req-check 对过账。
#
# 口径：
#   * 条款、断言编号、样例编号全部来自 req/REQ.tsv；
#   * 样例红绿来自 oracle-sup.sh 的实际运行，断言红绿来自 accept-table.sh 的实际运行
#     （--accept 时）；没跑就写"未跑"，绝不用"上次是绿的"代替；
#   * 题面冻结状态由 freeze.sh --check 现场核对；
#   * 未证目标（DP_GOALS 里 proven 为空）单独计数 —— 沉默不算决定。
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
BIN=$(CDPATH= cd -- "$(dirname -- "$BIN")" && pwd)/$(basename "$BIN")

TMP=$ROOT/.build/report
rm -rf "$TMP"; mkdir -p "$TMP"
"$BIN" --contract > "$TMP/contract.json"

# 样例：19 条封闭样例，一条一个进程；这里要的是逐条红绿。
ORA_LOG=$TMP/oracle.log
if sh code/sup/oracle-sup.sh report-oracle >"$ORA_LOG" 2>&1; then
  ORA_OK=1
else
  ORA_OK=0
fi

ACC=""
if [ -n "$PORT" ]; then
  LOG=$TMP/accept.log
  if sh code/sup/accept-sup.sh "$BIN" "$PORT" report >"$LOG" 2>&1; then
    ACC=green
  else
    ACC=red
  fi
  echo "（验收：$(grep -E '[0-9]+ 过 / [0-9]+ 挂' "$LOG" | tail -n 1)）"
fi

FREEZE_OK=0
if sh code/sup/freeze.sh --check "$BIN" >"$TMP/freeze.log" 2>&1; then
  FREEZE_OK=1
fi
REQ_OK=0
if python3 code/sup/req-check.py "$BIN" >"$TMP/req.log" 2>&1; then
  REQ_OK=1
fi

python3 - "$TMP/contract.json" "$ACC" "$TMP/accept.log" "$ORA_LOG" \
          "$FREEZE_OK" "$REQ_OK" "$TMP/freeze.log" "$TMP/req.log" "$ORA_OK" <<'PY'
import json, os, re, sys

contract = json.load(open(sys.argv[1], encoding="utf-8"))
acc = sys.argv[2]
accept_log, oracle_log = sys.argv[3], sys.argv[4]
freeze_ok, req_ok = sys.argv[5] == "1", sys.argv[6] == "1"
freeze_log, req_log = sys.argv[7], sys.argv[8]
ora_ok = sys.argv[9] == "1"


def statuses(path):
    st = {}
    try:
        with open(path, encoding="utf-8") as f:
            for line in f:
                m = re.match(r"\s+(ok|FAIL)\s+#(\d+)", line)
                if m:
                    st[int(m.group(2))] = (m.group(1) == "ok")
    except OSError:
        pass
    return st


a_st = statuses(accept_log) if acc else {}
e_st = statuses(oracle_log)

def width(s):
    return sum(2 if ord(c) > 127 else 1 for c in s)

def pad(s, n):
    return s + " " * max(0, n - width(s))

def cut(s, n):
    out = ""
    for ch in s:
        if width(out + ch) > n - 2:
            return out + "…"
        out += ch
    return out

reqs = []
root = os.getcwd()  # report.sh 已经 cd 到仓库根


def tsv(path, ncol):
    out = []
    p = os.path.join(root, "req", path)
    if not os.path.exists(p):
        return out
    for line in open(p, encoding="utf-8"):
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        f = line.rstrip("\n").split("\t")
        if len(f) != ncol:
            continue
        out.append([x.strip() for x in f])
    return out


def ids(s):
    if s in ("", "-"):
        return []
    return [x for x in s.split(",") if x]


def run(xs):
    """1,2,3,7 -> 1–3,7：证据列太长会被切掉，连续编号压成区间。"""
    xs = sorted(int(x) for x in xs)
    out, i = [], 0
    while i < len(xs):
        j = i
        while j + 1 < len(xs) and xs[j + 1] == xs[j] + 1:
            j += 1
        out.append(str(xs[i]) if i == j else
                   ("%d–%d" % (xs[i], xs[j]) if j - i >= 2 else "%d,%d" % (xs[i], xs[j])))
        i = j + 1
    return ",".join(out)


reqs = [(r[0], r[1], ids(r[2]), r[3], ids(r[4]), ids(r[5]), ids(r[6]))
        for r in tsv("REQ.tsv", 7)]
nsrc = len(re.findall(r"^S\d+\s", open(os.path.join(root, "req", "SRC.md"),
                                       encoding="utf-8").read(), re.M))
trans = tsv("TRANS.tsv", 6)
ambig = tsv("AMBIG.tsv", 7)
gaps = tsv("GAP.tsv", 4)
unresolved = [a[0] for a in ambig if a[6] != "已定"]
open_gaps = [g[0] for g in gaps if g[3] == "未闭"]
wide_gaps = [g for g in gaps if g[3] == "不闭"]
done_gaps = [g[0] for g in gaps if g[3].startswith("已闭")]
gap_by_rid = {}
for g in gaps:
    gap_by_rid.setdefault(g[1], []).append(g)
kinds = {}
for r in reqs:
    kinds[r[3]] = kinds.get(r[3], 0) + 1

goals = contract["goals"]
routes = contract["routes"]
examples = contract["examples"]
envs = contract["envs"]
unproven = [g for g in goals if not g["proven"]]

rows = []
bad_req = 0
unrun_req = 0
for rid, text, ss, typ, gs, as_, es in reqs:
    bad = any(a_st.get(int(a)) is False for a in as_) or \
          any(e_st.get(int(e)) is False for e in es)
    unknown = (bool(as_) and not acc) or \
              any(int(a) not in a_st for a in as_) or \
              any(int(e) not in e_st for e in es)
    if bad:
        res, bad_req = "红", bad_req + 1
    elif unknown:
        res, unrun_req = "未跑", unrun_req + 1
    else:
        res = "绿"
    ev = []
    if as_:
        ev.append("断言 " + run(as_))
    if es:
        ev.append("样例 " + run(es))
    if not ev:
        ev.append("（缺口：无证据）")
    for g in gap_by_rid.get(rid, []):
        ev.append("缺口 %s %s" % (g[0], g[3]))
    rows.append((rid, "/".join(ss), typ, text, "；".join(ev), res))

print()
print("Droplet 验收报告")
print("  介质：合法 C11 + 4 张表   |   路由 %d   |   配置 %d   |   样例 %d   |   目标 %d（未证 %d）   |   条款 %d"
      % (len(routes), len(envs), len(examples), len(goals), len(unproven), len(reqs)))
print()
print("  " + pad("条款", 6) + pad("源句", 7) + pad("类型", 5) + pad("需求", 40) + pad("证据", 35) + "结果")
print("  " + "-" * 93)
for rid, ss, typ, text, ev, res in rows:
    print("  " + pad(rid, 6) + pad(ss, 7) + pad(typ, 5)
          + pad(cut(text, 38), 40) + pad(cut(ev, 34), 35) + res)
print()
print("  路由：" + " / ".join(f"{r['method']} {r['path']}" for r in routes))
print("  样例：%d 条封闭样例覆盖纯决定；读写、轮转与 wire 语义由断言兜着" % len(examples))
print("  译稿：源句 %d → 条款 %d（回归 %d / 新增 %d / 缺口 %d），逐条语义对账 %d 份"
      % (nsrc, len(reqs), kinds.get("回归", 0), kinds.get("新增", 0),
         kinds.get("缺口", 0), len(trans)))
n_ev = sum(1 for t in trans if len(t) == 6 and t[5] not in ("", "-"))
# "-" 有两种，别混：反例写“无…”（译法与原文同宽，本来就没有反例可认领），
# 和写了真反例但眼下没人管（这种必须由 GAP 行挂着，req-check 会查）。
n_same = sum(1 for t in trans if len(t) == 6 and t[5] == "-" and t[4].startswith("无"))
n_gaponly = sum(1 for t in trans if len(t) == 6 and t[5] == "-" and not t[4].startswith("无"))
print("  反例台账：%d 条里 %d 条认领了判据（S=实现变异 / E=样例）；"
      "%d 条写“-”：其中 %d 条是“译法与原文同宽”，%d 条没有判据、靠 GAP 登记挂在验收页上"
      % (len(trans), n_ev, len(trans) - n_ev, n_same, n_gaponly))
print("  歧义 %d（未定 %d）；缺口登记 %d（未闭 %d / 不闭 %d / 已闭 %d）"
      % (len(ambig), len(unresolved), len(gaps),
         len(open_gaps), len(wide_gaps), len(done_gaps)))
if wide_gaps:
    print("  不闭的边界（承认验不到那一步，挂着让人看）："
          + "；".join("%s %s：%s" % (g[0], g[1], cut(g[2], 30)) for g in wide_gaps))
if done_gaps:
    print("  已闭的缺口：" + ",".join(done_gaps) + "（补了判据，闭合方式写在那一行的文本里）")
if unresolved or open_gaps:
    print("  译稿是红的：未定歧义 " + (",".join(unresolved) or "-")
          + "，未闭缺口 " + (",".join(open_gaps) or "-"))
if unproven:
    print("  未证目标（DP_GOALS.proven 为空）：" + ", ".join(g["id"] for g in unproven))
else:
    print("  未证目标：0 条（每条目标都有断言或样例认领）")

print("  题面冻结：" + ("一致（题没被改过）" if freeze_ok else "红 —— 题面与 req/FROZEN 不一致"))
print("  登记链：  " + ("一致（条款 ↔ 目标 ↔ 断言/样例）" if req_ok else "红 —— 见 python3 code/sup/req-check.py"))

if not ora_ok:
    print("  样例 oracle：红 —— 样例没跑全，凡是有样例的条款都不能算绿。")

if not acc:
    print()
    print("  断言结果未附：加 --accept <端口> 跑一次验收表，这一栏才有红绿。")
    print("  本页现在只证明“条款都有证据”，不证明服务这一版是好的。")
else:
    ok_a = sum(1 for v in a_st.values() if v)
    bad_a = sum(1 for v in a_st.values() if not v)
    ok_e = sum(1 for v in e_st.values() if v)
    bad_e = sum(1 for v in e_st.values() if not v)
    print()
    print("  结论：条款 %d 绿 / %d 红 / %d 未跑；断言 %d 绿 / %d 红；样例 %d 绿 / %d 红（接的是这一版二进制）"
          % (len(reqs) - bad_req - unrun_req, bad_req, unrun_req, ok_a, bad_a, ok_e, bad_e))
    if not req_ok:
        print("  注意：登记链是红的 —— 上面的条款映射本身不可信，先修 req-check。")
    if not freeze_ok:
        print("  注意：题面冻结是红的 —— 这一版验的题与冻结时不是同一道题。")

fail = (not freeze_ok) or (not req_ok) or (not ora_ok) or bad_req > 0 or \
       any(v is False for v in e_st.values()) or \
       (bool(acc) and any(v is False for v in a_st.values()))
sys.exit(1 if fail else 0)
PY
