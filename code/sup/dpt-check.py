#!/usr/bin/env python3
# dpt-check.py —— Droplet 登记语言的检查器。
#
# 这个文件与 lang/droplet.dpt 是同一份口径的两半：定义里的错误码表列出哪些错会被抓，
# 这里的 CODES 列出实现真的会吐哪些码。`--self-check` 双向对账：
# 定义多一条 = 文档骗人；实现多一条 = 有没写进定义的失败模式。两种都红。
#
# 用法：
#   python3 code/sup/dpt-check.py --self-check [--spec 定义文件]   # 定义 <-> 实现对账
#   python3 code/sup/dpt-check.py <登记.dpt> [--root 仓库根] [--binary 二进制]
#   python3 code/sup/dpt-check.py lang/blobd.dpt            # 用工程清单（验收表/契约/变异表）判
#   python3 code/sup/dpt-check.py x.dpt --inventory inv.json # 用外部清单判（可脱离本工程）
#   python3 code/sup/dpt-check.py lang/blobd.dpt --against req/ # 与五份 TSV 逐栏对账
#   python3 code/sup/dpt-check.py --emit req/               # 从 TSV 机械转写成 .dpt
#
# 退出码：0 = 通过；1 = 有 E 码；2 = 用法/环境错。
import json
import os
import re
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

# 实现会吐的全部码。加一条就必须往定义的码表里加一行，否则 --self-check 红。
CODES = {
    "E01": "未知标识符",
    "E02": "缺必填栏，或栏出现两次",
    "E03": "恰好一个 service 之外的顶层声明",
    "E04": "ID 形状或重复不对",
    "E05": "引用了清单里不存在的编号",
    "E06": "引用了不存在的 src 或 promise ID",
    "E07": "src 没被任何 promise 认领",
    "E08": "status 不是三个值之一",
    "E09": "非 gap 的 promise 既无 wire 也无 unit（只有文字 = 未证）",
    "E10": "goal 的 proven 与本条 wire 不等",
    "E11": "evidence 的 S 变异打不中本条任何 wire",
    "E12": "evidence 的 @ 样例没被本条声明",
    "E13": "evidence=none 与 trap 不匹配",
    "E14": "reads/bounds/trap 为空",
    "E15": "choice 的选定为空（未定的歧义）",
    "E16": "hole 状态非法、为 open、或没写缺什么",
    "E17": "hole 的 ID 没出现在其 promise 的 trap 里",
    "E18": "清单里的断言、样例或目标没人认领（孤儿）",
    "E19": "status=new 且本次要求当场判先红（--strict）",
    "E20": "括号不平衡",
}

FIELDS = {"from", "status", "goal", "wire", "unit", "reads", "bounds", "trap", "evidence"}
TOPS = {"service", "src", "promise", "choice", "hole"}
STATUSES = {"regress", "new", "gap"}
HOLES = {"open", "waived", "closed"}


class Fail(Exception):
    def __init__(self, code, where, msg):
        self.code, self.where, self.msg = code, where, msg

    def __str__(self):
        return "%s %s：%s" % (self.code, self.where, self.msg)


def die(code, where, msg):
    raise Fail(code, where, msg)


# ---------- 词法 ----------
TOK = re.compile(r'\s*("(?:[^"\\]|\\.)*"|[A-Za-z_][A-Za-z0-9_]*|@?[0-9]+|.)')


def lex(text):
    out, i, line = [], 0, 1
    while i < len(text):
        if text.startswith("//", i):
            j = text.find("\n", i)
            i = len(text) if j < 0 else j
            continue
        if text[i] == "\n":
            line += 1
            i += 1
            continue
        if text[i].isspace():
            i += 1
            continue
        m = TOK.match(text, i)
        if not m:
            die("E01", "line %d" % line, "读不下去的字符 %r" % text[i])
        t = m.group(1)
        out.append((t, line))
        i = m.end(1)
    out.append(("", 10 ** 9))
    return out


