#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""编译器对账：同一份 .dr，机器码跑出来的东西必须与参考实现逐字相同。

  python3 tools/codegen_check.py [--reps N]

编译器本身是 C（drop/codegen/dropc.c），这里只用 cc 把它编成可执行文件，
再拿它编 .dr —— 全程不经过 python、as、ld。python 只充当对账的人。

查四件事：
  1  写出的产物是裸 ELF：没有 INTERP 段、ldd 说「not a dynamic executable」
  2  输出与参考实现逐字相同（这是唯一可信的判据 —— 编译器自己说的不算）
  3  子集之外的输入当场拒绝，退出码 2，并且报的是第几行（不许猜、不许编）
  4  量一把：磁盘、常驻内存、启动→退出（同一把尺：fork + exec + wait4，见 docs/BUILD.md §3）

量法上的老实话：进程自己 fork 出来的子进程会继承父进程的 RSS，ru_maxrss 又是高水位，
所以这里的数只能当作「同一把秤上的相对值」；绝对下限要看本机的 /bin/true。
"""
import os
import statistics
import subprocess
import sys
import tempfile
import time

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DROPC_SRC = os.path.join(ROOT, "drop", "codegen", "dropc.c")
INTERP = os.path.join(ROOT, "drop", "hosts", "python", "interpreter.py")
CASES = [
    os.path.join(ROOT, "drop", "codegen", "cases", "tail.dr"),
    os.path.join(ROOT, "drop", "codegen", "cases", "arith.dr"),
    os.path.join(ROOT, "drop", "codegen", "cases", "ifs.dr"),
    os.path.join(ROOT, "drop", "examples", "fact.dr"),
]
REFUSES = [
    (os.path.join(ROOT, "drop", "codegen", "cases", "refuse_list.dr"), "第 2 行", "子集之外"),
    (os.path.join(ROOT, "drop", "codegen", "cases", "refuse_bool.dr"), "第 2 行", "布尔"),
    (os.path.join(ROOT, "drop", "codegen", "cases", "refuse_cond.dr"), "第 2 行", "条件"),
]
PY = sys.executable
REPS = 30
if "--reps" in sys.argv:
    REPS = int(sys.argv[sys.argv.index("--reps") + 1])

bad = []


def red(why):
    bad.append(why)
    print("  红：" + why)


def run(cmd, **kw):
    return subprocess.run(cmd, capture_output=True, text=True, **kw)


def measure(cmd, reps, shim):
    """时间用 fork+execvp+wait4 量（中位）；RSS 交给 rssshim 量 ——
    在 Python 里 wait4 量小目标，量到的是「父进程 + 目标」的最大值，那是假的。"""
    ms = []
    for _ in range(reps):
        pid = os.fork()
        if pid == 0:
            fd = os.open(os.devnull, os.O_WRONLY)
            os.dup2(fd, 1)
            os.dup2(fd, 2)
            try:
                os.execvp(cmd[0], cmd)
            finally:
                os._exit(127)
        t0 = time.perf_counter()
        os.wait4(pid, 0)
        ms.append((time.perf_counter() - t0) * 1000.0)
    r = subprocess.run([shim] + cmd, capture_output=True, text=True)
    kb = int(r.stdout.split("peak_rss_kb=")[1].split()[0])
    return statistics.median(ms), kb


def main():
    tmp = tempfile.mkdtemp()
    dropc = os.path.join(tmp, "dropc")
    cc = run(["cc", "-O2", "-Wall", "-Wextra", "-o", dropc, DROPC_SRC])
    if cc.returncode != 0:
        red("编译器编不出来：%s" % cc.stderr.strip()[:300])
        print("codegen: 红")
        return 1
    print("编译器：%s（cc -O2 → %s；%d 行）"
          % (os.path.relpath(DROPC_SRC, ROOT), os.path.basename(dropc),
             sum(1 for _ in open(DROPC_SRC, encoding="utf-8"))))

    # ---- 1+2 每个用例：编、跑、比对
    bins = []
    for src in CASES:
        out = os.path.join(tmp, os.path.basename(src) + ".bin")
        r = run([dropc, src, "-o", out])
        if r.returncode != 0:
            red("%s 编译失败 rc=%d %s" % (src, r.returncode, r.stderr.strip()[:120]))
            continue
        want = run([PY, INTERP, src])
        got = run([out])
        if got.returncode != want.returncode or got.stdout != want.stdout:
            red("%s 编译产物与参考实现不一样：rc=%d/%d out=%r want=%r"
                % (os.path.basename(src), got.returncode, want.returncode,
                   got.stdout[:80], want.stdout[:80]))
            continue
        pem = run(["readelf", "-lW", out])
        if "INTERP" in pem.stdout:
            red("%s 还有 INTERP 段 —— 这不是裸 ELF" % os.path.basename(src))
        ldd = run(["ldd", out])
        if "not a dynamic executable" not in ldd.stdout + ldd.stderr:
            red("%s 被 ldd 认出是动态的：%r" % (os.path.basename(src), ldd.stdout[:80]))
        bins.append((src, out, got.stdout))
        print("  对上 %-24s %5d B  %d 行输出逐字相同"
              % (os.path.basename(src), os.path.getsize(out), got.stdout.count("\n")))

    # ---- 3 子集之外：拒绝，而且说第几行、说对是哪一类
    for path, line, why in REFUSES:
        r = run([dropc, path, "-o", os.path.join(tmp, "no.bin")])
        if r.returncode != 2 or line not in r.stderr or "子集之外" not in r.stderr:
            red("%s 没有被正确拒绝：rc=%d err=%r" % (os.path.basename(path), r.returncode, r.stderr.strip()[:140]))
        elif why not in r.stderr:
            red("%s 拒绝了，但理由不是「%s」：%r" % (os.path.basename(path), why, r.stderr.strip()[:140]))
        else:
            print("  对上 %-24s 拒绝在 %s，理由含「%s」（退出码 2）"
                  % (os.path.basename(path), line, why))

    # ---- 4 量尺：编译产物 vs 参考实现（同一个用例）
    if bins:
        shim = os.path.join(tmp, "rssshim")
        cc = subprocess.run(["cc", "-O2", "-o", shim, os.path.join(ROOT, "tools", "rssshim.c")],
                            capture_output=True, text=True)
        if cc.returncode != 0:
            red("rssshim 编不出来：%s" % cc.stderr.strip()[:120])
            shim = None
        if shim:
            src, out, _ = bins[0]
            t_code, kb_code = measure([out], REPS, shim)
            t_py, kb_py = measure([PY, INTERP, src], REPS, shim)
            t_true, kb_true = measure(["/bin/true"], REPS, shim)
            print("  量尺（中位 %d 次，fork+exec+wait4；RSS 由 tools/rssshim.c 量，它自己的地板 /bin/true = %.2f ms / %d KB）"
                  % (REPS, t_true, kb_true))
            print("    %-24s %7.2f ms  %6d KB  %6d B"
                  % ("编译产物 " + os.path.basename(src), t_code, kb_code, os.path.getsize(out)))
            print("    %-24s %7.2f ms  %6d KB" % ("参考实现 " + os.path.basename(src), t_py, kb_py))
            print("    差：启动 %.0f× / 常驻 %.0f×（口径见 docs/BUILD.md §3 与 §0 第一条）"
                  % (t_py / t_code, kb_py / max(kb_code, 1)))

    print("codegen: %s" % ("绿" if not bad else "红（%d 处）" % len(bad)))
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
