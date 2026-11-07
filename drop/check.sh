#!/bin/sh
# drop 的门。这个目录不引用外面任何东西 —— 语言的全部就是 spec.txt + drop.py + 这些例子。
#   [1/5] 例子     examples/*.dr 与 tests/*.dr（冷读读者写的程序，v0.40 改写过）的实际输出必须等于 *.out
#                 examples/http.dr 这段跳过：它是服务器，第五段拿真 socket 打它
#   [2/5] 对账     spec.txt 的错误码 = drop.py 会吐的错误码；实现表面每条都有定义原话对着
#                 且每条码都有一个真能触发它的样本
#   [3/5] 预算     spec.txt 是全部定义：超预算 = 定义在膨胀，不是能力在增长
#   [4/5] 定义即可执行  从 spec.txt 重新抽出 > 行，必须与 examples/spec.dr 逐字相同，且跑出 *.out
#   [5/5] 服务器  起真进程 + 裸 socket 打 examples/http.dr，对话必须逐字等于 tests/http.expected
#                 （预算的来由：v0.38 第一版定义 497 token，冷读测试写出 3 份能跑的程序的读者
#                  同时交回 25 条「靠猜」—— 补完这 25 条后 1,100 上下。守的是「一遍读得完 +
#                  猜的次数为 0」，不是某个好看的数。v0.40 抬到 1,750 只有一次原因：定义多背了
#                  「循环只有一种」和整个服务器协议；现在占用 100.0%，加一句必须先删一句。）
#
# 破坏测试的纪律（本轮踩过一次才补上的）：一条破坏的「红」必须归因到它声称的那个原因。
# Python 崩栈、找不到文件、sed 没落到目标上 —— 这些也是非零退出码，看起来和抓住一模一样，其实是假绿。
# 所以每条破坏都先断言「破坏真的落到被改的那一处」，再断言「门因为这一处红了」。
set -e
cd "$(dirname "$0")"
export BUDGET_TOK=1750
export BUDGET_BYTES=11900
PY=${PYTHON:-python3}

# sh drop/check.sh --update  改完 spec.txt 后重抽 examples/spec.{dr,out}
if [ "$1" = "--update" ]; then
  $PY - <<'UP'
import subprocess, sys
lines = [l[2:] for l in open('spec.txt', encoding='utf-8').read().splitlines() if l.startswith('> ')]
src = "".join(l + "\n" for l in lines)
open('examples/spec.dr', 'w', encoding='utf-8').write(src)
r = subprocess.run([sys.executable, 'drop.py', 'examples/spec.dr'], capture_output=True, text=True)
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
       --include='*.out' --include='*.expected' . ; then
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
    r = subprocess.run([sys.executable, 'drop.py', f], capture_output=True, text=True)
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
    r = subprocess.run([sys.executable, 'drop.py'] + args, capture_output=True, text=True)
    return r.returncode, r.stdout, r.stderr
for args, want in [([], 2), (['/tmp/不存在.dr'], 1)]:
    rc, out, err = run(args)
    if rc != want or "Traceback" in err:
        print("  差 CLI %r：rc=%d（要 %d）err=%r" % (args, rc, want, err[-120:]))
        bad += 1
    else:
        print("  对上 CLI %r -> rc=%d，一行说明，无堆栈" % (args, rc))
sys.exit(1 if bad else 0)
PY

echo "[2/5] 定义 <-> 实现对账 + 码的活性"
$PY drop.py --self-check
$PY drop.py --self-test

echo "[3/5] 定义预算"
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
r = subprocess.run([sys.executable, 'drop.py', 'examples/spec.dr'], capture_output=True, text=True)
if r.returncode != 0 or r.stdout != exp:
    print("  红：定义里的例子跑出来不是那个数")
    print("    rc=%d %s" % (r.returncode, r.stderr.strip()))
    print("    期望 %r" % exp)
    print("    得到 %r" % r.stdout)
    sys.exit(1)
print("  绿：从定义抽出 %d 行真代码，逐字对得上，跑出来 %d 行输出全对" % (len(lines), exp.count(chr(10))))
PY

echo "[5/5] 服务器（拿真 socket 打 examples/http.dr）"
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
echo "      超上限断（不报错）/ 处理函数里红 = 整个服务器红 / 一次请求回两次 = E14"
rm -f "$T_live.out" "$T_live.err"

echo "破坏测试（门自己会不会失效）"
T=$(mktemp -d)
cp drop.py spec.txt "$T/"
mkdir -p "$T/examples" && cp examples/spec.dr examples/spec.out "$T/examples/"
trap 'rm -rf "$T"' EXIT

# Z1 从定义里抹掉一条错误码 -> 对账必须红
sed -i '/E11/d' "$T/spec.txt"
if $PY "$T/drop.py" --self-check --spec "$T/spec.txt" >/dev/null 2>&1; then
  echo "  假绿 Z1：定义少了 E11，对账却没红"; exit 1
fi
echo "  抓住 Z1：定义少一条码 -> 对账红"
cp spec.txt "$T/spec.txt"

# Z2 把实现里一条码的文案改掉 -> 对账必须红（不许两边各说各话）
sed -i 's/"E6": "下标越界"/"E6": "下标跑出去了"/' "$T/drop.py"
if $PY "$T/drop.py" --self-check --spec "$T/spec.txt" >/dev/null 2>&1; then
  echo "  假绿 Z2：实现改了说法，对账却没红"; exit 1
fi
echo "  抓住 Z2：实现与定义说得不一样 -> 对账红"
cp drop.py "$T/drop.py"