class P:
    def __init__(self, toks):
        self.t, self.i = toks, 0

    def peek(self):
        return self.t[self.i][0]

    def line(self):
        return self.t[self.i][1]

    def next(self):
        v = self.t[self.i]
        self.i += 1
        return v[0]

    def eat(self, s):
        if self.peek() != s:
            die("E20", "line %d" % self.line(), "这里要 %r，实际是 %r" % (s, self.peek() or "文件尾"))
        self.i += 1

    def eof(self):
        return self.peek() == ""


def unq(s):
    if len(s) >= 2 and s[0] == '"' and s[-1] == '"':
        return s[1:-1].replace('\\"', '"').replace("\\n", "\n")
    return s


def esc(s):
    return s.replace('"', '\\"').replace("\n", "\\n")


# ---------- 语法 ----------
def parse(text):
    p = P(lex(text))
    ids = {"src": {}, "promise": {}, "choice": {}, "hole": {}}
    if p.peek() != "service":
        die("E03", "line %d" % p.line(), "第一句必须是 service")
    p.next()
    svc = p.next()
    if not re.fullmatch(r"[a-z][a-z0-9_]*", svc):
        die("E04", "service", "ID 要小写标识符，实际 %r" % svc)
    p.eat("{")
    while p.peek() != "}":
        if p.eof():
            die("E20", "文件尾", "service 没闭合")
        kind = p.next()
        if kind not in ("src", "promise", "choice", "hole"):
            die("E01", "line %d" % p.line(), "顶层只有 src/promise/choice/hole，实际是 %r" % kind)
        rid = p.next()
        shape = {"src": r"S\d+", "promise": r"R\d+", "choice": r"A\d+", "hole": r"G\d+"}[kind]
        if not re.fullmatch(shape, rid):
            die("E04", kind, "ID %r 不符合 %s" % (rid, shape))
        if rid in ids[kind]:
            die("E04", rid, "%s 重复" % kind)
        if kind == "src":
            ids["src"][rid] = unq(p.next())
        elif kind == "choice":
            a = [unq(p.next()) for _ in range(5)]
            ids["choice"][rid] = {"src": a[0], "a": a[1], "b": a[2], "pick": a[3], "prove": a[4]}
            if not re.fullmatch(r"S\d+", a[0]):
                die("E04", rid, "choice 的第一栏要是 src ID，实际 %r" % a[0])
        elif kind == "hole":
            r = p.next()
            note = unq(p.next())
            st = p.next()
            if not re.fullmatch(r"R\d+", r):
                die("E04", rid, "hole 要挂在 R<n> 上，实际 %r" % r)
            if st not in HOLES:
                die("E16", rid, "状态只能是 open/waived/closed，实际 %r" % st)
            if st == "open":
                die("E16", rid, "未闭的缺口挡路，不许带着它继续")
            if not note.strip():
                die("E16", rid, "%s 也得写清缺的是什么" % st)
            ids["hole"][rid] = {"promise": r, "note": note, "state": st}
        else:  # promise
            body = {"id": rid, "text": unq(p.next()), "seen": set(), "line": p.line()}
            p.eat("{")
            while p.peek() != "}":
                if p.eof():
                    die("E20", "文件尾", "%s 没闭合" % rid)
                f = p.next()
                if f not in FIELDS:
                    die("E01", "%s line %d" % (rid, p.line()), "%r 不是 promise 的栏" % f)
                if f in body["seen"]:
                    die("E02", "%s.%s" % (rid, f), "出现两次")
                body["seen"].add(f)
                if f in ("reads", "bounds", "trap"):
                    body[f] = unq(p.next())
                elif f == "status":
                    v = p.next()
                    if v not in STATUSES:
                        die("E08", "%s.status" % rid, "只能是 regress/new/gap，实际 %r" % v)
                    body[f] = v
                else:
                    vals = []
                    while True:
                        vals.append(p.next())
                        if p.peek() == ",":
                            p.next()
                            continue
                        break
                    body[f] = vals
                    if not vals:
                        die("E02", "%s.%s" % (rid, f), "空的")
            p.eat("}")
            for req in ("from", "status", "reads", "bounds", "trap", "evidence"):
                if req not in body["seen"]:
                    die("E02", "%s.%s" % (rid, req), "必填栏缺失")
            for t in ("reads", "bounds", "trap"):
                if not body[t].strip():
                    die("E14", "%s.%s" % (rid, t), "不许留空 —— 空着等于没做这个决定")
            ids["promise"][rid] = body
    p.eat("}")
    if not p.eof():
        die("E03", "line %d" % p.line(), "service 之后还有东西")
    return svc, ids


