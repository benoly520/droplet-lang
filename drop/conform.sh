#!/bin/sh
# 两个宿主，一份定义：这份脚本比的不是谁快，而是「同一句话两个宿主是不是说的一样」。
# 第二个宿主（hosts/csharp/interpreter.cs）是按 spec.txt 从零写的，写它的人没看过
# hosts/python/interpreter.py，也没看过 lib/ 和 check.sh —— 这份脚本自己不住在任何一间宿主里：
# 它比的是两间，所以它和 check.sh 同级，是第二道门。
# —— 所以这里每一条相等都不是「抄得像」，而是「定义真把那件事说清了」；每一条不等都是一张借条。
#
#   [1/4] 语料     examples/*.dr + tests/*.dr（服务器那份除外）：stdout 逐字节、退出码
#   [2/4] 报错     跨行表（tests/layout/x_*.dr）+ 第二宿主自己写的样本（hosts/csharp/probe/*.dr，拿不到
#                  真 socket 的那几个除外）：比退出码、比 stdout、比「第 N 行 Ex」；
#                  两边的中文说明本来就不必相同，比的是码和红在哪一行
#   [3/4] 实录     同一份 lib/http.dr + examples/http.dr，两个宿主各起一次服务器，
#                  对话都必须逐字节等于 tests/http.expected
#   [4/4] 耗时     同一批程序各跑三遍取最快 —— 只报数，不作判据
#
# 用法：sh drop/conform.sh             没有 dotnet 就明说「这段没跑」，不装绿
# 四段都跑完再算账：第 2 段红的时候第 3 段的证据照样要看 —— 少一段不是宽松，是少一段证据。
FAILS=""
fail() { FAILS="$FAILS $1"; echo "  （第 $1 段红）"; }
HERE=$(cd "$(dirname "$0")" && pwd)          # = drop/
cd "$HERE"
CS="$HERE/hosts/csharp"                 # 第二宿主那一间
PY=${PYTHON:-python3}
DLL="$CS/bin/Release/net8.0/droplet.dll"

if ! command -v dotnet >/dev/null 2>&1; then
  echo "hosts: 这段没有跑 —— 本机没有 dotnet，第二宿主起不来（这不是绿，是没测）"
  exit 0
fi
if [ ! -f "$DLL" ]; then
  (cd "$CS" && DOTNET_CLI_TELEMETRY_OPTOUT=1 DOTNET_NOLOGO=1 TMPDIR="${TMPDIR:-/tmp}" \
      dotnet build -c Release --nologo >"$CS/build.log" 2>&1) || {
    echo "红：第二宿主构建不起来（看 hosts/csharp/build.log 最后几行）"; tail -5 "$CS/build.log"; exit 1; }
fi

T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
echo "$DLL" > "$T/dll"

echo "[1/4] 语料：同一份程序，两个宿主的 stdout 必须逐字节相同"
$PY - "$T" <<'PY' || fail 1
import glob, os, subprocess, sys
DLL = open(os.path.join(sys.argv[1], "dll"), encoding="utf-8").read().strip()
cs = ["dotnet", DLL]
files = [f for f in sorted(glob.glob("examples/*.dr")) + sorted(glob.glob("tests/*.dr"))
         if f != "examples/http.dr"]
bad = 0
for f in files:
    a = subprocess.run([sys.executable, "hosts/python/interpreter.py", f], capture_output=True, text=True)
    b = subprocess.run(cs + [f], capture_output=True, text=True)
    if (a.returncode, a.stdout) != (b.returncode, b.stdout):
        bad += 1
        print("  漂 %s：rc py=%d cs=%d" % (f, a.returncode, b.returncode))
        print("    py %r" % (a.stdout or a.stderr)[:150])
        print("    cs %r" % (b.stdout or b.stderr)[:150])
    else:
        print("  同 %s（%d 行输出，rc=%d）" % (f, a.stdout.count(chr(10)), a.returncode))
print("  语料：%d 个程序，%d 处漂" % (len(files), bad))
sys.exit(1 if bad else 0)
PY

echo "[2/4] 报错：跨行表 + 第二宿主自己的样本 + 边界形状，比 rc、比 stdout、比「第 N 行 Ex」"
$PY - "$T" <<'PY' || fail 2
import glob, os, re, subprocess, sys
DLL = open(os.path.join(sys.argv[1], "dll"), encoding="utf-8").read().strip()
cs = ["dotnet", DLL]

def shot(cmd, f):
    r = subprocess.run(cmd + [f], capture_output=True, text=True)
    m = re.search(r"第 (\d+) 行 (E\d+)", r.stderr)
    if "Traceback" in r.stderr:
        return (r.returncode, "?", "?", "吐了宿主堆栈")
    return (r.returncode, m.group(1) if m else "-", m.group(2) if m else "-", r.stdout)

# 已登记的漂移（定义没说了算、两边各自挑了一种读法的地方）。
# 一行一条：文件名 py=<rc/行/码/输出> cs=<...> 为什么。新漂移一律红；
# 登记过但已经不漂了也红 —— 那张借条得由人划掉，不许悄悄过期。
known = {}
if os.path.exists("conform.known"):
    for ln in open("conform.known", encoding="utf-8"):
        ln = ln.strip()
        if ln and not ln.startswith("//"):
            known[ln.split()[0]] = ln