# Z3 除零改报成别的码 -> 活性测试必须红（它验的是码号，不只是红不红）
sed -i 's/raise DropError("E8", line, "%s %s/raise DropError("E4", line, "%s %s/' "$T/drop.py"
if $PY "$T/drop.py" --self-test >/dev/null 2>&1; then
  echo "  假绿 Z3：E8 的检查改报成 E4，活性测试却没红"; exit 1
fi
echo "  抓住 Z3：有条码不再按自己报 -> 活性测试红"
cp drop.py "$T/drop.py"

# Z4 把定义里的一句规则删掉（实现照旧）-> 表面探针必须红
sed -i 's|切片：左含右不含、两端可省；越界不报错|切片端点另说|' "$T/spec.txt"
if cmp -s spec.txt "$T/spec.txt"; then
  echo "  假绿 Z4：破坏没落到定义上，先修这条 sed"; exit 1
fi
if $PY "$T/drop.py" --self-check --spec "$T/spec.txt" >/dev/null 2>&1; then
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
if $PY "$T/drop.py" "$T/examples/spec.dr" >/dev/null 2>&1; then
  echo "  假绿 Z5：定义里的例子已经跑不通，却没红"; exit 1
fi
echo "  抓住 Z5：定义里的例子跑不通 -> 红"

# Z6 把 print 打表的规则改回「不套括号」（本轮认下的真缺陷）-> 例子必须立刻对不上
sed -i 's@",".join(quote(x) if isinstance(x, str) else show(x) for x in v)@",".join(show(x) for x in v)@' "$T/drop.py"
if cmp -s drop.py "$T/drop.py"; then
  echo "  假绿 Z6：破坏用的 sed 没落到实现上，先修这条破坏"; exit 1
fi
if $PY - "$T/drop.py" <<'PY'
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
cp drop.py "$T/drop.py"

# Z7 把所有 *.out 拿掉（本轮真踩过：仓库的 .gitignore 里 *.out 把期望输出吞了，
#    本地全绿、干净克隆里门却根本没跑）-> 门必须红，而且必须说人话，不许吐堆栈
T7=$(mktemp -d)
cp -R examples tests drop.py spec.txt check.sh "$T7/"
rm -f "$T7"/*/*.out
trap 'rm -rf "$T" "$T7" "$T11" "$T_live.out" "$T_live.err"' EXIT
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
sed -i 's/^SUGAR = (.*)$/SUGAR = ()/' "$T/drop.py"
if cmp -s drop.py "$T/drop.py"; then
  echo "  假绿 Z8：破坏没落到实现上，先修这条 sed"; exit 1
fi
if $PY "$T/drop.py" --self-test >/dev/null 2>&1; then
  echo "  假绿 Z8：sugar 名字放回表面了，活性测试却没红"; exit 1
fi
echo "  抓住 Z8：把 map/filter/fold/range/sort 放回可调用 -> 活性测试红"
cp drop.py "$T/drop.py"

# Z9 把 print 的「只收字符串」改回悄悄转换 -> 活性测试必须红（E10 的样本不红了）
$PY - "$T/drop.py" <<'ZPY'
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
assert s.count(old) == 1, "Z9 的锚点没了"
open(p, "w", encoding="utf-8").write(s.replace(old, new))
ZPY
if $PY "$T/drop.py" --self-test >/dev/null 2>&1; then
  echo "  假绿 Z9：print 又能悄悄转文本了，活性测试却没红"; exit 1
fi
echo "  抓住 Z9：print 恢复隐式转换 -> 活性测试红"
cp drop.py "$T/drop.py"

# Z10 让 serve 在没给 --serve 时自己挑个端口 -> 服务器那段必须给意见：端口有了第二个来源
$PY - "$T/drop.py" <<'YPY'
import sys
p = sys.argv[1]
s = open(p, encoding="utf-8").read()
old = """    port = SRV["port"]
    if port is None:"""
new = """    port = SRV["port"] or 8321
    if False:"""
assert s.count(old) == 1, "Z10 的锚点没了"
open(p, "w", encoding="utf-8").write(s.replace(old, new))
YPY
if $PY - "$T" <<'RPY'
import os, subprocess, sys, time
t = sys.argv[1]
# 用打了破坏的实现起 examples/http.dr，不给 --serve：它现在会自己挑一个端口
p = subprocess.Popen([sys.executable, os.path.join(t, "drop.py"), "examples/http.dr"],
                     cwd=t, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
deadline = time.time() + 6
heard = False
while time.time() < deadline:
    line = p.stderr.readline()
    if line and "在听" in line:
        heard = True
        break
    if p.poll() is not None:
        break
p.kill()
sys.exit(0 if heard else 1)      # 听到「在听」= 端口有了第二个来源
RPY
then
  echo "  假绿 Z10：serve 在没 --serve 时自己挑了端口，门却没意见"; exit 1
fi
echo "  抓住 Z10：serve 在没 --serve 时自己挑端口 -> 立刻红（端口只有命令行一个来源）"
cp drop.py "$T/drop.py"

# Z11 往定义里塞一句「去本目录外面看」（本轮真发生过：实现的一句注释里写了仓库别处的目录名，
#    而门里当时只有文档中手写的那一句 grep，没有任何东西会红）-> 第一段必须立刻红。
T11=$(mktemp -d)
cp -R examples tests drop.py spec.txt check.sh "$T11/"
$PY - "$T11" <<'ZPY'
import sys, os
p = os.path.join(sys.argv[1], "spec.txt")
s = open(p, encoding="utf-8").read()
open(p, "w", encoding="utf-8").write(s + "// 端口的事另见 " + "req" + "/SRC.md 第 3 条\n")
ZPY
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

echo "drop: 绿（五段 + 十一条破坏）"