# ---------- 清单 ----------
def inventory_from_project(binary, root=ROOT):
    b = os.path.join(ROOT, binary) if not os.path.exists(binary) else binary
    accept = open(os.path.join(root, "code/webapi/deploy/accept-table.sh"), encoding="utf-8").read()
    assertions = sorted({int(x) for x in re.findall(r"^ck (\d+) ", accept, re.M)})
    sup = open(os.path.join(root, "code/sup/mutation-score.py"), encoding="utf-8").read()
    mutants = {}
    for m in re.finditer(r'id="(S\d+)",[^\n]*?expect=\{([0-9,\s]*)\}', sup):
        mutants[m.group(1)] = sorted({int(x) for x in re.findall(r"\d+", m.group(2))})
    if not os.path.exists(b):
        die("E05", "清单", "找不到二进制 %s —— 先 sh code/sup/build.sh sup，或用 --inventory" % b)
    c = json.loads(subprocess.run([b, "--contract"], capture_output=True, text=True, check=True).stdout)
    return {
        "assertions": assertions,
        "goals": {g["id"]: sorted(int(x) for x in g["proven"]) for g in c["goals"]},
        "examples": sorted(int(e["id"]) for e in c["examples"]),
        "mutants": mutants,
    }


def inventory_from_json(path):
    d = json.load(open(path, encoding="utf-8"))
    return {
        "assertions": sorted(int(x) for x in d["assertions"]),
        "goals": {k: sorted(int(y) for y in v) for k, v in d.get("goals", {}).items()},
        "examples": sorted(int(x) for x in d.get("examples", [])),
        "mutants": {k: sorted(int(y) for y in v) for k, v in d.get("mutants", {}).items()},
    }


