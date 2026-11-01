#!/usr/bin/env python3
"""S4 的注入器：把某条目标的"证明断言编号"改成不存在的 #99。

单独成文件是因为它要在 shell 脚本里跑，而 C 的续行反斜杠穿过两层 heredoc
一定会被吃掉 —— 上一版就是这么静默地"注入失败却报告抓住"的。
现在注入失败 = 非零退出 = 脚本中止，**不允许假绿**。
"""
import re
import sys

if len(sys.argv) != 2:
    sys.exit("用法：sabotage_s4.py <blobd.c 的副本>")

path = sys.argv[1]
src = open(path, encoding="utf-8").read()
# 只动 health_open 这一条：它的 PROVEN 是单个编号 "1"，改成不存在的 #99。
# 用整行查找替换而不是正则 —— 上一版用正则，模式里 C 的右括号转义怎么写都别扭，
# 结果脚本自己编译不过，测试静默不跑。简单到不容易错才是这里要的写法。
lines = src.split("\n")
hit = -1
for i, line in enumerate(lines):
    if "X(health_open," in line:
        hit = i
        break
if hit < 0:
    sys.exit("S4 注入失败：找不到 health_open 那条目标（DP_GOALS 的形状变了，脚本要跟着改）")
if '"1")' not in lines[hit]:
    sys.exit("S4 注入失败：health_open 的证明编号不再是 \"1\"，脚本要跟着改（拒绝假装抓住）")
lines[hit] = lines[hit].replace('"1")', '"99")', 1)
open(path, "w", encoding="utf-8").write("\n".join(lines))
