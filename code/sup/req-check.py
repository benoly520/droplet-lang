#!/usr/bin/env python3
"""需求登记核对：源句 -> 条款 -> 目标 -> 断言/样例，链子断在哪一环就红在哪一环。

为什么不是一张 Word 需求表：需求表本身不会失败。这里要的是**可失败**的链子：
  1. 每个源句（S）至少被一条条款（R）认领；每条 R 至少引一个 S —— 双向覆盖，防漏需求；
  2. 每条 R 至少有一条真断言或真样例撑着（类型“缺口”除外，它必须有一条未闭缺口）；
  3. R 写的断言/样例/目标必须真的存在；
  4. R 写的断言集合必须等于它声明的目标所证明的集合（改了一边没改另一边 = 漂移）；
  5. 验收表里的每条断言、样例表里的每条样例、契约里的每条目标，都必须有人认领；
  6. 每条 R 在 TRANS.tsv 里恰好一行，译法/排除/反例/证据四栏非空；
  7. 反例台账：反例不是“无…”时，必须要么认领变异（S<n>）/样例（E<n>），要么在 GAP.tsv
     里登记成缺口，并且缺口编号要被这条反例点名。编号不存在、样例不属于本条款 = 红；
     GAP 状态只能 已闭/不闭/未闭，未闭 = 红（不闭 = 承认的边界，看得见但不挡路）；
  8. AMBIG.tsv 不允许“未定”；
  8. 基线核对（先红后绿）：回归条款在基线二进制上必须全绿；新增条款必须至少有一条断言在
     基线上是红的。全绿的新增条款 = 翻译抄了现状，或类型标错了。
     这一条现在是**门**不是手跑项：有“新增”条款时自动从 req/BASELINE 取基线 commit，
     交给 code/sup/baseline-sup.sh 构建“旧实现 + 当前题面”。拿不到基线 = 红。

口径：读的是编译产物的 --contract、验收脚本本体与 req/ 下的登记文件，不读实现源码。
用法：
  python3 code/sup/req-check.py [二进制] [--baseline 基线二进制|commit] [--no-baseline] [--port 18600]
  默认二进制 .build/check/blobd。--baseline 可以给现成二进制，也可以给 commit（自动构建）。
  --no-baseline 是显式逃生口：跳过先红后绿，但会在结论里写明“这一层没走完”。
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
BASE = os.path.join(ROOT, "req", "BASELINE")
SCRIPT = os.path.join(ROOT, "code", "webapi", "deploy", "accept-table.sh")
MUT = os.path.join(ROOT, "code", "sup", "mutation-score.py")

BIN = ""
BASELINE = ""
NO_BASELINE = False
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
    elif args[i] == "--no-baseline":
        NO_BASELINE = True
        i += 1
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
MUT_IDS = set(re.findall(r'id="(S\d+)"', open(MUT, encoding="utf-8").read()))
trans = {r[0]: r for r in rows_of(TRANS, 6)}
for rid, r in trans.items():
    if rid not in seen:
        die("TRANS.tsv 里有未知条款 %s" % rid)
    for label, v in zip(("原文", "译法", "排除", "反例", "证据"), r[1:]):
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
gap_by_id = {}
GAP_STATE = ("已闭", "未闭", "不闭")
for gid, rid, what, st in gaps:
    if rid not in seen:
        die("缺口 %s 引用的条款 %s 不在 REQ.tsv 里" % (gid, rid))
    gap_r.setdefault(rid, []).append((gid, what, st))
    gap_by_id[gid] = (rid, what, st)
    if st not in GAP_STATE:
        die("缺口 %s 的状态 %r 只能是 已闭/未闭/不闭" % (gid, st))
    if st == "未闭":
        die("缺口 %s（%s）未闭：%s" % (gid, rid, what))
for rid, text, ss, typ, gs, as_, es in rows:
    if typ == "缺口" and rid not in gap_r:
        die("%s 标成缺口，但 GAP.tsv 里没有登记" % rid)

# ---- 反例台账：TRANS 的每一条反例，要么有人管，要么登记成缺口 ----
# 静态能查的部分：编号存在、样例属于本条款、反例写“无”就不许认领证据、
# 有缺口行就必须被那条反例点名（否则缺口是别人笔记里的缺口，不是这条需求的）。
# 实测部分（这条变异真的弄红了本条款声明的某条断言）在 mutation-score.py 里查。
claimed_mut = set()
unclaimed_mut = []
for rid, text, ss, typ, gs, as_, es in rows:
    as_, es = cells(as_), cells(es)
    r = trans.get(rid)
    if not r:
        continue
    ex, ev = r[4], r[5]
    ids = cells(ev)
    claimed_mut.update(x for x in ids if x.startswith("S"))
    for gid, what, st in gap_r.get(rid, []):
        if gid not in ex:
            die("%s 在 GAP.tsv 里有 %s（%s），但 TRANS 的反例没点名它 —— 缺口得挂在那条反例上才看得见"
                % (rid, gid, st))
    if ex.startswith("无"):
        if ids:
            die("%s 的反例写“无”，证据列却认领了 %s —— 要么反例没写全，要么这些编号是孤儿"
                % (rid, ",".join(ids)))
        continue
    if typ == "缺口":
        continue
    if not ids and rid not in gap_r:
        die("%s 写了反例（%s），既没认领变异/样例，也没登记缺口 —— 这条反例只是句漂亮话"
            % (rid, ex[:24] + "…" if len(ex) > 24 else ex))
    for x in ids:
        if x.startswith("S"):
            if x not in MUT_IDS:
                die("%s 的证据认领了 %s，但 %s 里没有这条变异"
                    % (rid, x, os.path.relpath(MUT, ROOT)))
        elif x.startswith("E"):
            n = x[1:]
            if n not in have_e:
                die("%s 的证据认领了 %s，但 DP_EXAMPLES 里没有这个样例" % (rid, x))
            if n not in es:
                die("%s 的证据认领了样例 %s，可它自己没在“样例”栏声明它" % (rid, x))
        else:
            die("%s 的证据列里的 %r 既不是 S<n> 也不是 E<n>" % (rid, x))
for x in sorted(MUT_IDS - claimed_mut):
    unclaimed_mut.append(x)

# ---- 基线核对：先红后绿 ----
base_ok = False
n_new = sum(1 for r in rows if r[3] == "新增")
BASE_NOTE = ""
if BASELINE and NO_BASELINE:
    die("--baseline 和 --no-baseline 不能同时给：要么验先红后绿，要么显式不验")
elif NO_BASELINE and n_new:
    BASE_NOTE = "被 --no-baseline 跳过（快速回路不启服务；sh code/sup/check.sh --full 会跑它）"
if not BASELINE and not NO_BASELINE and n_new and not errors:
    # 有“新增”条款就得验先红后绿，这一步不再靠人记得手跑。
    # 基线从 req/BASELINE 取（freeze.sh --update 在换题那一刻钉的）。
    spec = ""
    if os.path.isfile(BASE):
        for line in open(BASE, encoding="utf-8"):
            if line.strip() and not line.lstrip().startswith("#"):
                spec = line.strip()
                break
    if not spec:
        die("有 %d 条“新增”条款，但 req/BASELINE 里没有基线 commit —— "
            "换题时 sh code/sup/freeze.sh --update 会钉它；确实不验就显式加 --no-baseline" % n_new)
    else:
        BASELINE = spec
        BASE_NOTE = "（自动取自 req/BASELINE）"

if BASELINE and not errors and not NO_BASELINE:
    if os.path.isfile(BASELINE):
        bbin, bsrc = os.path.abspath(BASELINE), "手给的二进制"
    else:
        r = subprocess.run(["sh", os.path.join(ROOT, "code", "sup", "baseline-sup.sh"),
                            BASELINE, "reqbase"], cwd=ROOT, capture_output=True, text=True)
        out = [x.strip() for x in (r.stdout or "").splitlines() if x.strip()]
        bbin = out[-1] if out else ""
        bsrc = "基线 %s：旧实现 + 当前题面" % BASELINE[:8]
        if r.returncode != 0 or not bbin or not os.path.isfile(bbin):
            die("基线不可得（%s）：先红后绿这一层没跑成，不算走完。%s"
                % (bsrc, ((r.stdout + r.stderr).strip().splitlines() or ["无输出"])[-1]))
            bbin = ""
    if bbin:
        print("基线来源：%s" % bsrc)
        logs = os.path.join(ROOT, ".build", "req-check")
        os.makedirs(logs, exist_ok=True)
        ora = subprocess.run(["sh", os.path.join(ROOT, "code", "sup", "oracle-sup.sh"),
                              "reqcheck-oracle"], cwd=ROOT, capture_output=True, text=True)
        open(os.path.join(logs, "oracle.log"), "w", encoding="utf-8").write(ora.stdout + ora.stderr)
        acc = subprocess.run(["sh", os.path.join(ROOT, "code", "sup", "accept-sup.sh"),
                              bbin, str(PORT), "reqbase"],
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
                    die("%s 是新增条款，但基线上 %s 全是绿的 —— 三种原因之一："
                        "翻译抄了现状；写成了同义反复；或换题时没把上一轮的“新增”改成“回归”"
                        "（基线已经包含这一版实现，它就再也红不起来了）"
                        % (rid, ",".join("#" + a for a in as_)))
        base_ok = True
        print("基线核对（先红后绿）：回归 %d 条全绿；新增 %d 条在基线上为红" % (reg, new))

# ---- 输出 ----
print("需求登记：%d 个源句 / %d 条条款 / %d 条断言 / %d 条样例 / %d 条目标（未证目标 %d）"
      % (len(srcs), len(rows), len(have_a), len(have_e), len(goals), len(unproven_goals)))
kinds = {t: sum(1 for r in rows if r[3] == t) for t in TYPES}
amb_open = sum(1 for a in ambig if a[6] != "已定")
gap_open = sum(1 for g in gaps if g[3] == "未闭")
gap_vis = sum(1 for g in gaps if g[3] == "不闭")
gap_done = sum(1 for g in gaps if g[3].startswith("已闭"))
print("  类型：回归 %d / 新增 %d / 缺口 %d；歧义 %d（未定 %d）；缺口登记 %d（未闭 %d / 不闭 %d / 已闭 %d）"
      % (kinds["回归"], kinds["新增"], kinds["缺口"], len(ambig), amb_open,
         len(gaps), gap_open, gap_vis, gap_done))
for rid, text, ss, typ, gs, as_, es in rows:
    ss, gs, as_, es = cells(ss), cells(gs), cells(as_), cells(es)
    ev = []
    if as_:
        ev.append("断言 " + ",".join("#" + a for a in as_))
    if es:
        ev.append("样例 " + ",".join("#" + e for e in es))
    if trans.get(rid) and cells(trans[rid][5]):
        ev.append("反例归 " + ",".join(cells(trans[rid][5])))
    for g in gap_r.get(rid, []):
        ev.append("缺口 %s（%s）：%s" % (g[0], g[2], g[1]))
    print("  %-4s %s  [%s %s · %s]" % (rid, text, "/".join(ss), typ, "；".join(ev)))
if unproven_goals:
    print("  未证目标（DP_GOALS 里 proven 为空，显式承认没被证明）：" + ", ".join(unproven_goals))
if unclaimed_mut:
    print("  提示：%s 没被任何条款的反例认领 —— 覆盖分照样算它们，但它们不代表任何一条写下来的"
          "语义差异（写得出反例的话，请补进 TRANS 证据列）" % ",".join(unclaimed_mut))
if base_ok:
    print("  基线核对：已跑%s（回归在基线上全绿、新增在基线上至少一条红）" % BASE_NOTE)
elif kinds["新增"]:
    print("  基线核对：没跑成 —— 有 %d 条“新增”条款没验过“先红后绿”%s。"
          "这一层的结论缺一段：要么 req/BASELINE 里有 commit（自动构建旧实现），"
          "要么手给 --baseline <二进制|commit>；--no-baseline 是逃生口，不是常态"
          % (kinds["新增"], ("：本轮" + BASE_NOTE) if BASE_NOTE else ""))
else:
    print("  基线核对：本轮没有“新增”条款，先红后绿不适用（题面没换，基线全绿由下一次换题时验）")
for e in errors:
    print("  FAIL " + e, file=sys.stderr)
sys.exit(1 if errors else 0)
