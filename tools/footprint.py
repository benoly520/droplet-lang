#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""footprint —— 运行时足迹量尺：一条命令的峰值 RSS、最快耗时、磁盘占用，一次量清。

  python3 tools/footprint.py --name py-host --python-floor --disk drop \
      -- python3 drop/hosts/python/interpreter.py drop/examples/fact.dr

  python3 tools/footprint.py --name cs-host --disk drop/hosts/csharp \
      -- dotnet drop/hosts/csharp/bin/Release/net8.0/droplet.dll drop/examples/fact.dr

四条新需求里「越小越好」是唯一能被机器直接量的一条，其余三条（API 极简、AI 只读就会用、
编译成什么）都得靠判据间接证。所以这一把先建，并且建得能被判：默认只报数，
给了 --budget-mb / --budget-ms 才判红（没量过就不发明数字）。

为什么不用 /usr/bin/time：这台机器上没有它，而且要的恰好是「那一个子进程」的峰值内存
与退出码 —— 自己 fork + wait4 才拿得到，顺带能把红归因到被测命令本身。
"""
import argparse
import os
import sys
import time

EXCLUDE_DEFAULT = ("/bin/", "/obj/", "__pycache__", "/.git/")


def run_once(cmd, env=None):
    """跑一次被测命令，返回 (墙钟 ms, 该子进程峰值 RSS MB, 退出码)。"""
    t0 = time.perf_counter()
    pid = os.fork()
    if pid == 0:
        try:
            if env:
                os.environ.update(env)
            dn = os.open(os.devnull, os.O_WRONLY)
            os.dup2(dn, 1)                      # 输出去向不是本题；红不红看退出码
            os.dup2(dn, 2)
            os.execvp(cmd[0], cmd)
        except Exception:
            os._exit(127)                       # 起不来也算红，且原因写在退出码上
    _, status, ru = os.wait4(pid, 0)
    return (time.perf_counter() - t0) * 1000.0, ru.ru_maxrss / 1024.0, os.waitstatus_to_exitcode(status)


def disk_of(path, excludes):
    """目录/文件的字节数与文件数（排除项按子串匹配，符号链接不跟）。"""
    if os.path.isfile(path):
        return os.path.getsize(path), 1
    total, nfiles = 0, 0
    for root, dirs, files in os.walk(path):
        if any(x in root + "/" for x in excludes):
            dirs[:] = []
            continue
        for f in files:
            p = os.path.join(root, f)
            if any(x in p for x in excludes):
                continue
            try:
                total += os.lstat(p).st_size
            except OSError:
                continue
            nfiles += 1
    return total, nfiles


def main(argv):
    ap = argparse.ArgumentParser(prog="tools/footprint.py",
                                 description="运行时足迹量尺（RSS / 耗时 / 磁盘）；-- 之后是被测命令")
    ap.add_argument("--name", default="run", help="这一行的标签")
    ap.add_argument("--reps", type=int, default=5, help="被测命令跑几遍：耗时取最快、RSS 取最大")
    ap.add_argument("--disk", action="append", default=[], help="要量磁盘的目录或文件，可重复")
    ap.add_argument("--exclude", action="append", default=[],
                    help="磁盘统计跳过的子串；不给就用默认 " + ",".join(EXCLUDE_DEFAULT))
    ap.add_argument("--floor-cmd", action="append", default=[], metavar="CMD",
                    help="参照命令（一般是「运行时自己什么也不干」那份），可重复；差值才是被测占的")
    ap.add_argument("--python-floor", action="store_true",
                    help="省事写法：加一条参照 " + sys.executable + " -c pass")
    ap.add_argument("--env", action="append", default=[], metavar="K=V", help="给被测命令加环境变量，可重复")
    ap.add_argument("--expect-rc", type=int, default=0,
                    help="期望退出码；不是它就判红（红必须归因，不许只说「跑过了」）")
    ap.add_argument("--budget-mb", type=float, default=None, help="给了就判：峰值 RSS 超过它即红")
    ap.add_argument("--budget-ms", type=float, default=None, help="给了就判：最快耗时超过它即红")
    ap.add_argument("cmd", nargs=argparse.REMAINDER, help="被测命令行（前面加 --）")
    a = ap.parse_args(argv)

    cmd = [c for c in a.cmd if c != "--"]
    if not cmd:
        sys.exit("没给被测命令：python3 tools/footprint.py -- 要量的命令 …（--help 看全部）")

    env = {}
    for kv in a.env:
        k, _, v = kv.partition("=")
        env[k] = v

    best_ms, peak_mb, rcs = None, 0.0, []
    for _ in range(max(1, a.reps)):
        ms, mb, rc = run_once(cmd, env)
        best_ms = ms if best_ms is None else min(best_ms, ms)
        peak_mb = max(peak_mb, mb)
        rcs.append(rc)
    bad = sorted(set(r for r in rcs if r != a.expect_rc))
    got = "/".join(str(r) for r in bad) if bad else str(a.expect_rc)

    excl = a.exclude or EXCLUDE_DEFAULT
    floors = list(a.floor_cmd) + (["%s -c pass" % sys.executable] if a.python_floor else [])
    floor_runs = []
    for f in floors:
        ms, mb, rc = run_once(f.split(), env)
        floor_runs.append((f, ms, mb, rc))

    print("footprint（%d 遍：耗时取最快、RSS 取最大）" % max(1, a.reps))
    print("  %-10s 峰值 RSS %7.1f MB / 最快 %8.1f ms / rc=%s"
          % (a.name, peak_mb, best_ms, got))
    for f, ms, mb, rc in floor_runs:
        print("  参照 %.1f MB / %.1f ms（rc=%d）← %s；被测比它多要 %.1f MB"
              % (mb, ms, rc, f, peak_mb - mb))
    for d in a.disk:
        b, n = disk_of(d, excl)
        print("  磁盘 %-28s %8.2f MB / %d 个文件（排除 %s）" % (d, b / 2 ** 20, n, ",".join(excl)))

    why = []
    if bad:
        why.append("退出码 %s、期望 %d（先修被测命令，别的判据都还没资格谈）" % (got, a.expect_rc))
    if a.budget_mb is not None and peak_mb > a.budget_mb:
        why.append("RSS %.1f MB > 预算 %.1f MB" % (peak_mb, a.budget_mb))
    if a.budget_ms is not None and best_ms > a.budget_ms:
        why.append("最快 %.1f ms > 预算 %.1f ms" % (best_ms, a.budget_ms))
    if why:
        print("footprint: 红 —— " + "；".join(why))
        return 1
    print("footprint: 绿（在给的预算之内）" if (a.budget_mb or a.budget_ms)
          else "footprint: 只报数 —— 没给预算就不判（没量过的数字不许当判据）")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
