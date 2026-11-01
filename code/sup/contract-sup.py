#!/usr/bin/env python3
"""契约核对（超集形态，SPEC §0.8）：声明的目标必须有真断言撑着，否则构建失败。

和上一轮那个 45 行正则核对器的区别是**读的不是源码文本**：
声明是编译产物里的表（`blobd --goals`），所以改名、挪函数、加包装都改不动它。

三条规则：
  1. 目标写的断言编号必须在 accept-sup.sh 里真的存在 —— 承诺一个不存在的检查 = 失败；
  2. 断言编号必须被至少一条目标引用（孤儿断言 = 有检查没人认领，警告不失败）；
  3. PROVEN 留空 = 人明确承认"这条没被证明"，进未证清单，不失败。
第 3 条是"沉默不是决定"的另一半：不证明可以，但不写下来不行。

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
    script = here + "/accept-sup.sh"

raw = subprocess.run([binary, "--goals"], capture_output=True, text=True)
if raw.returncode != 0:
    die("--goals 失败：" + raw.stderr.strip())

goals, routes, envs = [], [], []
for line in raw.stdout.splitlines():
    f = line.split("\t")
    if f[0] == "route":
        routes.append({"method": f[1], "path": f[2], "impl": f[3]})
    elif f[0] == "env":
        envs.append({"name": f[1], "kind": f[2], "default": f[3]})
    else:
        goals.append({"id": f[0], "text": f[1], "proven": [x for x in f[2].split(",") if x]})

# 脚本里真实存在的断言编号
src = open(script, encoding="utf-8").read()
have = set(re.findall(r'^ck (\d+) ', src, re.M))
if not have:
    die("验收脚本里一条断言都没找到：" + script)

errors, unproven, used = [], [], set()
for g in goals:
    for n in g["proven"]:
        if n not in have:
            errors.append("目标 %s 声称被断言 #%s 证明，但 %s 里没有这条" % (g["id"], n, script))
        used.add(n)
    if not g["proven"]:
        unproven.append(g["id"])

orphans = sorted(have - used, key=int)

doc = {"routes": routes, "envs": envs, "goals": goals,
       "unproven_goals": unproven, "orphan_checks": orphans}
if out:
    json.dump(doc, open(out, "w", encoding="utf-8"), ensure_ascii=False, indent=2)

print("超集契约：%d 条路由 / %d 个配置项 / %d 条目标（未证 %d 条，孤儿断言 %d 条）"
      % (len(routes), len(envs), len(goals), len(unproven), len(orphans)))
lines = []
for g in goals:
    mark = "未证" if not g["proven"] else ",".join("#" + n for n in g["proven"])
    lines.append("  %-22s %s  [%s]" % (g["id"], g["text"], mark))
if unproven:
    lines.append("  注意：以下目标没有断言撑着，按 D.7.3 第 3 条应标'未证 + 原因'：" + ", ".join(unproven))
if orphans:
    lines.append("  提示：以下断言没有目标认领（有检查没人说明它在保证什么）："
                 + ", ".join("#" + n for n in orphans))
try:
    print("\n".join(lines))
except BrokenPipeError:
    sys.stdout.close()
    sys.stderr = open("/dev/null", "w")
for e in errors:
    print("  FAIL " + e, file=sys.stderr)
if errors:
    sys.exit(1)
