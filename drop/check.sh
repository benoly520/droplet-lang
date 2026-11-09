#!/bin/sh
# drop 的门。这个目录不引用外面任何东西 —— 语言的全部就是这几样，按层各住一处：
#   spec.txt 定义（唯一） / lib/ 用这门语言写的库 / examples/ + tests/ 程序与语料 /
#   两份宿主一边一间：py/drop.py（第一宿主，参考实现）与 cs/Drop.cs（第二宿主，干净房间写的）。
#   v0.42 之前第一宿主平在这个目录顶上，第二宿主一进来就变成"一个在根、一个在子目录"，
#   读树的人会以为 cs/ 是附加品而不是第二份宿主；现在两间对称，跑法只差一个解释器。
#   [1/5] 例子     examples/*.dr 与 tests/*.dr（冷读读者写的程序，v0.40 改写过）的实际输出必须等于 *.out
#                 examples/http.dr 这段跳过：它是服务器，第五段拿真 socket 打它
#   [2/5] 对账     spec.txt 的错误码 = py/drop.py 会吐的错误码；实现表面每条都有定义原话对着
#                 且每条码都有一个真能触发它的样本；宿主命令表 = 定义说的那七个（双向比）
#   [3/5] 预算     spec.txt 是全部定义：超预算 = 定义在膨胀，不是能力在增长
#                 顺带两条分界：定义和语言实现里都不许出现协议词汇 —— HTTP 是库的事；
#                 定义里也不许出现任何具体宿主的名字 —— 跑法那两句必须对第二个宿主同样成立
#   [4/5] 定义即可执行  从 spec.txt 重新抽出 > 行，必须与 examples/spec.dr 逐字相同，且跑出 *.out
#   [5/5] 服务器  起真进程 + 裸 socket 打 lib/http.dr + examples/http.dr，对话必须逐字等于
#                 tests/http.expected；顺带查库只碰那七个宿主命令（它不许偷别的外面）
#                 （预算的来由：v0.38 第一版定义 497 token，冷读测试写出 3 份能跑的程序的读者
#                  同时交回 25 条「靠猜」—— 补完这 25 条后 1,100 上下。守的是「一遍读得完 +
#                  猜的次数为 0」，不是某个好看的数。v0.40 把它抬到 1,750 只有一次原因：定义
#                  多背了整个服务器协议；v0.41 把协议搬进库，预算当场退回 1,500，一句放宽没留。）
#
# 破坏测试的纪律（本轮踩过一次才补上的）：一条破坏的「红」必须归因到它声称的那个原因。
# Python 崩栈、找不到文件、sed 没落到目标上 —— 这些也是非零退出码，看起来和抓住一模一样，其实是假绿。
# 所以每条破坏都先断言「破坏真的落到被改的那一处」，再断言「门因为这一处红了」。
set -e
cd "$(dirname "$0")"
export BUDGET_TOK=1500
export BUDGET_BYTES=10240
PY=${PYTHON:-python3}
# 破坏测试和被起的服务器都往这里写；开头就建，好让 trap 一眼看得见全部临时文件
T=$(mktemp -d)
T_live="$T/live"
# 宿主在 py/ 里，临时树也得有这一层，不然 cp 的目标路径会自己变成红的原因
mkdir -p "$T/py"
trap 'rm -rf "$T"' EXIT

# 破坏测试要一整棵跑得起来的门树。cs/ 只带 Drop.cs —— 第三段只读它，bin/obj 不必跟着拷，
# 不然每条破坏都多拷 1 MB，门的时间白花。
tree() { mkdir -p "$1/cs" "$1/py"; cp -R examples tests lib spec.txt check.sh "$1/"; cp py/drop.py "$1/py/"; cp cs/Drop.cs "$1/cs/"; }


# sh drop/check.sh --update  改完 spec.txt 后重抽 examples/spec.{dr,out}
if [ "$1" = "--update" ]; then
  $PY - <<'UP'
import subprocess, sys
lines = [l[2:] for l in open('spec.txt', encoding='utf-8').read().splitlines() if l.startswith('> ')]
src = "".join(l + "\n" for l in lines)
open('examples/spec.dr', 'w', encoding='utf-8').write(src)
r = subprocess.run([sys.executable, 'py/drop.py', 'examples/spec.dr'], capture_output=True, text=True)
if r.returncode != 0:
    sys.stderr.write("红：定义里的例子跑不通，先把定义改对再 --update\n%s" % r.stderr)
    sys.exit(1)
open('examples/spec.out', 'w', encoding='utf-8').write(r.stdout)
print("examples/spec.dr %d 行 / spec.out %d 行 已重抽" % (len(lines), r.stdout.count("\n")))
UP
  exit 0
fi