# ---------- 合法性（定义里的 L1..L8）----------
def check(ids, inv, allow_new=True, orphan=True, legacy_s=False):
    errs = []
    claimed_src, claimed_mut = set(), set()
    declared_wire, declared_unit, declared_goal = set(), set(), set()
    for rid, b in ids["promise"].items():
        try:
            for s in b["from"]:
                if s not in ids["src"]:
                    die("E06", "%s.from" % rid, "没有源句 %s" % s)
                claimed_src.add(s)
            wires = sorted(int(x) for x in b.get("wire", []))
            units = sorted(int(x) for x in b.get("unit", []))
            for w in wires:
                if w not in inv["assertions"]:
                    die("E05", "%s.wire" % rid, "验收表里没有断言 #%d" % w)
            for u in units:
                if u not in inv["examples"]:
                    die("E05", "%s.unit" % rid, "样例表里没有样例 @%d" % u)
            declared_wire |= set(wires)
            declared_unit |= set(units)
            declared_goal |= set(b.get("goal", []))
            for g in b.get("goal", []):
                if g not in inv["goals"]:
                    die("E05", "%s.goal" % rid, "契约里没有目标 %s" % g)
            if b["status"] != "gap" and not wires and not units:
                die("E09", rid, "status=%s 却没有任何判据撑着（只有文字 = 未证）" % b["status"])
            for g in b.get("goal", []):
                if sorted(inv["goals"][g]) != wires:
                    die("E10", "%s.goal %s" % (rid, g),
                        "目标由 %s 证明，条款写的是 %s —— 一边改了另一边没改"
                        % (fmt(inv["goals"][g]), fmt(wires)))
            ev = b["evidence"]
            if ev == ["none"]:
                if not b["trap"].startswith("无"):
                    named = [g for g, h in ids["hole"].items() if h["promise"] == rid and g in b["trap"]]
                    if not named:
                        die("E13", "%s.evidence" % rid,
                            "写了反例却没署判据：要么给 evidence，要么在 trap 里点名一条 hole")
            else:
                for e in ev:
                    if e.startswith("M"):
                        sid = "S" + e[1:]
                        if sid not in inv["mutants"]:
                            die("E05", "%s.evidence" % rid, "变异表里没有 %s" % e)
                        hit = set(inv["mutants"][sid]) & set(wires)
                        if not hit:
                            die("E11", "%s.evidence %s" % (rid, e),
                                "它实测弄红的是 %s，本条款声明的是 %s —— 打的不是这条"
                                % (fmt(inv["mutants"][sid]), fmt(wires) or "无"))
                        claimed_mut.add(sid)
                    elif legacy_s and e.startswith("S") and e[1:].isdigit():
                        # 三臂实验期的口径：那时变异还写 S<n>，与源句撞名（实验的头号发现，已修）
                        sid = e
                        if sid not in inv["mutants"]:
                            die("E05", "%s.evidence" % rid, "变异表里没有 %s" % e)
                        if not set(inv["mutants"][sid]) & set(wires):
                            die("E11", "%s.evidence %s" % (rid, e), "打的不是本条款")
                        claimed_mut.add(sid)
                    elif e.startswith("@"):
                        n = int(e[1:])
                        if n not in inv["examples"]:
                            die("E05", "%s.evidence" % rid, "样例表里没有 %s" % e)
                        if n not in units:
                            die("E12", "%s.evidence %s" % (rid, e), "本条款没声明这个样例，认领无效")
                    else:
                        die("E01", "%s.evidence" % rid,
                            "%r 既不是变异 M<n> 也不是样例 @<n>，也不是 none"
                            "（源句用 S<n>，变异用 M<n>，两套编号不许混）" % e)
            if b["status"] == "new" and allow_new == "strict":
                die("E19", rid, "声明了 new，而这次带了 --strict（要求当场有基线可判先红）")
        except Fail as f:
            errs.append(f)
    for s in ids["src"]:
        if s not in claimed_src:
            errs.append(Fail("E07", s, "这句原文没被任何条款认领 —— 它要么没译，要么白写了"))
    for aid in ids["hole"]:
        h = ids["hole"][aid]
        if h["promise"] not in ids["promise"]:
            errs.append(Fail("E06", aid, "挂在不存在的 %s 上" % h["promise"]))
        elif aid not in ids["promise"][h["promise"]]["trap"]:
            errs.append(Fail("E17", aid, "没在 %s 的 trap 里被点名 —— 缺口就成了别人的笔记" % h["promise"]))
    # 孤儿判据：清单里有、没有一条承诺引用 —— 它红的时候没人知道那算违反了哪句话。
    # orphan=False 用于增量登记（只登记其中一句需求时，清单里当然有没人认领的判据）。
    for a in (inv["assertions"] if orphan else []):
        if a not in declared_wire:
            errs.append(Fail("E18", "#%d" % a, "断言没被任何承诺引用：它会红，但没人知道那算违了哪句"))
    for e in (inv["examples"] if orphan else []):
        if e not in declared_unit:
            errs.append(Fail("E18", "@%d" % e, "样例没被任何承诺引用"))
    for g in (inv["goals"] if orphan else {}):
        if g not in declared_goal:
            errs.append(Fail("E18", "goal %s" % g, "契约里的目标没被任何承诺认领"))
    # 没人署名的变异不是错：它是覆盖分的材料，不是某条款的反例。只提示，不当失败。
    unclaimed = sorted(set(inv["mutants"]) - claimed_mut)
    for a, c in ids["choice"].items():
        if not c["pick"].strip():
            errs.append(Fail("E15", a, "两种读法都写了，选定却是空的 —— 未定的歧义不许带着实现"))
        if c["src"] not in ids["src"]:
            errs.append(Fail("E06", a, "引用了不存在的源句 %s" % c["src"]))
    return errs, unclaimed


