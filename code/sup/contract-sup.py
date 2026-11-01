#!/usr/bin/env python3
"""契约核对（docs/SPEC.md §4）：声明的目标必须有真断言撑着，否则失败。

读的不是源码文本，也不是制表符导出：`blobd --contract` 输出四节 JSON
（routes / envs / goals / examples），这份脚本逐节核对形状与引用关系。
改名、挪函数、加包装、改宏，都改不动它 —— 因为它读的是编译产物里的表。

规则：
  1. 每一节都必须非空且形状正确（路由有 method/path/impl，配置有 name/kind/default，
     目标有 id/text/proven，样例有 id/call/want/why）；
  2. 目标写的断言编号必须在验收表里真的存在 —— 承诺一个不存在的检查 = 失败；
  3. 断言编号必须被至少一条目标引用（孤儿断言 = 有检查没人认领，只提示不失败）；
  4. PROVEN 留空 = 人明确承认"这条没被证明"，进未证清单，不失败；
  5. 样例编号唯一、调用与期望非空；一条样例都没有 = 失败（零条可执行样例不算通过）。

用法：python3 code/sup/contract-sup.py <二进制> [验收脚本] [-o out.json]
"""
import json
import re
import subprocess
import sys

def die(msg):
    print("contract-sup: " + msg, file=sys.stderr)
    sys.exit(1)

if len(sys.argv) < 2:
    die("用法：contract-sup.py <二进制> [验收脚本] [-o out.json]")

binary = sys.argv[1]
script = sys.argv[2] if len(sys.argv) > 2 and not sys.argv[2].startswith("-") else None
out = None
if "-o" in sys.argv:
    out = sys.argv[sys.argv.index("-o") + 1]

here = re.sub(r"/[^/]*$", "", sys.argv[0])
if script is None:
    # 断言表只有一份，accept-sup.sh 是薄壳；默认指到表本体。
    script = here + "/../webapi/deploy/accept-table.sh"

raw = subprocess.run([binary, "--contract"], capture_output=True, text=True)
if raw.returncode != 0:
    die("--contract 失败：" + raw.stderr.strip())
try:
    doc = json.loads(raw.stdout)
except json.JSONDecodeError as e:
    die("--contract 输出的不是 JSON：%s" % e)

errors = []
for sec in ("routes", "envs", "goals", "examples"):
    if not isinstance(doc.get(sec), list):
        errors.append("契约缺一节或形状不对：%s" % sec)
    elif not doc[sec]:
        errors.append("契约的 %s 是空的 —— 空表不算声明" % sec)

routes = doc.get("routes") or []
envs = doc.get("envs") or []
goals = doc.get("goals") or []
examples = doc.get("examples") or []

for r in routes:
    for k in ("method", "path", "impl", "ok", "auth", "guard", "why"):
        if not str(r.get(k, "")).strip():
            errors.append("路由缺 %s：%r" % (k, r))
    if not str(r.get("method", "")).isupper():
        errors.append("路由动词不是大写：%r" % r.get("method"))
    if not str(r.get("path", "")).startswith("/"):
        errors.append("路由路径不以 / 开头：%r" % r.get("path"))
if len({(r.get("method"), r.get("path")) for r in routes}) != len(routes):
    errors.append("路由表里有重复的 (method, path)")

KINDS = {"str", "port", "size"}
for e in envs:
    if not str(e.get("name", "")).strip():
        errors.append("配置项缺 name：%r" % e)
    if e.get("kind") not in KINDS:
        errors.append("配置项 %s 的 kind 不在 %s 里：%r" % (e.get("name"), sorted(KINDS), e.get("kind")))
    if "default" not in e:
        errors.append("配置项 %s 没有 default（默认值必须显式）" % e.get("name"))
if len({e.get("name") for e in envs}) != len(envs):
    errors.append("配置表里有重复的名字")

# 脚本里真实存在的断言编号
src = open(script, encoding="utf-8").read()
have = set(re.findall(r'^ck (\d+) ', src, re.M))
if not have:
    die("验收脚本里一条断言都没找到：" + script)

unproven, used = [], set()
for g in goals:
    proven = g.get("proven") or []
    if not str(g.get("id", "")).strip() or not str(g.get("text", "")).strip():
        errors.append("目标缺 id 或 text：%r" % g)
        continue
    for n in proven:
        if n not in have:
            errors.append("目标 %s 声称被断言 #%s 证明，但 %s 里没有这条" % (g["id"], n, script))
        used.add(n)
    if not proven:
        unproven.append(g["id"])
if len({g.get("id") for g in goals}) != len(goals):
    errors.append("目标里有重复的 id")

ids = [e.get("id") for e in examples]
if len(set(ids)) != len(ids):
    errors.append("样例编号重复")
for e in examples:
    if not str(e.get("call", "")).strip() or not str(e.get("want", "")).strip():
        errors.append("样例缺调用或期望：%r" % e)
    if not str(e.get("why", "")).strip():
        errors.append("样例 #%s 没有说明它在钉什么（沉默不算决定）" % e.get("id"))

orphans = sorted(have - used, key=int)

doc["unproven_goals"] = unproven
doc["orphan_checks"] = orphans
if out:
    json.dump(doc, open(out, "w", encoding="utf-8"), ensure_ascii=False, indent=2)

print("超集契约：%d 条路由 / %d 个配置项 / %d 条目标 / %d 条样例（未证 %d，孤儿断言 %d）"
      % (len(routes), len(envs), len(goals), len(examples), len(unproven), len(orphans)))
lines = []
for g in goals:
    mark = "未证" if not g.get("proven") else ",".join("#" + n for n in g["proven"])
    lines.append("  %-22s %s  [%s]" % (g.get("id"), g.get("text"), mark))
if unproven:
    lines.append("  注意：以下目标没有断言撑着（写 \"\" 是显式承认）：" + ", ".join(unproven))
if orphans:
    lines.append("  提示：以下断言没有目标认领：" + ", ".join("#" + n for n in orphans))
try:
    print("\n".join(lines))
except BrokenPipeError:
    sys.stdout.close()
    sys.stderr = open("/dev/null", "w")
for e in errors:
    print("  FAIL " + e, file=sys.stderr)
if errors:
    sys.exit(1)