echo "[1/5] 例子（先查一条：这个目录不许指到外面 —— 干净房间是门，不是文档里的声明）"
# 独立性的可执行版本：这个目录里任何一句指到本仓库其它顶层目录（需求表、C 实现与检查、文档、
# 那套登记格式），就说明这门语言开始依赖外面的东西，「读这一页就会用」不成立了。
# 正则写成下面那个样子（每个词第一个字母放进单字符方括号）是为了不让这条检查匹配到自己的注释
# —— 不然"把检查的名字写进注释"都会让门红。Z11 负责证明这条真的会红。
if grep -rnE '(^|[^A-Za-z0-9_.-])([r]eq|[c]ode|[d]ocs|[l]ang)/|[.]dpt|[b]lobd|[S]PEC' \
       --include='*.py' --include='*.sh' --include='*.txt' --include='*.dr' \
       --include='*.out' --include='*.expected' --include='*.cs' --include='*.csproj' . ; then
  echo "  红：上面这些行引用了 drop/ 外面的东西 —— 干净房间塌了，独立性从此只能靠自觉"
  exit 1
fi
echo "  绿：0 处外部引用"
$PY - <<'PY'
import glob, os, subprocess, sys
bad = 0
for f in sorted(glob.glob('examples/*.dr')) + sorted(glob.glob('tests/*.dr')):
    if f == 'examples/spec.dr':
        continue                      # 第四段专管它
    if f == 'examples/http.dr':
        print("  跳过 examples/http.dr —— 它是服务器，第五段拿真 socket 打它")
        continue
    if not os.path.exists(f[:-3] + '.out'):
        # 期望输出是题面的一部分，不是编译产物；丢了就等于这段没跑
        bad += 1
        print("  缺 %s —— 期望输出不在仓库里，这段没有可比的东西" % (f[:-3] + '.out'))
        continue
    exp = open(f[:-3] + '.out', encoding='utf-8').read()
    r = subprocess.run([sys.executable, 'py/drop.py', f], capture_output=True, text=True)
    if r.returncode != 0 or r.stdout != exp:
        bad += 1
        print("  差 %s rc=%d" % (f, r.returncode))
        print("    期望 %r" % exp)
        print("    得到 %r %s" % (r.stdout, r.stderr.strip()))
    else:
        print("  对上 %s" % f)
if bad:
    sys.exit(1)
# 命令行本身也是表面：读不到文件、参数不对，都不许吐堆栈
import os, tempfile
def run(args):
    r = subprocess.run([sys.executable, 'py/drop.py'] + args, capture_output=True, text=True)
    return r.returncode, r.stdout, r.stderr
for args, want in [([], 2), (['/tmp/不存在.dr'], 1)]:
    rc, out, err = run(args)
    if rc != want or "Traceback" in err:
        print("  差 CLI %r：rc=%d（要 %d）err=%r" % (args, rc, want, err[-120:]))
        bad += 1
    else:
        print("  对上 CLI %r -> rc=%d，一行说明，无堆栈" % (args, rc))
# 跨行写法：定义说「一行一句，换行是唯一的语句分隔符」，所以一句话不许在没闭合的括号里续行。
# 这一张表钉的是八个真会跨行的写法各红在哪一行，以及两个合法写法必须照样跑。
# 第二个宿主（cs/）拿同一张表比 —— 这条在两份实现之间是最容易漂的，光靠文档说不清。
import re
lay = 'tests/layout.expected'
if not os.path.exists(lay):
    print("  缺 %s —— 跨行写法没有对账表，这段等于没跑" % lay)
    bad += 1
else:
    for row in open(lay, encoding='utf-8').read().splitlines():
        who, ln, code, rcw = row.split()
        f = 'tests/layout/%s.dr' % who
        r = subprocess.run([sys.executable, 'py/drop.py', f], capture_output=True, text=True)
        got = re.match(r"挂：第 \d+ 行 (E\d+)", r.stderr)
        want_rc = int(rcw.split('=')[1])
        if r.returncode != want_rc or not got or got.group(1) != code or "Traceback" in r.stderr:
            print("  差 %s：rc=%d（要 %d）err=%r" % (f, r.returncode, want_rc, r.stderr[:90]))
            bad += 1
        elif "第 %d 行" % int(ln) not in r.stderr:
            print("  差 %s：码对了但行的归属漂了（要 %s，得到 %r）" % (f, ln, r.stderr[:60]))
            bad += 1
        else:
            print("  对上 跨行写法 %s -> %s %s" % (who, ln, code))
for f, want in [('tests/layout_ok_1.dr', 'a\n空\n'), ('tests/layout_ok_2.dr', '2\n')]:
    r = subprocess.run([sys.executable, 'py/drop.py', f], capture_output=True, text=True)
    if r.returncode != 0 or r.stdout != want:
        print("  差 %s：合法写法被拦了 rc=%d out=%r" % (f, r.returncode, r.stdout))
        bad += 1
    else:
        print("  对上 合法写法 %s" % f)
sys.exit(1 if bad else 0)
PY

echo "[2/5] 定义 <-> 实现对账 + 码的活性"
$PY py/drop.py --self-check
$PY py/drop.py --self-test