def fmt(vals):
    return "、".join("#%s" % v for v in vals) if vals else "—"


# ---------- 与 TSV 对账 / 从 TSV 转写 ----------
ST = {"回归": "regress", "新增": "new", "缺口": "gap"}
RS = {"已闭": "closed", "不闭": "waived", "未闭": "open"}


def tsv(path):
    rows = []
    for line in open(path, encoding="utf-8"):
        if line.startswith("#") or not line.strip():
            continue
        rows.append(line.rstrip("\n").split("\t"))
    return rows


def ev_from_tsv(cell):
    """TSV 侧的证据编号（S21 / E9 / -）翻成语言侧（M21 / @9 / none）。"""
    if cell.strip() in ("-", ""):
        return ["none"]
    out = []
    for x in re.split(r"[,，]", cell.strip()):
        x = x.strip()
        out.append("M" + x[1:] if x.startswith("S") else "@" + x[1:])
    return out


def ev_to_tsv(vals):
    return ",".join(v[1:] if v.startswith("M") else "E" + v[1:] for v in vals)


def emit(reqdir):
    out = ["// 由 code/sup/dpt-check.py --emit %s 机械转写（等价性证明用，不是手写样本）" % reqdir.rstrip("/"),
           'service blobd {']
    sm = open(os.path.join(reqdir, "SRC.md"), encoding="utf-8").read()
    for sid, txt in re.findall(r"^(S\d+) (.*)$", sm, re.M):
        out.append('  src %s "%s"' % (sid, esc(txt.strip())))
    tr = {r[0]: r for r in tsv(os.path.join(reqdir, "TRANS.tsv"))}
    for r in tsv(os.path.join(reqdir, "REQ.tsv")):
        rid, text, srcs, kind, goals, wires, units = (r + [""] * 7)[:7]
        t = tr.get(rid, ["", "", "", "", "", ""])
        out.append('  promise %s "%s" {' % (rid, esc(text)))
        out.append("    from %s" % srcs)
        out.append("    status %s" % ST.get(kind, kind))
        if goals.strip() not in ("-", ""):
            out.append("    goal %s" % goals)
        if wires.strip() not in ("-", ""):
            out.append("    wire %s" % wires)
        if units.strip() not in ("-", ""):
            out.append("    unit %s" % units)
        out.append('    reads "%s"' % esc(t[2]))
        out.append('    bounds "%s"' % esc(t[3]))
        out.append('    trap "%s"' % esc(t[4]))
        out.append("    evidence %s" % ",".join(ev_from_tsv(t[5])))
        out.append("  }")
    for r in tsv(os.path.join(reqdir, "AMBIG.tsv")):
        r = (r + [""] * 7)[:7]
        out.append('  choice %s %s "%s" "%s" "%s" "%s"'
                   % (r[0], r[1], esc(r[2]), esc(r[3]), esc(r[4]), esc(r[5])))
    for r in tsv(os.path.join(reqdir, "GAP.tsv")):
        r = (r + [""] * 4)[:4]
        out.append('  hole %s %s "%s" %s' % (r[0], r[1], esc(r[2]), RS.get(r[3], r[3])))
    out.append("}")
    return "\n".join(out) + "\n"


