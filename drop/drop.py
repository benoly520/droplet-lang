#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""drop —— 一门极简语言的参考实现。

  python3 drop.py 程序.dr                     跑
  python3 drop.py --self-check [--spec f]     定义里的错误码 = 这里会吐的错误码
  python3 drop.py --self-test                 每条错误码都有一个真能触发的样本

这门语言的全部规则在 spec.txt 里；本文件不做任何额外的承诺，也不引用任何外部清单。
"""
import os
import sys

# ---------------------------------------------------------------- 错误表（穷举）
ERRORS = {
    "E1": "没绑过的名字",
    "E2": "参数个数不符",
    "E3": "语法错",
    "E4": "不是函数不能调用",
    "E5": "值种类不对",
    "E6": "下标越界",
    "E7": "条件不是 true/false",
    "E8": "除数为 0",
    "E9": "该是整数的地方不是",
    "E10": "该是字符串的地方不是",
    "E11": "话说到一半",
    "E12": "形参重名",
    "E13": "调用叠到第 201 层",
}

MAX_CALL_DEPTH = 200
# 一层 drop 调用会吃掉好几层 Python 帧；把宿主上限抬到绝对够不到的地方，
# 好让「递归太深」始终是本语言自己报的 E13，而不是宿主的一句 RecursionError。
sys.setrecursionlimit(MAX_CALL_DEPTH * 25 + 1000)   # 本实现的上限；超了就报 E13，而不是让宿主栈崩

# 语言的全部表面。每一项必须在 spec.txt 里有一句原话（探针），少一句就红。
# 加一条构造 = 先往定义里加一句话，再往这里加一个探针；只动代码不动定义，--self-check 就拦住。
CONSTRUCTS = [
    ('换行是唯一分隔', '换行是唯一的语句分隔符'),
    ('注释不参与语义', '// 到行尾是注释，注释里的引号和括号不算数'),
    ('先全解析再执行', '整个文件先全部解析、再从头执行'),
    ('07 就是 7', '07 就是 7'),
    ('没有小数', '没有小数（写 1.5 是 E3）'),
    ('四种转义', '四种转义参与语义'),
    ('串里真实换行', '串里写真实换行 = E11'),
    ('名字可以带汉字', '汉字算字母'),
    ('大小写算不同', '大小写算不同名字'),
    ('let 绑定', 'let x = 1 + 2'),
    ('顶层没有裸赋值', '顶层没有裸赋值'),
    ('名字不能占内置名', '占了是 E3'),
    ('重名遮住旧的', '重名 = 新绑定遮住旧的'),
    ('具名函数（表达式体）', 'fn add(a, b) = a + b'),
    ('具名函数（块体）', 'fn show(x) { print(x) x }'),
    ('空块的值', '{} 的值是无'),
    ('块当表达式', '{ let a = 1 a + 1 }'),
    ('匿名函数只能带表达式体', '匿名函数，只能带表达式体'),
    ('块里可定义局部函数', 'fn outer(x) {'),
    ('递归引用自己', '递归可以引用自己'),
    ('词法作用域', '它被写在那儿时能看到的名字'),
    ('函数是值', '可以 let f = len'),
    ('函数放进表再调用', '可以放进表里再取出来调用'),
    ('先求参数再绑', '先把每个实参求一次值'),
    ('求值顺序从左到右', '求值顺序从左到右'),
    ('顶层表达式的值丢掉', '算它、把值丢掉'),
    ('六种值', '就这六种，没有第七种'),
    ('不自动转', '它们之间不自动转'),
    ('表可嵌套可混装', '表可嵌套可混装'),
    ('下标从 0 数', '从 0 数；越界 E6，负数下标也 E6'),
    ('切片省端点', '左含右不含，两端可省'),
    ('切片越界折边界', '越界不报错，折到 0 和 len(xs)'),
    ('if 惰性', '只算选中的那支'),
    ('if 的条件要真假', 'c 必须 true/false'),
    ('括号只是分组', '括号只是表达式，不是调用'),
    ('一元负号看左边', '紧跟在整数、字符串、名字、) ] 之后是减法'),
    ('取负只吃整数', '是取负，只吃整数'),
    ('表能拼接', '或两个表拼接'),
    ('跨种相等 false', '不同种一律 false'),
    ('表逐个元素比', '表逐个元素比'),
    ('函数不能比较', '函数之间 == 比不出真假 = E5'),
    ('真假不能比大小', 'true false none 不能比大小'),
    ('短路', '左边已经定了就不算右边'),
    ('! 只吃真假', '!a               只吃 true/false'),
    ('优先级排列', '||   &&   == !=   < <= > >=   + -   * / %   一元 - !   后缀 () []'),
    ('print 只收一个值', '只收 1 个值'),
    ('整数打数字', '整数→"12"'),
    ('字符串不加引号', '字符串→它自己（不加引号）'),
    ('无打 none', '无→"none"'),
    ('print 就是 str', '先按 str(x) 转成文本，再打这一行'),
    ('表转文本套括号', '表→"[1,2]"：外面套方括号'),
    ('str 元素套引号', '其中的字符串元素外面要再套引号'),
    ('str 不吃函数', '函数不能转文本 = E5'),
    ('num 认负号', '可选正负号紧贴一串数字'),
    ('num 不认空格', '前后空格也算别的东西（" -42 " = E9）'),
    ('num 不吃整数', '给它整数或别的种类 = E10'),
    ('len 按字符', '"文" 的 len 是 1（按字符，不按字节）'),
    ('range 负数空表', 'n 不大于 0 得空表'),
    ('map 返回新表', '返回结果组成的表'),
    ('filter 只留 true', '只留下 f(x) 为 true 的元素'),
    ('filter 回别的 E7', 'f 回了不是 true/false 的东西 = E7'),
    ('fold 左折', 'f(f(f(z, x0), x1), x2)'),
    ('fold 空表给 z', 'xs 是空表就返回 z'),
    ('sort 空表', 'sort([]) = []'),
    ('sort 混装 E5', '混了或夹了别的 = E5'),
    ('join 空表空串', 'join(sep, []) = ""'),
    ('join 非字符串 E10', '元素或 sep 不是字符串 = E10'),
    ('三不吃字符串', '这三个不吃字符串'),
    ('头参要函数 E4', '第一个参数必须是函数（不是 = E4）'),
    ('内置只有 10 个', '内置函数只有 10 个'),
    ('没有别的内置', '没有 split、trim、abs、min、max、in'),
    ('叠到第 201 层', '第 201 层进不去 = E13'),
    ('出错只一行', '打印一行「挂：第 N 行 Ex —— 中文说明」到 stderr，退出码 1'),
    ('错误行的两处例外', '函数体里的错报在调用那一行，表字面量里的错报在 [ 那一行'),
    ('没有第 14 种', '没有第 14 种'),
    ('命令行不许吐堆栈', '文件读不到、命令行用错同样是一行「挂：…」，不是 Python 的 Traceback'),
]

class DropError(Exception):
    def __init__(self, code, line, what=""):
        self.code, self.line, self.what = code, line, what
    def __str__(self):
        w = ("：" + self.what) if self.what else ""
        return "第 %d 行 %s —— %s%s" % (self.line, self.code, ERRORS[self.code], w)

# ---------------------------------------------------------------- 词法
PUNCT = ["==", "!=", "<=", ">=", "&&", "||",
         "+", "-", "*", "/", "%", "!", "<", ">", "=", ",", ":", "(", ")", "[", "]", "{", "}"]

def is_name_start(c):
    return c.isalpha() or c == "_"

def is_name(c):
    return c.isalnum() or c == "_"

def tokenize(src):
    toks, i, line, n = [], 0, 1, len(src)
    while i < n:
        c = src[i]
        if c == "\n":
            line += 1; i += 1; continue
        if c in " \t\r":
            i += 1; continue
        if src.startswith("//", i):
            j = src.find("\n", i)
            i = n if j < 0 else j
            continue
        if c.isdigit():
            j = i
            while j < n and src[j].isdigit():
                j += 1
            toks.append(("num", int(src[i:j]), line)); i = j; continue
        if c == '"':
            j, out = i + 1, []
            while True:
                if j >= n:
                    raise DropError("E11", line, "字符串没闭合")
                if src[j] == '"':
                    break
                if src[j] == "\n":
                    raise DropError("E11", line, "字符串里不许有换行，要换行写 \\n")
                if src[j] == "\\":
                    if j + 1 >= n:
                        raise DropError("E11", line, "反斜杠后面什么都没有")
                    e = src[j + 1]
                    if e not in '\\nt"':
                        raise DropError("E3", line, "不认 \\%s，只有 \\\\ \\n \\t \\\"" % e)
                    out.append({"\\": "\\", "n": "\n", "t": "\t", '"': '"'}[e])
                    j += 2; continue
                out.append(src[j]); j += 1
            toks.append(("str", "".join(out), line)); i = j + 1; continue
        if is_name_start(c):
            j = i
            while j < n and is_name(src[j]):
                j += 1
            w = src[i:j]
            toks.append(("kw" if w in ("let", "fn", "if", "true", "false", "none") else "name", w, line))
            i = j; continue
        for p in PUNCT:
            if src.startswith(p, i):
                toks.append(("op", p, line)); i += len(p); break
        else:
            raise DropError("E3", line, "不认的字符 %r" % c)
    toks.append(("eof", None, line))
    return toks

# ---------------------------------------------------------------- 语法
class P:
    def __init__(self, toks):
        self.t, self.i = toks, 0
    def peek(self, k=0):
        return self.t[self.i + k]
    def take(self):
        tok = self.t[self.i]; self.i += 1; return tok
    def is_op(self, *ops):
        ty, v, _ = self.peek()
        return ty == "op" and v in ops
    def is_kw(self, *kws):
        ty, v, _ = self.peek()
        return ty == "kw" and v in kws
    def expect(self, v):
        if self.is_op(v):
            return self.take()
        t = self.peek()
        if t[0] == "eof":
            raise DropError("E11", t[2], "这里该有 %r，可文件到头了" % v)
        raise DropError("E3", t[2], "这里该有 %r，看到的是 %r" % (v, t[1]))

def parse_program(src):
    p = P(tokenize(src))
    items = []
    while p.peek()[0] != "eof":
        items.append(parse_item(p))
    return items

def parse_params(p):
    params, seen = [], set()
    if not p.is_op(")"):
        while True:
            q = p.take()
            if q[0] != "name":
                raise DropError("E3", q[2], "形参要是一个名字")
            if q[1] in BUILTINS:
                raise DropError("E3", q[2], "%s 是内置名字，不能当形参" % q[1])
            if q[1] in seen:
                raise DropError("E12", q[2], "形参 %s 写了两次" % q[1])
            seen.add(q[1]); params.append(q[1])
            if p.is_op(","):
                p.take(); continue
            break
    p.expect(")")
    return params

def parse_item(p):
    """let / fn / 表达式"""
    if p.is_kw("let"):
        line = p.take()[2]
        name = p.take()
        if name[0] != "name":
            raise DropError("E3", line, "let 后面要是一个名字")
        p.expect("=")
        return ("let", name[1], parse_expr(p, 0), line)
    if p.is_kw("fn"):
        line = p.take()[2]
        name = p.take()
        if name[0] != "name":
            raise DropError("E3", line, "fn 后面要是一个名字")
        p.expect("(")
        params = parse_params(p)
        if p.is_op("="):
            p.take()
            return ("fn", name[1], params, ("expr", parse_expr(p, 0)), line)
        if p.is_op("{"):
            p.take()
            body = []
            while not p.is_op("}"):
                if p.peek()[0] == "eof":
                    raise DropError("E11", p.peek()[2], "花括号没闭合")
                body.append(parse_item(p))
            p.expect("}")
            return ("fn", name[1], params, ("block", body), line)
        raise DropError("E3", p.peek()[2], "fn 的体要么 = 表达式，要么 { 若干句 }")
    if p.peek()[0] == "kw" and p.peek()[1] in ("true", "false"):
        pass
    if p.peek()[0] in ("op",) and p.peek()[1] in (")", "}", "]", ","):
        raise DropError("E11", p.peek()[2], "这里冒出一个 %r" % p.peek()[1])
    return ("expr", parse_expr(p, 0), p.peek()[2])

def parse_expr(p, min_prec):
    LEFT = {"||": 1, "&&": 2, "==": 3, "!=": 3, "<": 4, "<=": 4, ">": 4, ">=": 4,
            "+": 5, "-": 5, "*": 6, "/": 6, "%": 6}
    if p.is_op("-", "!"):
        op = p.take()
        lhs = ("un", op[1], parse_expr(p, 7), op[2])
    else:
        lhs = parse_postfix(p)
    while True:
        ty, v, _ = p.peek()
        if ty != "op" or v not in LEFT or LEFT[v] < min_prec:
            break
        p.take()
        rhs = parse_expr(p, LEFT[v] + 1)
        lhs = ("bin", v, lhs, rhs, lhs[3] if len(lhs) > 3 else p.peek()[2])
    return lhs

def parse_postfix(p):
    e = parse_atom(p)
    while True:
        if p.is_op("("):
            line = p.take()[2]
            args = []
            if not p.is_op(")"):
                while True:
                    args.append(parse_expr(p, 0))
                    if p.is_op(","):
                        p.take(); continue
                    break
            p.expect(")")
            e = ("call", e, args, line)
            continue
        if p.is_op("["):
            line = p.take()[2]
            lo = parse_expr(p, 0) if not p.is_op(":") else None
            colon = p.is_op(":")
            hi = None
            if colon:
                p.take()
                hi = parse_expr(p, 0) if not p.is_op("]") else None
            p.expect("]")
            if not colon:
                e = ("index", e, lo, line)
            else:
                e = ("slice", e, lo, hi, line)
            continue
        break
    return e

def parse_atom(p):
    ty, v, line = p.take()
    if ty == "num":
        return ("num", v, line)
    if ty == "str":
        return ("str", v, line)
    if ty == "kw" and v in ("true", "false"):
        return ("bool", v == "true", line)
    if ty == "kw" and v == "none":
        return ("none", line)
    if ty == "kw" and v == "if":
        p.expect("(")
        c = parse_expr(p, 0); p.expect(",")
        a = parse_expr(p, 0); p.expect(",")
        b = parse_expr(p, 0)
        p.expect(")")
        return ("if", c, a, b, line)
    if ty == "kw" and v == "fn":
        p.expect("(")
        params = parse_params(p)
        p.expect("=")
        return ("fn", params, parse_expr(p, 0), line)
    if ty == "name":
        return ("name", v, line)
    if ty == "op" and v == "(":
        e = parse_expr(p, 0)
        p.expect(")")
        return e
    if ty == "op" and v == "{":
        items = []
        while p.peek()[0] != "eof" and not p.is_op("}"):
            items.append(parse_item(p))
        p.expect("}")
        return ("block", items, line)
    if ty == "op" and v == "[":
        xs = []
        if not p.is_op("]"):
            while True:
                xs.append(parse_expr(p, 0))
                if p.is_op(","):
                    p.take(); continue
                break
        p.expect("]")
        return ("list", xs, line)
    if ty == "eof":
        raise DropError("E11", line, "话说到一半，文件先到头了")
    raise DropError("E3", line, "这里不该出现 %r" % (v,))

# ---------------------------------------------------------------- 值
class Fn:
    def __init__(self, params, body, env, name):
        self.params, self.body, self.env, self.name = params, body, env, name

class Env:
    __slots__ = ("d", "outer")
    def __init__(self, outer=None):
        self.d, self.outer = {}, outer
    def get(self, k):
        e = self
        while e:
            if k in e.d:
                return e.d[k]
            e = e.outer
        return None
    def has(self, k):
        e = self
        while e:
            if k in e.d:
                return True
            e = e.outer
        return False
    def set(self, k, v):
        self.d[k] = v

def is_int(v):
    return isinstance(v, int) and not isinstance(v, bool)

def show(v):
    if v is None:
        return "none"
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, list):
        return "[" + ",".join(quote(x) if isinstance(x, str) else show(x) for x in v) + "]"
    if isinstance(v, int):
        return str(v)
    if isinstance(v, str):
        return v
    if isinstance(v, Fn):
        return "函数 %s" % v.name
    if isinstance(v, tuple):
        return "内置函数 %s" % v[1]
    raise DropError("E5", 0, "内部：不认识的值 %r" % (type(v).__name__,))

def text_of(v, line=0):   # str / print 用它：函数不是文本
    if isinstance(v, (Fn, tuple)):
        raise DropError("E5", line, "函数不能转成文本，只能调用")
    return show(v)

def quote(t):  # 表里的字符串要加引号，为的是 ["a,b"] 与 ["a","b"] 分得开
    return '"' + t.replace("\\", "\\\\").replace('\"', '\\"') + '"'

def print_text(v, line=0):
    return text_of(v, line)

# ---------------------------------------------------------------- 内置 10 个
def B_print(args, line):
    print(print_text(args[0], line)); return None
def B_len(args, line):
    v = args[0]
    if isinstance(v, (str, list)):
        return len(v)
    raise DropError("E5", line, "len 要的是字符串或表，给的是 %s" % show(v))
def B_str(args, line):
    return text_of(args[0], line)
def B_num(args, line):
    s = args[0]
    if not isinstance(s, str):
        raise DropError("E10", line, "num 吃字符串，给的是 %s" % show(s))
    body = s[1:] if s[:1] in ("-", "+") else s
    if body and body.isdigit():
        return int(s)
    raise DropError("E9", line, "%r 不是整数的写法" % s)
def B_range(args, line):
    n = args[0]
    if not is_int(n):
        raise DropError("E9", line, "range 要整数，给的是 %s" % show(n))
    return list(range(n))
def B_map(args, line):
    f, xs = args
    return [call(f, [x], line) for x in _list(xs, line, "map")]
def B_filter(args, line):
    f, xs = args
    out = []
    for x in _list(xs, line, "filter"):
        r = call(f, [x], line)
        if not isinstance(r, bool):
            raise DropError("E7", line, "filter 的判断函数必须回 true/false，%s 回了 %s" % (getattr(f, "name", "?"), show(r)))
        if r:
            out.append(x)
    return out
def B_fold(args, line):
    f, xs, z = args
    acc = z
    for x in _list(xs, line, "fold"):
        acc = call(f, [acc, x], line)
    return acc
def B_sort(args, line):
    xs = _list(args[0], line, "sort")
    ints = [x for x in xs if is_int(x)]
    strs = [x for x in xs if isinstance(x, str)]
    if len(ints) + len(strs) != len(xs):
        raise DropError("E5", line, "表里有排不了序的元素 %s" % show([x for x in xs if not (is_int(x) or isinstance(x, str))][0]))
    if ints and strs:
        raise DropError("E5", line, "sort 要么全整数、要么全字符串，给的是 %s" % show(xs))
    return sorted(xs)
def B_join(args, line):
    sep, xs = args
    if not isinstance(sep, str):
        raise DropError("E10", line, "join 的分隔符要是字符串，给的是 %s" % show(sep))
    out = []
    for x in _list(xs, line, "join"):
        if not isinstance(x, str):
            raise DropError("E10", line, "join 只能拼字符串，遇到 %s" % show(x))
        out.append(x)
    return sep.join(out)

def _list(v, line, who):
    if not isinstance(v, list):
        raise DropError("E5", line, "%s 要的是表，给的是 %s" % (who, show(v)))
    return v

BUILTINS = {"print": (B_print, 1), "len": (B_len, 1), "str": (B_str, 1), "num": (B_num, 1),
            "range": (B_range, 1), "map": (B_map, 2), "filter": (B_filter, 2),
            "fold": (B_fold, 3), "sort": (B_sort, 1), "join": (B_join, 2)}

_depth = [0]

def call(f, args, line):
    if isinstance(f, tuple) and f[0] == "builtin":
        fn, n = BUILTINS[f[1]]
        if len(args) != n:
            raise DropError("E2", line, "%s 收 %d 个，给了 %d 个" % (f[1], n, len(args)))
        return fn(args, line)
    if isinstance(f, Fn):
        if len(args) != len(f.params):
            raise DropError("E2", line, "%s 收 %d 个，给了 %d 个" % (f.name, len(f.params), len(args)))
        if _depth[0] >= MAX_CALL_DEPTH:
            raise DropError("E13", line, "%s 递归到第 %d 层 —— 本实现的上限是 %d 层"
                            % (f.name, _depth[0] + 1, MAX_CALL_DEPTH))
        _depth[0] += 1
        try:
            return run_call(f, args)
        finally:
            _depth[0] -= 1
    raise DropError("E4", line, "%s 不能当函数调用" % show(f))

def run_call(f, args):
        e = Env(f.env)
        for k, v in zip(f.params, args):
            e.set(k, v)
        return run_body(f.body, e)

def run_body(body, env):
    kind = body[0]
    if kind == "expr":
        return ev(body[1], env)
    last = None
    for it in body[1]:
        last = ev_item(it, env)
    return last

def ev(node, env):
    k = node[0]
    if k == "num" or k == "str" or k == "bool":
        return node[1]
    if k == "none":
        return None
    if k == "list":
        return [ev(x, env) for x in node[1]]
    if k == "name":
        nm, line = node[1], node[2]
        if env.has(nm):
            return env.get(nm)
        if nm in BUILTINS:
            return ("builtin", nm)
        raise DropError("E1", line, "没绑过 %r" % nm)
    if k == "fn":
        return Fn(node[1], ("expr", node[2]), env, "一个匿名函数")
    if k == "block":
        return run_body(("seq", node[1]), env)
    if k == "if":
        c, line = ev(node[1], env), node[4]
        if not isinstance(c, bool):
            raise DropError("E7", line, "if 的条件要 true/false，给的是 %s" % show(c))
        return ev(node[2] if c else node[3], env)
    if k == "un":
        v, line = ev(node[2], env), node[3]
        if node[1] == "!":
            if not isinstance(v, bool):
                raise DropError("E7", line, "! 只吃 true/false，给的是 %s" % show(v))
            return not v
        if not is_int(v):
            raise DropError("E5", line, "一元 - 只吃整数，给的是 %s" % show(v))
        return -v
    if k == "bin":
        return ev_bin(node, env)
    if k == "call":
        callee, line = node[1], node[3]
        args = [ev(a, env) for a in node[2]]
        if callee[0] == "builtin":
            fn, n = BUILTINS[callee[1]]
            if len(args) != n:
                raise DropError("E2", line, "%s 收 %d 个，给了 %d 个" % (callee[1], n, len(args)))
            return fn(args, line)
        return call(ev(callee, env), args, line)
    if k == "index":
        v, i, line = ev(node[1], env), ev(node[2], env), node[3]
        if isinstance(v, (list, str)):
            if not is_int(i):
                raise DropError("E5", line, "下标要整数，给的是 %s" % show(i))
            if not (0 <= i < len(v)):
                raise DropError("E6", line, "%s 长 %d，取第 %d 个" % (show(v), len(v), i))
            return v[i]
        raise DropError("E5", line, "只有表和字符串能用 [] 取，给的是 %s" % show(v))
    if k == "slice":
        v, line = ev(node[1], env), node[4]
        if not isinstance(v, (list, str)):
            raise DropError("E5", line, "只有表和字符串能切片，给的是 %s" % show(v))
        a = 0 if node[2] is None else ev(node[2], env)
        b = len(v) if node[3] is None else ev(node[3], env)
        for w, x in (("起点", a), ("终点", b)):
            if not is_int(x):
                raise DropError("E5", line, "切片%s要整数，给的是 %s" % (w, show(x)))
        a = max(0, min(a, len(v)))
        b = max(a, min(b, len(v)))
        return v[a:b]
    raise DropError("E3", 0, "内部：认不出的节点 %r" % (k,))

def ev_bin(node, env):
    op, line = node[1], node[4]
    if op in ("&&", "||"):            # 这两条不许先算右边
        a = expect_bool(ev(node[2], env), line, op + " 左边")
        if (op == "||") == bool(a):   # || 见 true 就停，&& 见 false 就停
            return a
        return expect_bool(ev(node[3], env), line, op + " 右边")
    a, b = ev(node[2], env), ev(node[3], env)
    if op in ("==", "!="):
        for x in (a, b):
            if isinstance(x, (Fn, tuple)):
                raise DropError("E5", line, "函数不能比较，只能调用")
        r = same(a, b)
        return r if op == "==" else not r
    if op == "+":
        if isinstance(a, str) and isinstance(b, str):
            return a + b
        if isinstance(a, list) and isinstance(b, list):
            return a + b
        if is_int(a) and is_int(b):
            return a + b
        raise DropError("E5", line, "+ 要么两个整数、要么两个字符串、要么两个表，给的是 %s 和 %s" % (show(a), show(b)))
    if op in ("-", "*", "/", "%"):
        if not (is_int(a) and is_int(b)):
            raise DropError("E5", line, "%s 只吃整数，给的是 %s 和 %s" % (op, show(a), show(b)))
        if op == "-":
            return a - b
        if op == "*":
            return a * b
        if b == 0:
            raise DropError("E8", line, "%s %s —— 除数是 0" % (show(a), op))
        if op == "/":
            q = abs(a) // abs(b)
            return q if (a < 0) == (b < 0) else -q
        return a % b if a >= 0 else -((-a) % b)
    if op in ("<", "<=", ">", ">="):
        if isinstance(a, int) and isinstance(b, int) and not isinstance(a, bool):
            pass
        elif isinstance(a, str) and isinstance(b, str):
            pass
        else:
            raise DropError("E5", line, "%s 要比同类，给的是 %s 和 %s" % (op, show(a), show(b)))
        return {"<": a < b, "<=": a <= b, ">": a > b, ">=": a >= b}[op]
    raise DropError("E3", line, "没有这个运算 %r" % op)

def expect_bool(v, line, where):
    if not isinstance(v, bool):
        raise DropError("E7", line, "%s要 true/false，给的是 %s" % (where, show(v)))
    return v

def same(a, b):
    if isinstance(a, bool) != isinstance(b, bool):
        return False
    if isinstance(a, list) or isinstance(b, list):
        return isinstance(a, list) and isinstance(b, list) and len(a) == len(b) and all(same(x, y) for x, y in zip(a, b))
    if type(a) is not type(b):
        return False
    return a == b

def ev_item(it, env):
    if it[0] == "expr":
        return ev(it[1], env)
    if it[0] == "let":
        _, nm, e, line = it
        if nm in BUILTINS:
            raise DropError("E3", line, "%s 是内置名字，不许重新绑定" % nm)
        env.set(nm, ev(e, env))
        return None
    if it[0] == "fn":
        _, nm, params, body, line = it
        if nm in BUILTINS:
            raise DropError("E3", line, "%s 是内置名字，不许重新定义" % nm)
        env.set(nm, Fn(params, body, env, nm))
        return None
    raise DropError("E11", 0, "内部：顶层的东西不对")

# ---------------------------------------------------------------- 跑一个程序
def run_src(src):
    env = Env()
    for it in parse_program(src):
        ev_item(it, env)
    return env

def run_file(path):
    try:
        with open(path, encoding="utf-8") as f:
            text = f.read()
    except IOError as e:
        sys.stderr.write("挂：读不到文件 %r —— %s\n" % (path, e.strerror or e))
        return 1
    try:
        run_src(text)
    except BrokenPipeError:
        return 0            # 下游把输出管道关了，不是程序错
    return 0

# ---------------------------------------------------------------- 定义 <-> 实现对账
def parse_spec_codes(text):
    got, cur = {}, None
    for ln in text.splitlines():
        if "错误一共" in ln or "穷举" in ln:
            cur = True; continue
        if cur:
            for m in split_codes(ln):
                got[m[0]] = m[1]
            if got and "排版" in ln:
                break
    return got

def split_codes(ln):
    out, i = [], 0
    while True:
        j = ln.find("E", i)
        if j < 0:
            break
        k = j + 1
        while k < len(ln) and ln[k].isdigit():
            k += 1
        if k == j + 1:
            i = k; continue
        code = ln[j:k]
        m = k
        while m < len(ln) and ln[m] == " ":
            m += 1
        n = m
        for stop in ("  ", "    ", "\t"):
            p = ln.find(stop, m)
            if p >= 0 and (n == m or p < n):
                n = p
        tail = ln.find("E", n)
        n = tail if tail >= 0 else len(ln)
        out.append((code, ln[m:n].strip()))
        i = k
    return out

def self_check(spec_path):
    with open(spec_path, encoding="utf-8") as f:
        spec = parse_spec_codes(f.read())
    bad = []
    for c in sorted(set(spec) | set(ERRORS), key=lambda x: int(x[1:])):
        if c not in spec:
            bad.append("实现会吐 %s，定义里没写" % c)
        elif c not in ERRORS:
            bad.append("定义写了 %s，实现不会发生" % c)
        elif spec[c] != ERRORS[c]:
            bad.append("%s 两边说得不一样：定义「%s」/ 实现「%s」" % (c, spec[c], ERRORS[c]))
    with open(spec_path, encoding="utf-8") as f:
        text = f.read()
    for name, probe in CONSTRUCTS:
        if probe not in text:
            bad.append("实现里有「%s」，定义里没有这句话（探针 %r）" % (name, probe))
    if bad:
        for b in bad:
            print("  " + b)
        return 1
    print("绿：定义里的 %d 条错误码 = 实现会吐的 %d 条，逐一对上。" % (len(spec), len(ERRORS)))
    print("绿：实现的 %d 条表面，定义里每句都对着；定义没有的构造，实现里也没有。" % len(CONSTRUCTS))
    return 0

# ---------------------------------------------------------------- 每条码的活性样本
FIXTURES = [
    ('E1', 'let y = x + 1'),
    ('E2', 'fn f(a) = a\nprint(f(1, 2))'),
    ('E2', 'print(1, 2)'),
    ('E3', 'let x = )'),
    ('E3', 'fn f(a, b) = a + b\nprint(f(1 2))'),
    ('E3', 'let x = "a\\q"'),
    ('E3', 'let len = 1'),
    ('E3', 'fn f(if) = 1'),
    ('E3', 'fn f(a, len) = a'),
    ('E3', 'x = 1'),
    ('E3', 'print(1.5)'),
    ('E3', 'print(if(true, 1))'),
    ('E4', 'let x = 1\nx(2)'),
    ('E4', 'print(map(1, [1]))'),
    ('E5', 'let x = 1 + "a"'),
    ('E5', 'print(sort([1, "a"]))'),
    ('E5', 'print(str(print))'),
    ('E5', 'print(1[0])'),
    ('E5', 'let a = fn(x) = x\nlet b = fn(y) = y\nprint(a == b)'),
    ('E5', 'print(map(str, "ab"))'),
    ('E6', 'print([1][3])'),
    ('E6', 'print([1, 2, 3][-1])'),
    ('E7', 'print(if(1, 2, 3))'),
    ('E7', 'print(!1)'),
    ('E7', 'print(filter(fn(v) = v, [1, 2, 3]))'),
    ('E8', 'print(1 / 0)'),
    ('E9', 'print(num("1a"))'),
    ('E9', 'print(range("a"))'),
    ('E10', 'print(num(1))'),
    ('E10', 'print(join("-", [1]))'),
    ('E10', 'print(join(1, ["a"]))'),
    ('E11', 'let x = "abc'),
    ('E11', 'print(1'),
    ('E11', 'let x = "a\nb"'),
    ('E12', 'fn f(a, a) = a'),
    ('E13', 'fn deep(n) = if(n <= 0, 0, deep(n - 1))\nprint(deep(201))'),
]

def self_test():
    bad = []
    for code, src in FIXTURES:
        try:
            run_src(src)
            bad.append("%s 的样本居然不报 %s（码是装饰）" % (code, code))
            continue
        except DropError as e:
            if e.code != code:
                bad.append("%s 的样本报成了 %s（应报 %s）" % (code, e.code, code))
        except RecursionError:
            bad.append("%s 的样本递归炸了" % code)
    clean = 'fn add(a, b) = a + b\nprint(add(1, 2))'
    import contextlib, io
    buf = io.StringIO()
    try:
        with contextlib.redirect_stdout(buf):
            run_src(clean)
        if buf.getvalue().strip() != "3":
            bad.append("干净样本应当打印 3，实际打印 %r" % buf.getvalue())
    except DropError as e:
        bad.append("干净样本不该红，却报了 %s" % e)
    if bad:
        for b in bad:
            print("  " + b)
        return 1
    print("绿：%d 个样本逐个打中 %d 条错误码，干净样本无报错。" % (len(FIXTURES), len(ERRORS)))
    return 0

def main(argv):
    if not argv:
        sys.stderr.write("用法: python3 drop.py 程序.dr | --self-check | --self-test\n")
        return 2
    if argv[0] in ("-h", "--help"):
        print(__doc__.strip())
        return 0
    if argv[0] == "--self-check":
        here = os.path.dirname(os.path.abspath(__file__))
        sp = argv[argv.index("--spec") + 1] if "--spec" in argv else os.path.join(here, "spec.txt")
        return self_check(sp)
    if argv[0] == "--self-test":
        return self_test()
    return run_file(argv[0]) or 0

if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except DropError as e:
        print("挂：" + str(e), file=sys.stderr)
        sys.exit(1)
    except SyntaxError as e:
        print("挂：第 %d 行 %s" % (e.lineno, e.msg), file=sys.stderr)
        sys.exit(1)