echo "[3/5] 定义预算 + 两道分界（协议词汇不许长在定义和实现里，具体宿主名不许长在定义里）"
$PY - <<'PY'
import os, sys
s = open('spec.txt', encoding='utf-8').read()
asc = sum(1 for c in s if ord(c) < 128)
tok = asc // 4 + (len(s) - asc) // 3
b = len(s.encode('utf-8'))
lim_t, lim_b = int(os.environ['BUDGET_TOK']), int(os.environ['BUDGET_BYTES'])
print("  spec.txt %d B / %d token(估算) / %d 行  预算 %d / %d -> 占用 %.1f%% / %.1f%%"
      % (b, tok, len(s.splitlines()), lim_t, lim_b, 100.0 * tok / lim_t, 100.0 * b / lim_b))
if tok > lim_t or b > lim_b:
    print("  红：定义超出预算 —— 「读完这一份就会用」这个提法的成立条件没了")
    sys.exit(1)
# 分层不是文档里的一句话：定义和语言实现里出现协议词汇，就是 HTTP 又长回语言里了。
# 「请求」这一句是例外 —— 它只出现在「语言不认识「请求」」那句否定里，所以数一次。
WORDS = ["HTTP", "Content-Length", "Content-Type", "Date:", "keep-alive", "CRLF",
         "状态行", "状态码", "响应", "首部", "正文", "路由", "GET", "POST", "HEAD"]
in_spec = [w for w in WORDS if w in s]
in_py = [w for w in WORDS if w in open('py/drop.py', encoding='utf-8').read()]
if s.count("请求") != 1 or in_spec or in_py:
    print("  红：分界塌了 —— 定义里 %r，实现里 %r，「请求」出现 %d 次（只许在被否定的那一句里）"
          % (in_spec, in_py, s.count("请求")))
    sys.exit(1)
lib = open('lib/http.dr', encoding='utf-8').read()
n_lib = len([w for w in WORDS if w in lib])
if n_lib < 4:
    print("  红：这一条查不出东西 —— 协议词汇一个都不在 lib/http.dr 里，说明上面那两条是空的")
    sys.exit(1)
# 第二道分界：定义不许写死宿主。跑法那一句、不许吐堆栈那一句，都必须对任何一个宿主同时成立 ——
# 定义里一旦留下某个具体宿主的名字，「同一份定义可以有第二个宿主」就只是文档里的一句话。
# 注意这一条只查 spec.txt：py/drop.py 本身就是一个宿主，它说「我用 python3 跑」是诚实，不是越界。
HOSTS = ["python", "Python", "dotnet", "C#", "node", "Ruby", "Lua", "JVM", "gcc", "clang"]
in_host = [w for w in HOSTS if w in s]
if in_host:
    print("  红：宿主分界塌了 —— 定义里出现了具体宿主的名字 %r，跑法与报错那两句必须对任何宿主都成立"
          % in_host)
    sys.exit(1)
# 分界对两份宿主都要成立：第二个宿主里长出协议词汇，等于把库的代码搬回语言里。
if not os.path.exists('cs/Drop.cs'):
    print("  红：第二个宿主 cs/Drop.cs 不在这个仓库里 —— 「同一份定义可以有第二个宿主」没人举证了")
    sys.exit(1)
in_cs = [w for w in WORDS if w in open('cs/Drop.cs', encoding='utf-8').read()]
if in_cs:
    print("  红：第二个宿主伸出了协议词汇 %r —— 分层不是只改第一份实现就能算数的" % in_cs)
    sys.exit(1)
print("  绿：定义 %d / %d token 之内；协议词汇 0 处（定义里 %d、两份宿主里 %d/%d），全在库里那 %d 处；"
      "宿主名 0 处" % (tok, lim_t, len(in_spec), len(in_py), len(in_cs), n_lib))
PY

echo "[4/5] 定义即可执行（> 行 = 抽得出、跑得起、对得上）"
$PY - <<'PY'
import os, subprocess, sys
lines = [l[2:] for l in open('spec.txt', encoding='utf-8').read().splitlines() if l.startswith('> ')]
mine = "".join(l + "\n" for l in lines)
try:
    committed = open('examples/spec.dr', encoding='utf-8').read()
except IOError:
    committed = ""
if mine != committed:
    print("  红：定义里的 > 行与 examples/spec.dr 不一致 —— 有人改了代码例子却没改定义（或反了）")
    a, b = committed.splitlines(), lines
    for n in range(max(len(a), len(b))):
        x = a[n] if n < len(a) else "<没有>"
        y = b[n] if n < len(b) else "<没有>"
        if x != y:
            print("    第 %d 行：定义 %r / 例子 %r" % (n + 1, y, x))
    sys.exit(1)
if not os.path.exists('examples/spec.out'):
    print("  红：examples/spec.out 不在仓库里 —— 期望输出是题面的一部分，不是编译产物")
    sys.exit(1)
exp = open('examples/spec.out', encoding='utf-8').read()
r = subprocess.run([sys.executable, 'py/drop.py', 'examples/spec.dr'], capture_output=True, text=True)
if r.returncode != 0 or r.stdout != exp:
    print("  红：定义里的例子跑出来不是那个数")
    print("    rc=%d %s" % (r.returncode, r.stderr.strip()))
    print("    期望 %r" % exp)
    print("    得到 %r" % r.stdout)
    sys.exit(1)