def against(dpt, reqdir):
    svc, ids = parse(open(dpt, encoding="utf-8").read())
    bad = []
    req = {r[0]: (r + [""] * 7)[:7] for r in tsv(os.path.join(reqdir, "REQ.tsv"))}
    tr = {r[0]: (r + [""] * 6)[:6] for r in tsv(os.path.join(reqdir, "TRANS.tsv"))}
    am = {r[0]: (r + [""] * 7)[:7] for r in tsv(os.path.join(reqdir, "AMBIG.tsv"))}
    gp = {r[0]: (r + [""] * 4)[:4] for r in tsv(os.path.join(reqdir, "GAP.tsv"))}
    for label, a, b in (("条款", set(req), set(ids["promise"])), ("歧义", set(am), set(ids["choice"])),
                        ("缺口", set(gp), set(ids["hole"]))):
        if a != b:
            bad.append("%s集合不等：只在 TSV %s / 只在 .dpt %s" % (label, sorted(a - b), sorted(b - a)))
    for rid, r in req.items():
        p = ids["promise"].get(rid)
        if not p:
            continue
        t = tr.get(rid, [""] * 6)
        want = {"text": r[1], "from": r[2].split(","), "status": ST.get(r[3], r[3]),
                "goal": r[4].split(",") if r[4] not in ("-", "") else [],
                "wire": r[5].split(",") if r[5] not in ("-", "") else [],
                "unit": r[6].split(",") if r[6] not in ("-", "") else [],
                "reads": t[2], "bounds": t[3], "trap": t[4], "evidence": ev_from_tsv(t[5])}
        for k, v in want.items():
            got = p.get(k, [])
            if (got if isinstance(v, list) else str(got)) != (v if isinstance(v, list) else str(v)):
                bad.append("%s.%s 不一致\n      TSV：%s\n      dpt：%s" % (rid, k, v, got))
    for aid, a in am.items():
        c = ids["choice"].get(aid)
        if c and (c["src"], c["a"], c["b"], c["pick"]) != (a[1], a[2], a[3], a[4]):
            bad.append("%s 的读法/选定与 AMBIG 不一致" % aid)
    for gid, g in gp.items():
        h = ids["hole"].get(gid)
        if h and (h["promise"], h["note"], h["state"]) != (g[1], g[2], RS.get(g[3], g[3])):
            bad.append("%s 的挂法/缺什么/状态与 GAP 不一致" % gid)
    return bad


# ---------- 入口 ----------
def main(argv):
    if "--self-check" in argv:
        path = argv[argv.index("--spec") + 1] if "--spec" in argv \
            else os.path.join(ROOT, "lang/droplet.dpt")
        spec = set(re.findall(r"^(E\d\d) ", open(path, encoding="utf-8").read(), re.M))
        impl = set(CODES)
        if spec != impl:
            print("挂：定义与实现的对账不齐", file=sys.stderr)
            if spec - impl:
                print("  文档写了但不会发生：%s —— 定义在骗读它的人" % " ".join(sorted(spec - impl)), file=sys.stderr)
            if impl - spec:
                print("  会发生但没写进定义：%s —— 有没写进定义的失败模式" % " ".join(sorted(impl - spec)), file=sys.stderr)
            return 1
        print("绿：定义里的 %d 条错误码 = 实现会吐的 %d 条，逐一对上。" % (len(spec), len(impl)))
        return 0

    if "--self-test" in argv:
        return self_test()

    if "--emit" in argv:
        sys.stdout.write(emit(argv[argv.index("--emit") + 1]))
        return 0

    dpt = argv[0] if argv and not argv[0].startswith("--") else None
    if not dpt:
        print(__doc__.strip(), file=sys.stderr)
        return 2
    if "--against" in argv:
        bad = against(dpt, argv[argv.index("--against") + 1])
        if bad:
            print("挂：与 TSV 登记面对不上（%d 处）" % len(bad), file=sys.stderr)
            for b in bad[:20]:
                print("  - " + b, file=sys.stderr)
            return 1
        svc, ids = parse(open(dpt, encoding="utf-8").read())
        print("绿：登记面等价 —— %d 条承诺 / %d 句原文 / %d 处歧义 / %d 个缺口，逐栏一致。"
              % (len(ids["promise"]), len(ids["src"]), len(ids["choice"]), len(ids["hole"])))
        return 0

    if "--inventory" in argv:
        inv = inventory_from_json(argv[argv.index("--inventory") + 1])
    else:
        b = argv[argv.index("--binary") + 1] if "--binary" in argv else ".build/sup/blobd"
        inv = inventory_from_project(b,
                                    argv[argv.index("--root") + 1] if "--root" in argv else ROOT)
    svc, ids = parse(open(dpt, encoding="utf-8").read())
    errs, unclaimed = check(ids, inv, True, "--partial" not in argv,
                            legacy_s="--legacy-s" in argv)
    if "--strict" in argv:
        for rid, b in ids["promise"].items():
            if b["status"] == "new":
                errs.append(Fail("E19", rid, "声明了 new，而这次带了 --strict（先红后绿要当场判）"))
    print("dpt-check %s：service %s / %d 句原文 / %d 条承诺 / %d 处歧义 / %d 个缺口"
          % (dpt, svc, len(ids["src"]), len(ids["promise"]), len(ids["choice"]), len(ids["hole"])))
    print("  清单：断言 %d 条、目标 %d 个、样例 %d 条、变异 %d 条"
          % (len(inv["assertions"]), len(inv["goals"]), len(inv["examples"]), len(inv["mutants"])))
    if errs:
        print("挂：%d 条" % len(errs), file=sys.stderr)
        for e in errs[:40]:
            print("  " + str(e), file=sys.stderr)
        return 1
    if unclaimed:
        print("  提示：%s 没被任何承诺署成反例 —— 它们仍算覆盖分，只是不代表某条写下的语义差异"
              % ",".join(unclaimed))
    print("绿：全部引用存在、每条承诺有判据、每条反例有署名、无孤儿。")
    return 0




