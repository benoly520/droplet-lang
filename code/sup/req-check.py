#!/usr/bin/env python3
"""需求登记核对：条款 -> 目标 -> 断言/样例，链子断在哪一环就红在哪一环。

为什么不是一张 Word 需求表：需求表本身不会失败。这里要的是**可失败**的链子：
  1. 每条条款（R）至少有一条真断言或真样例撑着 —— 只有文字 = 未证；
  2. 条款写的断言编号必须在 accept-table.sh 里真的存在；
  3. 条款写的样例编号必须在 DP_EXAMPLES 里真的存在；
  4. 每条条款声称的目标必须在 DP_GOALS 里，且目标声明的断言集合与条款写的一致
     （目标加了证明而条款没跟上 = 漂移）；
  5. 验收表里的每条断言、样例表里的每条样例、契约里的每条目标，都必须有人认领
     （孤儿断言/孤儿样例/孤儿目标 = 有东西没人承诺，红）；
  6. DP_GOALS 里 proven 为空的目标列为“未证”，不算失败，但单独数出来。

口径：读的是编译产物的 --contract 与验收脚本本体，不读实现源码。
用法：python3 code/sup/req-check.py [二进制]
      默认二进制 .build/check/blobd，默认登记表 req/REQ.tsv。
"""
import json
import os
import re
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(HERE))
REQ = os.path.join(ROOT, "req", "REQ.tsv")
SCRIPT = os.path.join(ROOT, "code", "webapi", "deploy", "accept-table.sh")
BIN = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, ".build", "check", "blobd")

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


def cells(s):
    s = s.strip()
    if s in ("", "-"):
        return []
    return [x.strip() for x in s.split(",") if x.strip()]


rows = []
for lineno, line in enumerate(open(REQ, encoding="utf-8"), 1):
    if not line.strip() or line.lstrip().startswith("#"):
        continue
    f = line.rstrip("\n").split("\t")
    if len(f) != 5:
        die("REQ.tsv 第 %d 行有 %d 列，要求 5 列（编号/条款/目标/断言/样例）" % (lineno, len(f)))
        continue
    rid, text, gs, as_, es = f
    rows.append((rid.strip(), text.strip(), cells(gs), cells(as_), cells(es)))

if not rows:
    die("REQ.tsv 里一条条款都没有")

seen = set()
claimed_a, claimed_e, claimed_g = set(), set(), set()
for rid, text, gs, as_, es in rows:
    if not re.fullmatch(r"R\d+", rid):
        die("条款编号不像 R<数字>：%r" % rid)
    if rid in seen:
        die("条款编号重复：%s" % rid)
    seen.add(rid)
    if not text:
        die("%s 没有条款文字" % rid)
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

for a in sorted(have_a - claimed_a, key=int):
    die("验收断言 #%s 没有被任何条款认领（孤儿断言）" % a)
for e in sorted(have_e - claimed_e, key=int):
    die("样例 #%s 没有被任何条款认领（孤儿样例）" % e)
for g in sorted(set(goals) - claimed_g):
    die("目标 %s 没有被任何条款认领（孤儿目标）" % g)

print("需求登记：%d 条条款 / %d 条断言 / %d 条样例 / %d 条目标（未证目标 %d）"
      % (len(rows), len(have_a), len(have_e), len(goals), len(unproven_goals)))
for rid, text, gs, as_, es in rows:
    ev = []
    if as_:
        ev.append("断言 " + ",".join("#" + a for a in as_))
    if es:
        ev.append("样例 " + ",".join("#" + e for e in es))
    print("  %-4s %s  [%s]" % (rid, text, "；".join(ev)))
if unproven_goals:
    print("  未证目标（DP_GOALS 里 proven 为空，显式承认没被证明）：" + ", ".join(unproven_goals))
for e in errors:
    print("  FAIL " + e, file=sys.stderr)
sys.exit(1 if errors else 0)
