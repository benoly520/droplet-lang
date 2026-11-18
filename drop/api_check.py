#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""api.json 的对账：表 <-> 定义、表 <-> 行为，两个方向都要对上。

  python3 api_check.py [目录]      （默认这个文件所在的目录）

查的东西（每一条都是「两个方向」：表里多的、定义里少的，一样红）：
  1  版本          api.json 的 version = 定义第一行那个版本
  2  错误码        14 条码与中文解与定义「穷举如下」那段逐字相等，且与实现里的表相等
  3  内置名        8 个名字与定义里那几行 `名字(` 相等；arity 拿真程序打：少给一个实参必须是 E2
  4  宿主命令      7 个名字与定义「宿主」那段相等、与实现里的表相等；arity 同上
  5  保留名        3 关键字 + 8 内置 + 11 个「这里没有那种东西」，与定义、实现三方相等
  6  退出码        0/1/2 各拿一个真程序打出来

为什么要有这张表：编译器和文档得吃同一份东西。散文里写错一个字没人发现，
这里写错一个字，门就红 —— 表是用程序读的，所以它必须能被程序查。
"""
import json
import os
import re
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
D = os.path.abspath(sys.argv[1]) if len(sys.argv) > 1 else HERE
PY = sys.executable
INTERP = os.path.join(D, "hosts", "python", "interpreter.py")

bad = []


def red(why):
    bad.append(why)
    print("  红：" + why)


def spec_text():
    with open(os.path.join(D, "spec.txt"), encoding="utf-8") as f:
        return f.read()


def run(src, extra=()):
    """跑一段临时程序，回 (rc, stdout, stderr)。"""
    fd, p = tempfile.mkstemp(suffix=".dr")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            f.write(src)
        r = subprocess.run([PY, INTERP] + list(extra) + [p],
                           capture_output=True, text=True)
        return r.returncode, r.stdout, r.stderr
    finally:
        os.unlink(p)


def code_of(err):
    m = re.search(r"(E\d+)", err)
    return m.group(1) if m else None


def main():
    api = json.load(open(os.path.join(D, "api.json"), encoding="utf-8"))
    s = spec_text()

    # ---- 1 版本
    m = re.search(r"^drop (v[\d.]+)", s, re.M)
    want = m.group(1) if m else "<定义第一行没有版本号>"
    if api.get("version") != want:
        red("版本漂了：表说 %r，定义第一行说 %r" % (api.get("version"), want))

    # ---- 2 错误码：与定义「穷举如下」那段逐字比
    blk = s.split("错误一共 14 种，穷举如下：")[-1].split("输出只有一条规则")[0]
    from_spec = {}
    for line in blk.splitlines():
        for code, zh in re.findall(r"(E\d+)\s+(.+?)(?=\s{2,}E\d+|\s*$)", line.strip()):
            from_spec[code] = zh.strip()
    if from_spec != api.get("errors"):
        only_t = {k: v for k, v in api.get("errors", {}).items() if from_spec.get(k) != v}
        only_s = {k: v for k, v in from_spec.items() if api.get("errors", {}).get(k) != v}
        red("错误码与定义对不上：表多/不同 %r，定义多/不同 %r" % (only_t, only_s))

    # ---- 3 内置名：定义「转换」那段里 `名字(` 那几行 + 实现里的 BUILTINS
    conv = s.split("转换：每样只有一条路")[-1].split("宿主：")[0]
    from_spec = set(re.findall(r"^  ([a-z]+)\(", conv, re.M)) | {"host"}   # host 那一行在「宿主」段
    if from_spec != set(api.get("builtins", {})):
        red("内置名与定义对不上：表 %r，定义 %r"
            % (sorted(api.get("builtins", {})), sorted(from_spec)))

    sys.path.insert(0, os.path.dirname(INTERP))
    import importlib.util
    spec_ = importlib.util.spec_from_file_location("dropref", INTERP)
    ref = importlib.util.module_from_spec(spec_)
    spec_.loader.exec_module(ref)
    impl = {k: v[1] for k, v in ref.BUILTINS.items()}
    mine = {k: v["arity"] for k, v in api.get("builtins", {}).items()}
    if impl != mine:
        red("内置名/arity 与实现对不上：表 %r，实现 %r" % (mine, impl))

    # 3b arity 拿真程序打：少给一个实参必须是 E2（表说几个就是几个）
    for name, meta in api.get("builtins", {}).items():
        n = meta["arity"]
        src = name + "()" if n == 1 else name + '("")'
        rc, out, err = run(src)
        if rc != 1 or code_of(err) != "E2":
            red("%s 的 arity=%d，但 %s 报的是 %r（rc=%d；要 E2）"
                % (name, n, src, code_of(err), rc))
        if "oracle" in meta:
            rc, out, err = run(meta["oracle"] + "\n")
            if rc != 0:
                red("%s 的正例跑不通：%r rc=%d %s" % (name, meta["oracle"], rc, err.strip()[:80]))

    # ---- 4 宿主命令：定义「宿主」那段 + 实现里的 DEF_CMDS
    sec = s.split("宿主：语言与外面之间只有 host 这一个口子")[-1].split("出错")[0]
    from_spec = set(re.findall(r"([a-z]+)\(", sec)) - {"host"}
    if from_spec != set(api.get("host_commands", {})):
        red("宿主命令与定义对不上：表 %r，定义 %r"
            % (sorted(api.get("host_commands", {})), sorted(from_spec)))
    if set(ref.DEF_CMDS) != set(api.get("host_commands", {})):
        red("宿主命令与实现对不上：表 %r，实现 %r"
            % (sorted(api.get("host_commands", {})), sorted(ref.DEF_CMDS)))
    for name in api.get("host_commands", {}):
        rc, out, err = run('host("%s")\n' % name)          # 命令只给一个实参
        if rc != 1 or code_of(err) != "E2":
            red("host(%r) 少一个实参报的是 %r（要 E2）" % (name, code_of(err)))

    # ---- 5 保留名：定义那句 + 实现里的 RESERVED
    r = api.get("reserved", {})
    flat = set(r.get("keywords", [])) | set(r.get("builtins", [])) | set(r.get("no_things", []))
    if len(r.get("keywords", [])) != 3 or len(r.get("builtins", [])) != 8 or len(r.get("no_things", [])) != 11:
        red("保留名的三个桶不是 3/8/11：%d/%d/%d"
            % (len(r.get("keywords", [])), len(r.get("builtins", [])), len(r.get("no_things", []))))
    if flat != set(ref.RESERVED):
        red("保留名与实现对不上：表多的 %r，实现多的 %r"
            % (sorted(flat - set(ref.RESERVED)), sorted(set(ref.RESERVED) - flat)))
    m = re.search(r"（(while for break continue return else range map filter fold sort)）", s)
    if not m or set(m.group(1).split()) != set(r.get("no_things", [])):
        red("11 个「这里没有那种东西」与定义对不上：定义 %r，表 %r"
            % (m.group(1) if m else None, r.get("no_things")))
    m = re.search(r"\+ 3 个关键字（(let fn if)）|3 个关键字（(let fn if)）", s)
    if not m or set((m.group(1) or m.group(2)).split()) != set(r.get("keywords", [])):
        red("3 个关键字与定义对不上：定义 %r，表 %r"
            % ((m.group(1) or m.group(2)) if m else None, r.get("keywords")))

    # ---- 6 退出码：各拿一个真程序打
    rc, _, _ = run("print(\"\")\n")
    if rc != 0:
        red("干净程序的退出码是 %d，表里 0 的那条说不是" % rc)
    rc, _, _ = run("print(1)\n")
    if rc != 1:
        red("红程序的退出码是 %d，表里 1 的那条说不是" % rc)
    r = subprocess.run([PY, INTERP], capture_output=True, text=True)
    if r.returncode != 2:
        red("命令行用错的退出码是 %d，表里 2 的那条说不是" % r.returncode)

    if bad:
        print("  红：api.json 与定义/实现对不上 %d 处 —— 表是拿来给程序读的，漂了就是假的" % len(bad))
        return 1
    print("  绿：api.json —— 版本 %s / %d 条码 / %d 个内置 / %d 个宿主命令 / %d 个保留名 / 退出码 0·1·2，"
          "两边逐字对上，arity 拿真程序打过"
          % (api["version"], len(api["errors"]), len(api["builtins"]),
             len(api["host_commands"]), len(flat)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