# ---------- 错误码的活性证明：每条 E 码都必须被一个最小样本打中 ----------
# 理由与验收表的 L 相相同：写进定义的失败模式如果不会真的发生，定义就在骗读它的人。
# 加一条 E 码就必须在这里加一个能打中它的样本，否则这里红。


def doc(assertions=None, goals=None, examples=None, mutants=None, srcs='  src S1 "a"\n',
        holes="", choices="", inject="", **lines):
    """造一份最小登记文件；关键字参数按栏名整行替换，传 "" 表示删掉这一栏。"""
    f = {"from": "from S1", "status": "status regress", "goal": "goal g1", "wire": "wire 1",
         "unit": "unit 1", "reads": 'reads "r"', "bounds": 'bounds "n"',
         "trap": 'trap "无（译法与原文同宽）"', "evidence": "evidence none"}
    f.update(lines)
    rid = f.pop("id", "R1")
    text = f.pop("text", "b")
    body = "".join("    %s\n" % f[k] for k in ("from", "status", "goal", "wire", "unit",
                                               "reads", "bounds", "trap", "evidence") if f[k])
    body += inject
    inv = {"assertions": assertions if assertions is not None else [1],
           "goals": goals if goals is not None else {"g1": [1]},
           "examples": examples if examples is not None else [1],
           "mutants": mutants if mutants is not None else {"S1": [1]}}
    return ("service t {\n%s  promise %s \"%s\" {\n%s  }\n%s%s}\n"
            % (srcs, rid, text, body, choices, holes)), inv


