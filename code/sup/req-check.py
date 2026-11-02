#!/usr/bin/env python3
"""需求登记核对：源句 -> 条款 -> 目标 -> 断言/样例，链子断在哪一环就红在哪一环。

为什么不是一张 Word 需求表：需求表本身不会失败。这里要的是**可失败**的链子：
  1. 每个源句（S）至少被一条条款（R）认领；每条 R 至少引一个 S —— 双向覆盖，防漏需求；
  2. 每条 R 至少有一条真断言或真样例撑着（类型“缺口”除外，它必须有一条未闭缺口）；
  3. R 写的断言/样例/目标必须真的存在；
  4. R 写的断言集合必须等于它声明的目标所证明的集合（改了一边没改另一边 = 漂移）；
  5. 验收表里的每条断言、样例表里的每条样例、契约里的每条目标，都必须有人认领；
  6. 每条 R 在 TRANS.tsv 里恰好一行，译法/排除/反例三栏非空；
  7. AMBIG.tsv 不允许“未定”；GAP.tsv 不允许“未闭”；
  8. --baseline：回归条款在基线二进制上必须全绿；新增条款必须至少有一条断言在基线上是红的
     （先红后绿）。全绿的新增条款 = 翻译抄了现状，或类型标错了。

口径：读的是编译产物的 --contract、验收脚本本体与 req/ 下的登记文件，不读实现源码。
用法：
  python3 code/sup/req-check.py [二进制] [--baseline 基线二进制] [--port 18600]
  默认二进制 .build/check/blobd；--baseline 需要一份“实现改动前”的二进制。
"""
import json
import os
import re
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(HERE))
REQ = os.path.join(ROOT, "req", "REQ.tsv")
SRC = os.path.join(ROOT, "req", "SRC.md")
TRANS = os.path.join(ROOT, "req", "TRANS.tsv")
AMBIG = os.path.join(ROOT, "req", "AMBIG.tsv")
GAP = os.path.join(ROOT, "req", "GAP.tsv")
SCRIPT = os.path.join(ROOT, "code", "webapi", "deploy", "accept-table.sh")

BIN = ""
BASELINE = ""
PORT = 18600
args = sys.argv[1:]
i = 0
while i < len(args):
    if args[i] == "--baseline":
        BASELINE = args[i + 1]
        i += 2
    elif args[i] == "--port":
        PORT = int(args[i + 1])
        i += 2
    else:
        BIN = args[i]
        i += 1
if not BIN:
    BIN = os.path.join(ROOT, ".build", "check", "blobd")
if not os.path.isfile(BIN):
    print("req-check: 找不到二进制 %s（先跑 sh code/sup/build.sh check）" % BIN, file=sys.stderr)
    sys.exit(2)

raw = subprocess.run([BIN, "--contract"], capture_output=True, text=True)
if raw.returncode != 0:
    print("req-check: --contract 失败：%s" % raw.stderr.strip(), file=sys.stderr)
    sys.exit(2)
try:
    doc = json.loads(raw.stdout)
except json.JSONDecodeError as e:
    print("req-check: --contract 输出的不是 JSON：%s" % e, file=sys.stderr)
    sys.exit(2)

have_a = set(re.findall(r"^ck (\d+) ", open(SCRIPT, encoding="utf-8").read(), re.M))
goals = {g["id"]: [str(x) for x in (g.get("proven") or [])] for g in doc["goals"]}
have_e = {str(e["id"]) for e in doc["examples"]}
unproven_goals = sorted(k for k, v in goals.items() if not v)

errors = []


def die(msg):
    errors.append(msg)


def rows_of(path, ncol):
    out = []
    for lineno, line in enumerate(open(path, encoding="utf-8"), 1):
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        f = line.rstrip("\n").split("\t")
        if len(f) != ncol:
            die("%s 第 %d 行有 %d 列，要求 %d 列"
                % (os.path.relpath(path, ROOT), lineno, len(f), ncol))
            continue
        out.append([x.strip() for x in f])
    return out


def cells(s):
    if s in ("", "-"):
        return []
    return [x.strip() for x in s.split(",") if x.strip()]


# ---- 源句 ----
srcs = {}
for line in open(SRC, encoding="utf-8"):
    m = re.match(r"^S(\d+)\s+(.+?)\s*$", line)
    if not m:
        if re.match(r"^S\d", line):
            die("SRC.md 有一行像源句但格式不对：%r" % line.strip())
        continue
    sid, text = "S" + m.group(1), m.group(2)
    if sid in srcs:
        die("源句编号重复：%s" % sid)
    srcs[sid] = text
if not srcs:
    die("SRC.md 里一条源句都没有")