print("  绿：从定义抽出 %d 行真代码，逐字对得上，跑出来 %d 行输出全对" % (len(lines), exp.count(chr(10))))
PY

echo "[5/5] 服务器（拿真 socket 打 lib/http.dr + examples/http.dr，顺带查库只碰那七个宿主命令）"
# 库是 drop 写的，不在解释器里，所以它能伸到外面去的那只手必须点名查：
# host("命令" 出现过的命令必须 ⊆ {bind accept recv send peer close date}。
# 第八个命令一旦从库里长出来，「语言只有这一个口子」这句话就又是假的。
$PY - <<'PY'
import re, sys
lib = open('lib/http.dr', encoding='utf-8').read()
calls = re.findall(r'host\(\s*("(?:[a-z]+)"|[A-Za-z_]\w*)', lib)
used = sorted(set(c for c in calls if c.startswith('"')))
loose = [c for c in calls if not c.startswith('"')]       # 命令名藏在变量里 = 这条扫不到东西
allow = {"bind", "accept", "recv", "send", "peer", "close", "date"}
extra = [u.strip('"') for u in used if u.strip('"') not in allow]
if not used or loose:
    print("  红：这一条查不出东西 —— 库里 %d 次 host 调用，命令名全是变量的有 %r（剩下 %r）"
          % (len(calls), loose, used))
    sys.exit(1)
if extra:
    print("  红：库里伸出了第八个宿主命令 %r —— 「只有 host 这一个口子」不再成立（它用掉的是 %r）"
          % (extra, sorted(u.strip('"') for u in used)))
    sys.exit(1)
print("  绿：库 %d 次 host 调用，命令名全是写死的字面量，只用那七个（用掉 %d 个：%s）"
      % (len(calls), len(used), " ".join(sorted(u.strip('"') for u in used))))
PY
PORT=$((8123 + $$ % 300))
if ! $PY tests/http_check.py "$PORT" > "$T_live.out" 2>"$T_live.err"; then
  echo "  红：服务器脚本自己退了（看 stderr 最后几行）"
  tail -5 "$T_live.err"
  rm -f "$T_live.out" "$T_live.err"
  exit 1
fi
if [ ! -f tests/http.expected ]; then
  echo "  红：tests/http.expected 不在仓库里 —— 这段没有可比的东西"
  rm -f "$T_live.out" "$T_live.err"
  exit 1
fi
if ! cmp -s "$T_live.out" tests/http.expected; then
  echo "  红：服务器的实际对话和期望输出不一样"
  $PY - "$T_live.out" tests/http.expected <<CMP
import sys
a = open(sys.argv[2], encoding="utf-8").read().splitlines()
b = open(sys.argv[1], encoding="utf-8").read().splitlines()
for n in range(max(len(a), len(b))):
    x = a[n] if n < len(a) else "<没有>"
    y = b[n] if n < len(b) else "<没有>"
    if x != y:
        print("    第 %d 行：期望 %r / 得到 %r" % (n + 1, x, y))
CMP
  rm -f "$T_live.out" "$T_live.err"
  exit 1
fi
echo "  绿：$(wc -l < tests/http.expected) 行对话逐字对上 —— GET / 404 / HEAD 只回头 / 405 /"
echo "      POST 字节原样回来 / keep-alive 第二个请求 / Connection: close / LF-only 断 /"
echo "      超上限断（不报错）/ 程序里红 = 整个服务器红 / 程序自己编句柄 = E14 /"
echo "      程序交回的东西不对 = 红在库里那一行（分层分对了的证据）"
rm -f "$T_live.out" "$T_live.err"

echo "破坏测试（门自己会不会失效）"
cp py/drop.py "$T/py/" && cp spec.txt "$T/"
mkdir -p "$T/examples" && cp examples/spec.dr examples/spec.out "$T/examples/"

# Z1 从定义里抹掉一条错误码 -> 对账必须红
sed -i '/E11/d' "$T/spec.txt"
if $PY "$T/py/drop.py" --self-check --spec "$T/spec.txt" >/dev/null 2>&1; then
  echo "  假绿 Z1：定义少了 E11，对账却没红"; exit 1
fi
echo "  抓住 Z1：定义少一条码 -> 对账红"
cp spec.txt "$T/spec.txt"

# Z2 把实现里一条码的文案改掉 -> 对账必须红（不许两边各说各话）
sed -i 's/"E6": "下标越界"/"E6": "下标跑出去了"/' "$T/py/drop.py"
if $PY "$T/py/drop.py" --self-check --spec "$T/spec.txt" >/dev/null 2>&1; then
  echo "  假绿 Z2：实现改了说法，对账却没红"; exit 1
fi
echo "  抓住 Z2：实现与定义说得不一样 -> 对账红"
cp py/drop.py "$T/py/drop.py"

# Z3 除零改报成别的码 -> 活性测试必须红（它验的是码号，不只是红不红）
sed -i 's/raise DropError("E8", line, "%s %s/raise DropError("E4", line, "%s %s/' "$T/py/drop.py"
if $PY "$T/py/drop.py" --self-test >/dev/null 2>&1; then
  echo "  假绿 Z3：E8 的检查改报成 E4，活性测试却没红"; exit 1
