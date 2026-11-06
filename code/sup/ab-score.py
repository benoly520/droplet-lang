#!/usr/bin/env python3
# ab-score.py —— 三臂实验的同一把尺。
#
# 为什么要"一个判卷器、两个读者"：如果臂 1 用 dpt-check 判、臂 3 另写一套逻辑判，
# 分差就可能来自判卷标准而不是介质。两种介质都归到同一个内存结构（promise/choice/hole），
# 再走 dpt-check.check() 的同一组规则 —— 结构那一项对两臂严格同口径。
#
# 两臂不完全同口径的地方只有一处，并且它本身就是一个发现：
#   .dpt 有解析器，格式错当场拒收；TSV 没有解析器，列数错了只能事后数出来。
# 所以这里把「格式」和「结构」分开报：格式行错只出现在 TSV 臂，结构错两臂同一把尺。
#
# 判卷口径钉在实验当时的语言版本上：`--legacy-s` 让 evidence 里的变异仍写作 S<n>
# （三臂都是用那个口径写的；实验之后已改成 M<n>）。用现在的口径判旧答卷会假红。
#
# 用法：python3 code/sup/ab-score.py <文件或目录>...
import importlib.machinery
import importlib.util
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
_spec = importlib.util.spec_from_loader(
    "dptcheck", importlib.machinery.SourceFileLoader("dptcheck", os.path.join(HERE, "dpt-check.py")))
D = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(D)

INV = D.inventory_from_json(os.path.join(HERE, "..", "..", "lang", "ab", "inventory.json"))
ARITY = {"REQ": 7, "TRANS": 6, "AMBIG": 7, "GAP": 4, "SRC": 2}


def read_tsv(path):
    ids = {"src": {}, "promise": {}, "choice": {}, "hole": {}}
    fmt = []
    sec = None
    for n, raw in enumerate(open(path, encoding="utf-8"), 1):
        line = raw.rstrip("\n")
        m = re.match(r"#\s*[-=]{2,}\s*([A-Za-z_]+)", line)
        if m:
            sec = m.group(1).upper()
            continue
        if not line.strip() or line.startswith("#"):
            continue
        if sec is None:
            fmt.append("第 %d 行在任何段之外：%s" % (n, line[:40]))
            continue
        c = line.split("\t")
        if len(c) < ARITY[sec]:
            fmt.append("第 %d 行 %s 段只有 %d 列（口径要 %d 列）" % (n, sec, len(c), ARITY[sec]))
            continue
        if sec == "SRC":
            ids["src"][c[0]] = c[1]
        elif sec == "REQ":
            ids["promise"][c[0]] = {"text": c[1], "from": c[2].split(","),
                                    "status": D.ST.get(c[3], c[3]),
                                    "goal": [] if c[4] == "-" else c[4].split(","),
                                    "wire": [] if c[5] == "-" else c[5].split(","),
                                    "unit": [] if c[6] == "-" else c[6].split(",")}
        elif sec == "TRANS":
            p = ids["promise"].get(c[0])
            if p:
                p.update({"reads": c[2], "bounds": c[3], "trap": c[4], "evidence": D.ev_from_tsv(c[5])})
            else:
                fmt.append("第 %d 行 TRANS 挂在不存在的 %s" % (n, c[0]))
        elif sec == "AMBIG":
            ids["choice"][c[0]] = {"src": c[1], "a": c[2], "b": c[3], "pick": c[4], "prove": c[5]}
        elif sec == "GAP":
            ids["hole"][c[0]] = {"promise": c[1], "note": c[2], "state": D.RS.get(c[3], c[3])}
    for p in ids["promise"].values():
        for k in ("reads", "bounds", "trap"):
            p.setdefault(k, "")
        p.setdefault("evidence", ["none"])
        p.setdefault("status", "regress")
        p.setdefault("from", [])
    return ids, fmt


def judge(path):
    """返回 (结构错, 格式错, 读出来的登记结构)。两臂走同一个 check()。"""
    if path.endswith(".dpt"):
        try:
            _svc, ids = D.parse(open(path, encoding="utf-8").read())
        except D.Fail as f:
            return [str(f)], ["语法：%s" % f], {"src": {}, "promise": {}, "choice": {}, "hole": {}}
        fmt = []
    else:
        ids, fmt = read_tsv(path)
    return [str(e) for e in D.check(ids, INV, True, orphan=False, legacy_s=True)[0]], fmt, ids


def shape(ids):
    return "%d promise / %d choice / %d hole / %d src" % (
        len(ids["promise"]), len(ids["choice"]), len(ids["hole"]), len(ids["src"]))


def main(argv):
    files = []
    for a in argv:
        if os.path.isdir(a):
            files += sorted(os.path.join(a, f) for f in os.listdir(a) if f.startswith("out-"))
        else:
            files.append(a)
    ok = 0
    for f in files:
        if not os.path.exists(f):
            print("%-36s 缺文件" % f)
            continue
        errs, fmt, ids = judge(f)
        tag = "结构绿" if not errs else "结构挂（%d 条）" % len(errs)
        extra = "" if not fmt else "  格式挂 %d 行" % len(fmt)
        print("%-36s %s%s：%s" % (f, tag, extra, shape(ids)))
        for e in errs[:3]:
            print("      " + e)
        for e in fmt[:3]:
            print("      格式 " + e)
        if not errs and not fmt:
            ok += 1
    print("\n完全合法（结构 + 格式）：%d / %d" % (ok, len(files)))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