# ---- 条款 ----
rows = rows_of(REQ, 7)
if not rows:
    die("REQ.tsv 里一条条款都没有")

TYPES = {"回归", "新增", "缺口"}
claimed_s, claimed_a, claimed_e, claimed_g = set(), set(), set(), set()
seen = set()
for rid, text, ss, typ, gs, as_, es in rows:
    ss, gs, as_, es = cells(ss), cells(gs), cells(as_), cells(es)
    if not re.fullmatch(r"R\d+", rid):
        die("条款编号不像 R<数字>：%r" % rid)
    if rid in seen:
        die("条款编号重复：%s" % rid)
    seen.add(rid)
    if not text:
        die("%s 没有条款文字" % rid)
    if typ not in TYPES:
        die("%s 的类型 %r 不在 %s 里" % (rid, typ, sorted(TYPES)))
    if not ss:
        die("%s 没有引用任何源句 —— 凭空造出来的需求" % rid)
    for s in ss:
        if s not in srcs:
            die("%s 引用的源句 %s 不在 SRC.md 里" % (rid, s))
        claimed_s.add(s)
    if typ == "缺口":
        if as_ or es:
            die("%s 标成缺口，却又写了断言/样例 —— 改类型或删证据" % rid)
    else:
        if not as_ and not es:
            die("%s 没有任何断言或样例 —— 只有文字，不算被证明" % rid)
    for a in as_:
        if a not in have_a:
            die("%s 引用的断言 #%s 不在 %s 里" % (rid, a, os.path.relpath(SCRIPT, ROOT)))
        claimed_a.add(a)
    for e in es:
        if e not in have_e:
            die("%s 引用的样例 #%s 不在 DP_EXAMPLES 里" % (rid, e))
        claimed_e.add(e)
    want_a = set()
    for g in gs:
        if g not in goals:
            die("%s 引用的目标 %s 不在 DP_GOALS 里" % (rid, g))
            continue
        claimed_g.add(g)
        want_a |= set(goals[g])
    if gs and want_a != set(as_):
        die("%s 的断言 %s 与目标声明的 %s 不一致（改了一边没改另一边）"
            % (rid, sorted(as_), sorted(want_a)))

for s in sorted(srcs, key=lambda x: int(x[1:])):
    if s not in claimed_s:
        die("源句 %s 没有被任何条款认领（漏需求）" % s)
for a in sorted(have_a - claimed_a, key=int):
    die("验收断言 #%s 没有被任何条款认领（孤儿断言）" % a)
for e in sorted(have_e - claimed_e, key=int):
    die("样例 #%s 没有被任何条款认领（孤儿样例）" % e)
for g in sorted(set(goals) - claimed_g):
    die("目标 %s 没有被任何条款认领（孤儿目标）" % g)

# ---- 语义差异单 ----
trans = {r[0]: r for r in rows_of(TRANS, 5)}
for rid, r in trans.items():
    if rid not in seen:
        die("TRANS.tsv 里有未知条款 %s" % rid)
    for label, v in zip(("原文", "译法", "排除", "反例"), r[1:]):
        if not v:
            die("%s 的 TRANS 行缺“%s”" % (rid, label))
for rid in seen:
    if rid not in trans:
        die("%s 在 TRANS.tsv 里没有译法记录" % rid)

# ---- 歧义台账 ----
ambig = rows_of(AMBIG, 7)
amb_ids = set()
for aid, s, a, b, chosen, ev, st in ambig:
    if aid in amb_ids:
        die("歧义编号重复：%s" % aid)
    amb_ids.add(aid)
    if s not in srcs:
        die("歧义 %s 引用的源句 %s 不在 SRC.md 里" % (aid, s))
    if st not in ("已定", "未定"):
        die("歧义 %s 的状态 %r 只能是已定/未定" % (aid, st))
    if st == "未定":
        die("歧义 %s（%s）还没有定论：%s / %s" % (aid, s, a, b))
    for label, v in zip(("读法A", "读法B", "选择", "判定证据"), (a, b, chosen, ev)):
        if not v:
            die("歧义 %s 缺“%s”" % (aid, label))

# ---- 机制缺口 ----
gaps = rows_of(GAP, 4)
gap_r = {}
for gid, rid, what, st in gaps:
    if rid not in seen:
        die("缺口 %s 引用的条款 %s 不在 REQ.tsv 里" % (gid, rid))
    gap_r[rid] = (gid, what, st)
    if st not in ("已闭", "未闭"):
        die("缺口 %s 的状态 %r 只能是已闭/未闭" % (gid, st))
    if st == "未闭":
        die("缺口 %s（%s）未闭：%s" % (gid, rid, what))