cases = sorted(glob.glob("tests/layout/x_*.dr"))
cases += [f for f in sorted(glob.glob("hosts/csharp/probe/*.dr")) if not os.path.basename(f).startswith("sock_")]
cases += sorted(glob.glob("hosts/csharp/probe/lay2/s*.dr"))
bad = 0
coded = 0
for f in cases:
    a = shot([sys.executable, "hosts/python/interpreter.py"], f)
    b = shot(cs, f)
    if a != b:
        bad += 1
        tag = "%s py=%s cs=%s" % (f, a, b)
        if f not in known:
            print("  新漂 %s" % f)
        else:
            print("  已登记 %s" % f)
        print("    %s" % (tag,))
    else:
        if f in known:
            bad += 1
            print("  借条过期 %s —— 两边已经一样了，把 conform.known 里那条划掉" % f)
        if a[2] != "-":
            coded += 1
print("  报错样本：%d 个（两边都在同一行报同一个码的 %d 个），%d 处漂，登记在册 %d 条"
      % (len(cases), coded, bad, len(known)))
sys.exit(1 if bad else 0)
PY

echo "[3/4] 实录：同一份库和程序，两个宿主对客户端说的字节必须一样"
PORT=$((8611 + $$ % 200))
if ! DROP_HOST="dotnet $DLL" $PY tests/http_check.py "$PORT" > "$T/cs.out" 2>"$T/cs.err"; then
  echo "  红：第二宿主的服务器连实录都没打完（看下面）"
  tail -4 "$T/cs.err"
  fail 3
else
$PY - "$T/cs.out" tests/http.expected <<'CMP' || fail 3
import re, sys
a = open(sys.argv[1], encoding="utf-8").read().splitlines()
b = open(sys.argv[2], encoding="utf-8").read().splitlines()
RE = re.compile(r"挂：第 (\d+) 行 (E\d+)")
bad = 0
for n in range(max(len(a), len(b))):
    x = a[n] if n < len(a) else "<没有>"
    y = b[n] if n < len(b) else "<没有>"
    if x == y:
        continue
    mx, my = RE.search(x), RE.search(y)
    # 报错那几行只比「第几行 + 哪个码」：两边的中文说明本来就不必相同（定义只管码，
    # 说明是宿主写的）。但红在哪一行、报哪个码是定义管的 —— 那一行不许漂。
    if mx and my and x.startswith("  [服务器的 stderr]") and y.startswith("  [服务器的 stderr]"):
        if mx.groups() == my.groups():
            continue
        bad += 1
        print("    第 %d 行：cs 报 %s / 期望 %s（说明可以不同，行和码不行）"
              % (n + 1, " ".join(mx.groups()), " ".join(my.groups())))
        continue
    bad += 1
    print("    第 %d 行：cs %r / 期望 %r" % (n + 1, x[:120], y[:120]))
if bad:
    print("  红：第二宿主的对话和 tests/http.expected 对不上（%d 处）" % bad)
    sys.exit(1)
print("  同：%d 行对话逐字对上（报错那 %d 行比的是行号 + 码号，不比中文说明）"
      % (len(b), sum(1 for x in b if "服务器的 stderr" in x)))
CMP
fi

echo "[4/4] 耗时：同一批程序各跑三遍取最快（只报数，不作判据）"
$PY - "$T" <<'PY'
import glob, os, subprocess, sys, time
DLL = open(os.path.join(sys.argv[1], "dll"), encoding="utf-8").read().strip()
cs = ["dotnet", DLL]
files = [f for f in sorted(glob.glob("examples/*.dr")) + sorted(glob.glob("tests/*.dr"))
         if f != "examples/http.dr"]

def best3(cmd, args):
    xs = []
    for _ in range(3):
        t0 = time.time()
        subprocess.run(cmd + args, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
        xs.append((time.time() - t0) * 1000)
    return min(xs)

spec = [os.path.join("examples", "spec.dr")]
py = [sys.executable, "hosts/python/interpreter.py"]
p_all, c_all = best3(py, files), best3(cs, files)
p_one, c_one = best3(py, spec), best3(cs, spec)
print("  %d 个程序连跑一遍取最快：python3 %.0f ms / dotnet %.0f ms（每个进程启动摊 %.1f / %.1f ms）"
      % (len(files), p_all, c_all, p_all / len(files), c_all / len(files)))
print("  单跑 spec.dr（定义里那 24 行，最重的是 count(50000)）：python3 %.0f ms / dotnet %.0f ms"
      % (p_one, c_one))
# 这里不跑 hosts/csharp/probe/e13tail.dr（100000 层尾调用）：那一份两边都要一两分钟，是 O(n^2) 的
# 表拼接（acc + [n] 与 xs[1:]），不是这一段的判据。量过一次记在下面，别让它当这段的标签。
print("  没跑的那份：hosts/csharp/probe/e13tail.dr（100000 层尾调用 + 100000 长的表切片）——"
      "量过一次：python3 84 s / dotnet 124 s，两边都慢在同一件事上，只记数不判")
PY

if [ -n "$FAILS" ]; then
  echo "hosts: 红（红在第$FAILS 段）—— 上面每条漂移都要么修掉，要么写明定义没说了算；登记在册的借条不算绿"
  exit 1
fi
echo "hosts: 绿（两份宿主在语料、报错、实录这三份东西上说的是同一套话）"