OK_HOLE = '  hole G1 R1 "只数了个数，缺内容比对" waived\n'
OK_CHOICE = '  choice A1 S1 "x" "y" "选 x" "#1"\n'
FIXTURE = [
    ("E01", lambda: doc(holes='  probe X "y"\n')),
    ("E01", lambda: doc(inject='    probe "r"\n')),
    ("E01", lambda: doc(holes='  thing R2 "b"\n')),
    ("E01", lambda: doc(evidence="evidence 大概吧")),
    ("E02", lambda: doc(evidence="")),
    ("E02", lambda: doc(reads='reads "r"\n    reads "r2"')),
    ("E03", lambda: (doc()[0] + "src S2 \"多出来的\"\n", doc()[1])),
    ("E03", lambda: ('src S1 "a"\n', doc()[1])),
    ("E04", lambda: doc(srcs='  src P1 "a"\n', choices="", holes="")),
    ("E04", lambda: doc(id="R1", srcs='  src S1 "a"\n  src S1 "b"\n', choices="", holes="")),
    ("E04", lambda: doc(srcs='  src S1 "a"\n  src S1 "b"\n')),
    ("E05", lambda: doc(assertions=[1, 2], wire="wire 77\n    ", goal="")),
    ("E05", lambda: doc(examples=[1, 2], unit="unit 9\n    ")),
    ("E05", lambda: doc(goal="goal nope\n    ", wire="wire 1\n    ")),
    ("E05", lambda: doc(wire="wire 1\n    ", evidence="evidence M9")),
    ("E06", lambda: doc(**{"from": "from S8"})),
    ("E06", lambda: doc(holes='  hole G1 R8 "缺" waived\n')),
    ("E07", lambda: doc(srcs='  src S1 "a"\n  src S2 "没人认领的原文"\n')),
    ("E08", lambda: doc(status="status 待定")),
    ("E09", lambda: doc(goal="", wire="", unit="")),
    ("E10", lambda: doc(assertions=[1, 2], goals={"g1": [1]}, wire="wire 2\n    ")),
    ("E11", lambda: doc(assertions=[1, 2], goal="", mutants={"S1": [1]}, wire="wire 2\n    ",
                        evidence="evidence M1", trap='trap "改坏投影"')),
    ("E12", lambda: doc(examples=[1], unit="", evidence="evidence @1", trap='trap "改坏投影"')),
    ("E13", lambda: doc(trap='trap "有一种实现能骗过它"')),
    ("E14", lambda: doc(reads='reads "  "')),
    ("E15", lambda: doc(choices='  choice A1 S1 "x" "y" "" "#1"\n')),
    ("E16", lambda: doc(holes='  hole G1 R1 "缺" open\n')),
    ("E16", lambda: doc(holes='  hole G1 R1 "缺" whatever\n')),
    ("E16", lambda: doc(holes='  hole G1 R1 "" waived\n')),
    ("E17", lambda: doc(holes=OK_HOLE)),
    ("E18", lambda: doc(assertions=[1, 2])),
    ("E18", lambda: doc(examples=[1, 2])),
    ("E18", lambda: doc(goals={"g1": [1], "g2": [1]})),
    ("E19", lambda: doc(status="status new")),
    ("E20", lambda: (doc()[0].rsplit("}", 1)[0], doc()[1])),
    ("E20", lambda: (doc()[0].rstrip()[:-1], doc()[1])),
]


def self_test():
    bad = []
    for i, (code, build) in enumerate(FIXTURE):
        try:
            text, inv = build()
        except Exception as e:
            bad.append("  %s 样本构造失败：%s" % (code, e))
            continue
        allow = "strict" if code == "E19" else True
        got = None
        try:
            _svc, ids = parse(text)
            errs, _u = check(ids, inv, allow)
            got = errs[0].code if errs else "无"
        except Fail as f:
            got = f.code
        except Exception:
            got = "崩"
        if got != code:
            bad.append("  %s 没打中：实际报 %s（样本 #%d）" % (code, got, i))
    text, inv = doc(holes=OK_HOLE, choices=OK_CHOICE,
                    trap='trap "无（同宽）；已知边界记在 G1"')
    try:
        _svc, ids = parse(text)
        errs, _u = check(ids, inv, True)
        if errs:
            bad.append("  基准样本不干净：%s" % errs[0])
    except Fail as f:
        bad.append("  基准样本解析失败：%s" % f)
    if bad:
        print("挂：%d 处" % len(bad), file=sys.stderr)
        for b in bad:
            print(b, file=sys.stderr)
        return 1
    print("绿：%d 个样本逐个打中 %d 条错误码，基准样本干净。"
          % (len(FIXTURE), len(set(c for c, _ in FIXTURE))))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except Fail as f:
        print("挂：" + str(f), file=sys.stderr)
        sys.exit(1)