fi
echo "  抓住 Z3：有条码不再按自己报 -> 活性测试红"
cp py/drop.py "$T/py/drop.py"

# Z4 把定义里的一句规则删掉（实现照旧）-> 表面探针必须红
sed -i 's|切片：左含右不含、两端可省；越界不报错|切片端点另说|' "$T/spec.txt"
if cmp -s spec.txt "$T/spec.txt"; then
  echo "  假绿 Z4：破坏没落到定义上，先修这条 sed"; exit 1
fi
if $PY "$T/py/drop.py" --self-check --spec "$T/spec.txt" >/dev/null 2>&1; then
  echo "  假绿 Z4：定义删了一条规则，探针却没红"; exit 1
fi
echo "  抓住 Z4：定义删掉一条规则 -> 表面探针红"

# Z5 定义里的例子改坏一行（除零）而输出没改 -> 第四段必须红
sed -i 's|^> print(str(fact(10)))$|> print(str(1/0))|' "$T/spec.txt"
$PY - "$T" <<'PY'
import re, subprocess, sys
t = sys.argv[1]
lines = [l[2:] for l in open(t + '/spec.txt', encoding='utf-8').read().splitlines() if l.startswith('> ')]
open(t + '/examples/spec.dr', 'w', encoding='utf-8').write("".join(l + "\n" for l in lines))
PY
if $PY "$T/py/drop.py" "$T/examples/spec.dr" >/dev/null 2>&1; then
  echo "  假绿 Z5：定义里的例子已经跑不通，却没红"; exit 1
fi
echo "  抓住 Z5：定义里的例子跑不通 -> 红"

# Z6 把 print 打表的规则改回「不套括号」（本轮认下的真缺陷）-> 例子必须立刻对不上
sed -i 's@",".join(quote(x) if isinstance(x, str) else show(x) for x in v)@",".join(show(x) for x in v)@' "$T/py/drop.py"
if cmp -s py/drop.py "$T/py/drop.py"; then
  echo "  假绿 Z6：破坏用的 sed 没落到实现上，先修这条破坏"; exit 1
fi
if $PY - "$T/py/drop.py" <<'PY'
import glob, os, subprocess, sys
fs = [f for f in sorted(glob.glob('examples/*.dr')) + sorted(glob.glob('tests/*.dr'))
      if f not in ('examples/spec.dr', 'examples/http.dr')]   # http 归第五段管
same = 0
for f in fs:
    if not os.path.exists(f[:-3] + '.out'):
        print('  Z6 自己没有期望输出可比：%s' % f)
        sys.exit(1)
    r = subprocess.run([sys.executable, sys.argv[1], f], capture_output=True, text=True)
    if r.stdout == open(f[:-3] + '.out', encoding='utf-8').read():
        same += 1