for rid, text, ss, typ, gs, as_, es in rows:
    if typ == "缺口" and rid not in gap_r:
        die("%s 标成缺口，但 GAP.tsv 里没有登记" % rid)
    if typ != "缺口" and rid in gap_r:
        die("%s 不是缺口类型，却在 GAP.tsv 里有登记" % rid)

# ---- 基线核对：先红后绿 ----
base_ok = False
if BASELINE and not errors:
    if not os.path.isfile(BASELINE):
        die("找不到基线二进制 %s" % BASELINE)
    else:
        logs = os.path.join(ROOT, ".build", "req-check")
        os.makedirs(logs, exist_ok=True)
        ora = subprocess.run(["sh", os.path.join(ROOT, "code", "sup", "oracle-sup.sh"),
                              "reqcheck-oracle"], cwd=ROOT, capture_output=True, text=True)
        open(os.path.join(logs, "oracle.log"), "w", encoding="utf-8").write(ora.stdout + ora.stderr)
        acc = subprocess.run(["sh", os.path.join(ROOT, "code", "sup", "accept-sup.sh"),
                              os.path.abspath(BASELINE), str(PORT), "reqbase"],
                             cwd=ROOT, capture_output=True, text=True)
        open(os.path.join(logs, "accept.log"), "w", encoding="utf-8").write(acc.stdout + acc.stderr)

        def statuses(text):
            st = {}
            for line in text.splitlines():
                m = re.match(r"\s+(ok|FAIL)\s+#(\d+)", line)
                if m:
                    st[int(m.group(2))] = (m.group(1) == "ok")
            return st

        a_st = statuses(acc.stdout + acc.stderr)
        e_st = statuses(ora.stdout + ora.stderr)
        reg = new = 0
        for rid, text, ss, typ, gs, as_, es in rows:
            as_, es = cells(as_), cells(es)
            if typ == "回归":
                reg += 1
                red = [("#" + a) for a in as_ if a_st.get(int(a)) is not True]
                red += [("#" + e) for e in es if e_st.get(int(e)) is not True]
                if red:
                    die("%s 是回归条款，但基线上 %s 不是绿的 —— 类型标错，或基线就不是“现状”"
                        % (rid, ",".join(red)))
            elif typ == "新增":
                new += 1
                if not as_:
                    die("%s 是新增条款，但没有 wire 断言：先红后绿无法判定（样例不算基线证据）" % rid)
                    continue
                red = [("#" + a) for a in as_ if a_st.get(int(a)) is False]
                if not red:
                    die("%s 是新增条款，但基线上 %s 全是绿的 —— 翻译抄了现状，或写成了同义反复"
                        % (rid, ",".join("#" + a for a in as_)))
        base_ok = True
        print("基线核对（先红后绿）：回归 %d 条全绿；新增 %d 条在基线上为红" % (reg, new))

# ---- 输出 ----
print("需求登记：%d 个源句 / %d 条条款 / %d 条断言 / %d 条样例 / %d 条目标（未证目标 %d）"
      % (len(srcs), len(rows), len(have_a), len(have_e), len(goals), len(unproven_goals)))
kinds = {t: sum(1 for r in rows if r[3] == t) for t in TYPES}
amb_open = sum(1 for a in ambig if a[6] != "已定")
gap_open = sum(1 for g in gaps if g[3] != "已闭")
print("  类型：回归 %d / 新增 %d / 缺口 %d；歧义 %d（未定 %d）；缺口登记 %d（未闭 %d）"
      % (kinds["回归"], kinds["新增"], kinds["缺口"], len(ambig), amb_open,
         len(gaps), gap_open))
for rid, text, ss, typ, gs, as_, es in rows:
    ss, gs, as_, es = cells(ss), cells(gs), cells(as_), cells(es)
    ev = []
    if as_:
        ev.append("断言 " + ",".join("#" + a for a in as_))
    if es:
        ev.append("样例 " + ",".join("#" + e for e in es))
    if typ == "缺口":
        g = gap_r.get(rid)
        ev.append("缺口 " + g[0] + "：" + g[1] if g else "缺口：GAP.tsv 里没登记")
    print("  %-4s %s  [%s %s · %s]" % (rid, text, "/".join(ss), typ, "；".join(ev)))
if unproven_goals:
    print("  未证目标（DP_GOALS 里 proven 为空，显式承认没被证明）：" + ", ".join(unproven_goals))
if BASELINE:
    print("  基线核对：已跑（回归在基线上全绿、新增在基线上至少一条红）")
elif kinds["新增"]:
    print("  基线核对：未跑 —— 有 %d 条“新增”条款没验过“先红后绿”。"
          "加 --baseline <改动前的二进制> 才算走完这一层" % kinds["新增"])
for e in errors:
    print("  FAIL " + e, file=sys.stderr)
sys.exit(1 if errors else 0)