sys.exit(0 if same == len(fs) else 1)     # 全对上 = 这条破坏看不见 = 门失效
PY
then
  echo "  假绿 Z6：print 打表改了规则，%d 份例子却全对上" $(ls examples/*.dr tests/*.dr | grep -Ev 'spec\.dr|http\.dr' | wc -l); exit 1
fi
echo "  抓住 Z6：print 打表的规则被改回去 -> 例子立刻对不上"
cp py/drop.py "$T/py/drop.py"

# Z7 把所有 *.out 拿掉（本轮真踩过：仓库的 .gitignore 里 *.out 把期望输出吞了，
#    本地全绿、干净克隆里门却根本没跑）-> 门必须红，而且必须说人话，不许吐堆栈
T7="$T/z7"
mkdir -p "$T7"
tree "$T7"
rm -f "$T7"/*/*.out
if (cd "$T7" && sh check.sh >"$T7/z7.log" 2>&1); then
  echo "  假绿 Z7：期望输出全没了，门却还是绿的"; exit 1
fi
if grep -q "Traceback" "$T7/z7.log"; then
  echo "  假绿 Z7：期望输出全没了，门红是红了，吐的是 Python 堆栈"; exit 1
fi
if ! grep -q "期望输出" "$T7/z7.log"; then
  echo "  假绿 Z7：门红了，但没说红在缺期望输出"; exit 1
fi
echo "  抓住 Z7：期望输出被藏起来 -> 门红，且报的是「缺哪个文件」"

# Z8 把「替你循环」那五个名字放回可调用（SUGAR 清空）-> 活性测试必须红：
#    定义说这些名字占了是 E3，放开后那几个样本就不红了
sed -i 's/^SUGAR = (.*)$/SUGAR = ()/' "$T/py/drop.py"
if cmp -s py/drop.py "$T/py/drop.py"; then
  echo "  假绿 Z8：破坏没落到实现上，先修这条 sed"; exit 1
fi
if $PY "$T/py/drop.py" --self-test >/dev/null 2>&1; then
  echo "  假绿 Z8：sugar 名字放回表面了，活性测试却没红"; exit 1
fi
echo "  抓住 Z8：把 map/filter/fold/range/sort 放回可调用 -> 活性测试红"
cp py/drop.py "$T/py/drop.py"

# Z9 把 print 的「只收字符串」改回悄悄转换 -> 活性测试必须红（E10 的样本不红了）
$PY - "$T/py/drop.py" <<'ZPY'
import sys
p = sys.argv[1]
s = open(p, encoding="utf-8").read()
old = """    v = args[0]
    if not isinstance(v, str):
        raise DropError("E10", line,
                        "print 只吃字符串，给的是 %s —— 要转文本就自己写 str(...)" % kind(v))
    print(v); return None"""
new = """    v = args[0]
    print(v if isinstance(v, str) else show(v)); return None"""
if not (s.count(old) == 1): sys.exit("Z9 的锚点没了")
open(p, "w", encoding="utf-8").write(s.replace(old, new))
ZPY
if [ $? -ne 0 ]; then echo "  红：这条破坏的补丁没打上（原因就在上面那行），破坏测试没跑成"; exit 1; fi
if $PY "$T/py/drop.py" --self-test >/dev/null 2>&1; then
  echo "  假绿 Z9：print 又能悄悄转文本了，活性测试却没红"; exit 1
fi
echo "  抓住 Z9：print 恢复隐式转换 -> 活性测试红"
cp py/drop.py "$T/py/drop.py"

# Z10 让 bind 在没给 --port 时自己挑个端口 -> 活性测试必须红：那条 E14 样本就是钉这一句的
#     （端口只能从命令行来；实现里悄悄给个默认值，「宿主起来时给的那个端口」这句话就又是假的）
$PY - "$T/py/drop.py" <<'YPY'
import os, sys
p = sys.argv[1]
s = open(p, encoding="utf-8").read()
old = """    port = HOST["port"]
    if port is None:"""
new = """    port = HOST["port"] or 0
    if False:"""
if not (s.count(old) == 1): sys.exit("Z10 的锚点没了")
open(p, "w", encoding="utf-8").write(s.replace(old, new))
YPY
if [ $? -ne 0 ]; then echo "  红：这条破坏的补丁没打上（原因就在上面那行），破坏测试没跑成"; exit 1; fi
if cmp -s py/drop.py "$T/py/drop.py"; then
  echo "  假绿 Z10：破坏没落到实现上，先修这条破坏"; exit 1
fi
if $PY "$T/py/drop.py" --self-test >"$T/z10.log" 2>&1; then
  echo "  假绿 Z10：bind 在没 --port 时自己挑了端口，活性测试却没意见"; exit 1
fi
if grep -q "Traceback" "$T/z10.log"; then
  echo "  假绿 Z10：红了，但吐的是 Python 堆栈 —— 崩掉也算红的话，破坏测试就没意义了"; exit 1
fi
if ! grep -q "E14 的样本居然不报" "$T/z10.log"; then
  echo "  假绿 Z10：门红了，但没说红在「host(\"bind\", \"\") 不报 E14」上"; exit 1
fi
echo "  抓住 Z10：bind 在没 --port 时自己挑端口 -> 活性测试红（端口只有命令行一个来源）"
cp py/drop.py "$T/py/drop.py"

# Z11 往定义里塞一句「去本目录外面看」（本轮真发生过：实现的一句注释里写了仓库别处的目录名，
#    而门里当时只有文档中手写的那一句 grep，没有任何东西会红）-> 第一段必须立刻红。
T11="$T/z11"
mkdir -p "$T11"
tree "$T11"
$PY - "$T11" <<'ZPY'
import sys, os
p = os.path.join(sys.argv[1], "spec.txt")
s = open(p, encoding="utf-8").read()
open(p, "w", encoding="utf-8").write(s + "// 端口的事另见 " + "req" + "/SRC.md 第 3 条\n")
ZPY
if [ $? -ne 0 ]; then echo "  红：这条破坏的补丁没打上（原因就在上面那行），破坏测试没跑成"; exit 1; fi
if ! cmp -s spec.txt "$T11/spec.txt"; then :; else echo "  假绿 Z11：破坏没落到定义上"; exit 1; fi
if (cd "$T11" && sh check.sh >"$T11/z11.log" 2>&1); then
  echo "  假绿 Z11：定义里多了一句指向本目录之外的引用，门却还是绿的"; exit 1
fi
if ! grep -q "外部\|干净房间" "$T11/z11.log"; then
  echo "  假绿 Z11：门红了，但不是红在那条独立性检查上（说明红的是别的东西，这条没测到）"; exit 1
fi
if grep -q "Traceback" "$T11/z11.log"; then
  echo "  假绿 Z11：红了，但吐的是 Python 堆栈 —— 崩掉也算红的话，破坏测试就没意义了"; exit 1
fi
rm -rf "$T11"
echo "  抓住 Z11：定义里塞一句指向外面的引用 -> 第一段当场红，且报的是独立性"

# Z12 往定义里塞一个协议词（v0.40 就是这么长的：HTTP 的规矩一条条写进定义，定义背下了整个协议）
#     -> 第三段必须红，且红在分界上，不是红在预算上（破坏特意选了一句长度差不多的，就是为了分开这两件事）
T12="$T/z12"
mkdir -p "$T12"
tree "$T12"
$PY - "$T12" <<'ZPY'
import os, sys
p = os.path.join(sys.argv[1], "spec.txt")
s = open(p, encoding="utf-8").read()
old = "语言不认识「请求」，那是用 drop 写的一个库的事"
new = "语言不认识「请求」，HTTP 是用 drop 写的一个库的事"
if not (s.count(old) == 1): sys.exit("Z12 的锚点没了")
t = s.replace(old, new)
asc = sum(1 for c in t if ord(c) < 128)
tok = asc // 4 + (len(t) - asc) // 3
# 破坏必须只撞分界这一条：撞进预算里，红就归因错了
if not (tok <= int(os.environ["BUDGET_TOK"])): sys.exit("Z12 会把定义顶出预算（%d token），换个不那么长的词" % tok)
open(p, "w", encoding="utf-8").write(t)
ZPY
if [ $? -ne 0 ]; then echo "  红：这条破坏的补丁没打上（原因就在上面那行），破坏测试没跑成"; exit 1; fi
if cmp -s spec.txt "$T12/spec.txt"; then
  echo "  假绿 Z12：破坏没落到定义上，先修这条破坏"; exit 1
fi
if (cd "$T12" && sh check.sh >"$T12/z12.log" 2>&1); then
  echo "  假绿 Z12：定义里冒出一个协议词，门却还是绿的 —— 分层只能靠自觉了"; exit 1
fi
if grep -q "Traceback" "$T12/z12.log"; then
  echo "  假绿 Z12：红了，但吐的是 Python 堆栈"; exit 1
fi
if ! grep -q "分界塌了" "$T12/z12.log"; then
  echo "  假绿 Z12：门红了，但没说红在分界上（红的是别的东西，这条没测到）"; exit 1
fi
rm -rf "$T12"
echo "  抓住 Z12：定义里塞进一个协议词 -> 第三段当场红，且报的是分界（不是预算）"

# Z13 让库伸出第八个宿主命令（口子一旦能随便加宽，「唯一的一个口子」就成了空话）
#     -> 第五段开头那条扫描必须红
T13="$T/z13"
mkdir -p "$T13"
tree "$T13"
$PY - "$T13" <<'ZPY'
import os, sys
p = os.path.join(sys.argv[1], "lib", "http.dr")
s = open(p, encoding="utf-8").read()
old = 'host("peer", c)'
new = 'host("getpeer", c)'
if not (s.count(old) >= 1): sys.exit("Z13 的锚点没了")
open(p, "w", encoding="utf-8").write(s.replace(old, new))
ZPY
if [ $? -ne 0 ]; then echo "  红：这条破坏的补丁没打上（原因就在上面那行），破坏测试没跑成"; exit 1; fi
if cmp -s lib/http.dr "$T13/lib/http.dr"; then
  echo "  假绿 Z13：破坏没落到库上，先修这条破坏"; exit 1
fi
if (cd "$T13" && sh check.sh >"$T13/z13.log" 2>&1); then
  echo "  假绿 Z13：库里多出第八个宿主命令，门却还是绿的"; exit 1
fi
if grep -q "Traceback" "$T13/z13.log"; then
  echo "  假绿 Z13：红了，但吐的是 Python 堆栈"; exit 1
fi
if ! grep -q "第八个宿主命令" "$T13/z13.log"; then
  echo "  假绿 Z13：门红了，但没说红在第八个宿主命令上（红的是别的东西，这条没测到）"; exit 1
fi
rm -rf "$T13"
echo "  抓住 Z13：库里伸出第八个宿主命令 -> 第五段当场红，且报的是那个命令的名字"

# Z14 往实现的宿主命令表里加第八个（Z13 查的是「库用没用」，这一条查的是「口子本身有没有被加宽」）
#     -> 第 2 段的对账必须红，而且必须报出多出来的那个名字
$PY - "$T/py/drop.py" <<'YPY'
import sys
p = sys.argv[1]
s = open(p, encoding="utf-8").read()
old = '"peer": _h_peer, "close": _h_close, "date": _h_date}'
new = '"peer": _h_peer, "close": _h_close, "date": _h_date, "file": _h_date}'
if not (s.count(old) == 1): sys.exit("Z14 的锚点没了")
open(p, "w", encoding="utf-8").write(s.replace(old, new))
YPY
if [ $? -ne 0 ]; then echo "  红：这条破坏的补丁没打上（原因就在上面那行），破坏测试没跑成"; exit 1; fi
if cmp -s py/drop.py "$T/py/drop.py"; then
  echo "  假绿 Z14：破坏没落到实现上，先修这条破坏"; exit 1
fi
if $PY "$T/py/drop.py" --self-check --spec "$T/spec.txt" >"$T/z14.log" 2>&1; then
  echo "  假绿 Z14：实现给口子加了第八个命令，对账却没意见"; exit 1
fi
if grep -q "Traceback" "$T/z14.log"; then
  echo "  假绿 Z14：红了，但吐的是 Python 堆栈"; exit 1
fi
if ! grep -q "宿主命令两边不是同一张表" "$T/z14.log"; then
  echo "  假绿 Z14：门红了，但没说红在宿主命令表上（红的是别的东西，这条没测到）"; exit 1
fi
echo "  抓住 Z14：实现的宿主命令表加宽到八个 -> 对账红（口子只有定义说的那七个）"
cp py/drop.py "$T/py/drop.py"

# Z15 往定义里塞回一个具体宿主的名字（v0.41 之前「跑法只有一条：python3 py/drop.py …」就是这么写的；
#     定义一旦写死宿主，「同一份定义可以有第二个宿主」就只剩文档里的一句话）
#     -> 第三段必须红，且红在宿主分界上，不是红在预算上，也不是红在表面探针上
#     破坏特意插在「宿主的解释器」前面，为的是保住那条探针原话 —— 不然红的会是第二段，归因就错了
T15="$T/z15"
mkdir -p "$T15"
tree "$T15"
$PY - "$T15" <<'ZPY'
import os, sys
p = os.path.join(sys.argv[1], "spec.txt")
s = open(p, encoding="utf-8").read()
old = "跑法只有一条：宿主的解释器"
new = "跑法只有一条（python3）：宿主的解释器"
if not (s.count(old) == 1): sys.exit("Z15 的锚点没了")
t = s.replace(old, new)
if not ("宿主的解释器 [--port <端口>] 库.dr 程序.dr" in t): sys.exit("Z15 会把表面探针撞掉，红会落到第二段")
asc = sum(1 for c in t if ord(c) < 128)
tok = asc // 4 + (len(t) - asc) // 3
if not (tok <= int(os.environ["BUDGET_TOK"])): sys.exit("Z15 会把定义顶出预算（%d token），换个不那么长的写法" % tok)
open(p, "w", encoding="utf-8").write(t)
ZPY
if [ $? -ne 0 ]; then echo "  红：这条破坏的补丁没打上（原因就在上面那行），破坏测试没跑成"; exit 1; fi
if cmp -s spec.txt "$T15/spec.txt"; then
  echo "  假绿 Z15：破坏没落到定义上，先修这条破坏"; exit 1
fi
if (cd "$T15" && sh check.sh >"$T15/z15.log" 2>&1); then
  echo "  假绿 Z15：定义里冒出具体宿主的名字，门却还是绿的 —— 第二个宿主没有立足点"; exit 1
fi
if grep -q "Traceback" "$T15/z15.log"; then
  echo "  假绿 Z15：红了，但吐的是 Python 堆栈"; exit 1
fi
if ! grep -q "宿主分界塌了" "$T15/z15.log"; then
  echo "  假绿 Z15：门红了，但没说红在宿主分界上（红的是别的东西，这条没测到）"; exit 1
fi
rm -rf "$T15"
echo "  抓住 Z15：定义里塞回一个具体宿主名 -> 第三段当场红，且报的是宿主分界（不是预算）"

# Z16 把「一句不许跨行」里管表字面量的那一处删掉（两个宿主最容易在这条上各判各的：
#     第一份实现当初就是按「括号里换行不算分隔」写的，第二份是按字面写的 —— 光靠文档说不清）
#     -> 第一段那张跨行表必须当场红，而且红在 x_list_comma 这一行
T16="$T/z16"
mkdir -p "$T16"
tree "$T16"
$PY - "$T16" <<'ZPY'
import os, sys
p = os.path.join(sys.argv[1], "py/drop.py")
s = open(p, encoding="utf-8").read()
old = 'p.take(); p.one_line("表字面量逗号后面换行了", line); continue'
new = 'p.take(); continue'
if not (s.count(old) == 1): sys.exit("Z16 的锚点没了")
open(p, "w", encoding="utf-8").write(s.replace(old, new))
ZPY
if [ $? -ne 0 ]; then echo "  红：这条破坏的补丁没打上（原因就在上面那行），破坏测试没跑成"; exit 1; fi
if cmp -s py/drop.py "$T16/py/drop.py"; then
  echo "  假绿 Z16：破坏没落到实现上，先修这条破坏"; exit 1
fi
if (cd "$T16" && sh check.sh >"$T16/z16.log" 2>&1); then
  echo "  假绿 Z16：跨行的表字面量又不红了，跨行表等于没跑"; exit 1
fi
if grep -q "Traceback" "$T16/z16.log"; then
  echo "  假绿 Z16：红了，但吐的是 Python 堆栈"; exit 1
fi
if ! grep -q "差 tests/layout/x_list_comma.dr" "$T16/z16.log"; then
  echo "  假绿 Z16：门红了，但没说红在 x_list_comma 上（红的是别的东西，这条没测到）"; exit 1
fi
rm -rf "$T16"
echo "  抓住 Z16：删掉一处「不许跨行」-> 第一段当场红，且红在跨行表的那一行上"

echo "drop: 绿（五段 + 十六条破坏）"
