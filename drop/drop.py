#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""drop —— 一门极简语言的参考实现。

  python3 drop.py 程序.dr                     跑
  python3 drop.py --serve <port> 程序.dr      把程序里的 serve(port) 绑到这个端口上跑
  python3 drop.py --self-check [--spec f]     定义里的错误码 = 这里会吐的错误码
  python3 drop.py --self-test                 每条错误码都有一个真能触发的样本

这门语言的全部规则在 spec.txt 里；本文件不做任何额外的承诺，也不引用任何外部清单。
"""
import os
import signal
import socket
import sys
import time

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
    "E14": "宿主不肯做",
}

MAX_CALL_DEPTH = 200
# 只有「非尾的自身调用」才数层数：尾递归跳回开头，不涨层数（见 run_call）。
# 一层 drop 调用会吃掉好几层 Python 帧；把宿主上限抬到绝对够不到的地方，
# 好让「往里缩得太深」始终是本语言自己报的 E13，而不是宿主的一句 RecursionError。
sys.setrecursionlimit(MAX_CALL_DEPTH * 25 + 1000)   # 本实现的上限；超了就报 E13，而不是让宿主栈崩

# 语言的全部表面。每一项必须在 spec.txt 里有一句原话（探针），少一句就红。
# 加一条构造 = 先往定义里加一句话，再往这里加一个探针；只动代码不动定义，--self-check 就拦住。
CONSTRUCTS = [
    # 「实现表面 -> 定义原话」的一一映射：每条探针都必须能在 spec.txt 里逐字找到。
    ('换行是唯一分隔', '换行是唯一的语句分隔符'),
    ('分号不是东西', '分号不是东西（写它是 E3）'),
    ('注释不参与语义', '// 到行尾是注释，注释里的引号和括号不算数'),
    ('没有块注释', '没有块注释'),
    ('先全解析再执行', '整个文件先全部解析、再从头执行'),
    ('07 就是 7', '07 就是 7'),
    ('没有小数', '没有小数（写 1.5 是 E3）'),
    ('没有指数', '没有指数'),
    ('转义只有五个', '转义只有五个'),
    ('十六进制转义', '两个十六进制数字'),
    ('转义只吃紧跟的一个字符', '反斜杠只和紧跟它的那一个字符算一次'),
    ('串里真实换行', '串里写真实换行 = E11'),
    ('名字可以带汉字', '汉字算字母'),
    ('大小写算不同', '大小写算不同名字'),
    ('let 绑定', 'let x = 1 + 2'),
    ('let 右边是表达式', '右边只能是一个表达式'),
    ('没有赋值', '没有赋值，x = 1 是 E3'),
    ('重名遮住旧的', '重名 = 新绑定遮住旧的'),
    ('函数只有块体', '函数只有一种写法：名字 + 形参 + 块'),
    ('没有表达式体没有匿名', '没有表达式体、没有匿名函数'),
    ('块的值是最后一句', '块的值 = 最后一句'),
    ('空块的值', '{} 的值是无'),
    ('块当表达式', '{ let a = 1 a + 1 }'),
    ('顶层表达式的值丢掉', '算它、把值丢掉，不打印任何东西'),
    ('六种值', '就这六种，没有第七种'),
    ('不自动转', '它们之间不自动转；比较时不同种一律 false'),
    ('表可嵌套可混装', '表可嵌套可混装'),
    ('函数不是值', '函数不是值：不能绑给别的名字'),
    ('函数不能进表', '不能放进表'),
    ('只能按名字调用', '只能按名字调用'),
    ('函数当值用了 E3', '当值用了 = E3'),
    ('调用目标必须是名字', '只有具名函数和内置名能被调'),
    ('内置名只能调用', '内置名只能调用'),
    ('let f = len 是 E3', 'let f = len 是 E3'),
    ('request peer 只读', '是两个只读名字，程序自己绑不出也改不了'),
    ('保留名占了是 E3', '都不能用保留名，占了是 E3'),
    ('保留名的清单', '保留名 = 3 个关键字（let fn if）+ 9 个内置名 + request peer 两个只读名'),
    ('没有的循环也在清单里', 'while for break continue return else'),
    ('替你循环的名字也在清单里', 'range map filter fold sort'),
    ('下标从 0 数', '从 0 数；越界 E6，负数下标也 E6'),
    ('切片左含右不含', '切片：左含右不含、两端可省'),
    ('切片越界折边界', '越界不报错，折到 0 和 len(xs)'),
    ('if 惰性', '只算选中的那支'),
    ('if 的条件要真假', 'c 必须 true/false'),
    ('if 只有三支', '写两支或四支是 E3'),
    ('括号只是分组', '括号只是表达式，不是调用'),
    ('一元负号看左边', '紧跟整数、字符串、名字、) ] 之后是减法'),
    ('取负只吃整数', '取负只吃整数'),
    ('同类相加', '两个整数相加、两个字符串拼接、或两个表拼接'),
    ('混着来 E5', '混着来 = E5，不自动转'),
    ('除向零', '/ 向零取整（-7/2 = -3）'),
    ('模跟被除数', '% 跟被除数同号（-7%2 = -1）'),
    ('跨种相等 false', '什么都能比，不同种一律 false'),
    ('表逐个元素比', '表逐个元素比'),
    ('真假不能比大小', 'true false none 不能比大小'),
    ('比较只吃同类', '两边必须同为整数或同为字符串'),
    ('短路', '左边已经定了就不算右边'),
    ('优先级排列', '||   &&   == !=   < <= > >=   + -   * / %   一元 - !   后缀 () []'),
    ('词法作用域', '它被写在那儿时能看到的'),
    ('递归可以引用自己', '递归可以引用自己'),
    ('前向引用', '前向引用别的函数只在调用时才成立'),
    ('块里可定义局部函数', '块里也能定义局部函数'),
    ('局部函数不追改', '之后再重定义外层名字也追不改它'),
    ('先求参数再绑', '先把每个实参求一次值（从左到右），再绑到形参上'),
    ('str 是唯一的值变文本', '唯一的「值变文本」'),
    ('整数转文本', '整数 → "12"'),
    ('字符串不加引号', '字符串 → 它自己（不加引号）'),
    ('无转 none', 'none → "none"'),
    ('表转文本套括号', '表 → "[1,2]"：套方括号、元素各自 str、用 , 连'),
    ('str 元素套引号', '其中字符串元素再套引号'),
    ('str 转义两个字符', '串里的反斜杠和双引号会被转义'),
    ('print 只收字符串', 'print(s)   唯一的「写到屏幕」：只收字符串'),
    ('print 别的种类 E10', '别的种类 = E10'),
    ('屏幕只有一套格式规则', '屏幕上只有一套格式规则，就是 str 那套'),
    ('num 是唯一的文本变整数', '唯一的「文本变整数」'),
    ('num 认负号', '只认「可选负号紧贴一串数字」'),
    ('num 不认空格', '前后空格算别的（" -42 " = E9）'),
    ('bytes 是唯一文本变字节', '唯一的「文本变字节」'),
    ('bytes 小码一字节', '<=255 就是一个字节，更大的拆成 UTF-8'),
    ('len 按字符', '"文" 的 len 是 1（按字符，不按字节）'),
    ('len 只吃字符串和表', '字符串的字符数、表的元素数，别的 = E5'),
    ('split 保留空段', '空段保留 —— split("a,,b", ",") = ["a", "", "b"]'),
    ('split 单元素', 'split("a", ",") = ["a"]'),
    ('split 空串', 'split("", ",") = [""]'),
    ('split 不吃空分隔', 'sep 或 s 不是字符串 = E10，sep 是空串 = E10'),
    ('join 空表空串', 'join(sep, []) = ""'),
    ('join 非字符串 E10', '元素或 sep 不是字符串 = E10'),
    ('没有第四种转换', '除此之外没有第四种转换，也没有哪里会悄悄转换'),
    ('没有别的助手', '没有 trim / abs / min / max / in / sort，没有元组、没有多个返回值'),
    ('循环只有一种机制', '循环只有一种机制（尾递归）'),
    ('没有 while 没有 for', '没有 while、没有 for、没有 break/continue'),
    ('尾位跳回不涨层数', '函数在尾位调用自己 = 跳回函数开头，不涨层数'),
    ('尾位的定义', '尾位 = 函数体块里的最后一句'),
    ('if 两支里的自身调用都算尾位', '两支里的自身调用都算尾位'),
    ('非尾叠 200 层', '非尾的自身调用同时最多叠 200 层，第 201 层进不去 = E13'),
    ('缩与转的分别', '那是「往里缩」的上限，不是「转」的上限'),
    ('没有替你循环的名字', '没有 map / filter / fold / range / sort 这些「替你循环」的名字'),
    ('同一件事一种写法', '同一件事只许有一种写法'),
    ('值不会自己变种类', '值不会自己变种类'),
    ('serve 不返回', '它不返回，'),
    ('serve 只能是最后一句', '所以只能写在程序体的最后一句，写在别处就是 E3'),
    ('端口只有一个来源', 'serve() 不收参数：端口只有命令行一个来源，地址不可配'),
    ('每接一个请求先更新 request peer', '先把 request、peer 更新好'),
    ('处理函数固定叫 handle', '名字固定叫 handle'),
    ('keep-alive', '同一条连接读完一个请求按 keep-alive 接着读下一个'),
    ('reply 只有四行头', '头只有四行：HTTP/1.1 状态行'),
    ('Content-Length 按字节', 'Content-Length（按字节数，不是字符数）'),
    ('正文走 bytes 那条路', '正文按 bytes(body) 那一条路发出去'),
    ('HEAD 只回头', 'HEAD 只回头不回正文'),
    ('Connection close', 'Connection: close（一模一样）就关这条连接'),
    ('关的时候多回一行', '关的时候多回一行 Connection: close'),
    ('原因短语由宿主给', '状态行的原因短语由宿主给，程序管不着'),
    ('一次请求一个响应', '一次请求里调两次 = E14'),
    ('request 一字节一字符', '一字节一字符'),
    ('还没接见 E14', '还没接见过请求时读 request、peer 或调 reply = E14'),
    ('命令行给端口', 'python3 drop.py --serve <端口> 程序.dr'),
    ('没给端口 E14', '没给端口就调 serve = E14'),
    ('行分隔只认 CRLF', '行分隔只认 CRLF'),
    ('断连接不算错误', '直接断掉这条连接，这不是错误码'),
    ('服务器不兜程序的错', '处理函数里报错 = 整个服务器一红就停'),
    ('没有第二种跑法', '除 status、body、端口外没有可配的东西'),
    ('绑在所有网卡', 'serve()     绑在 0.0.0.0 上命令行给的端口'),
    ('一条一条地接', '一条一条地接（Ctrl-C 停）'),
    ('一红就停', '打印一行「挂：第 N 行 Ex —— 中文说明」到 stderr，退出码 1'),
    ('不许吐堆栈', '不是 Python 的 Traceback'),
    ('错误行的两处例外', '函数体里的错报在调用那一行，表字面量里的错报在 [ 那一行'),
    ('14 条码', '错误一共 14 种，穷举如下'),
    ('输出只有一条规则', '只有 print 产生输出，一次一行，程序结束时不会自动打任何东西'),
]

class _NoRequest(object):
    """request / peer 在还没接见过任何请求时的值：读它就是 E14，不是空字符串。"""
    def __repr__(self):
        return "<没接见>"

NO_REQUEST = _NoRequest()

class DropError(Exception):
    def __init__(self, code, line, what=""):
        self.code, self.line, self.what = code, line, what
    def __str__(self):
        w = ("：" + self.what) if self.what else ""
        return "第 %d 行 %s —— %s%s" % (self.line, self.code, ERRORS[self.code], w)

# ---------------------------------------------------------------- 词法
# 定义里明写「没有」的那几个循环写法：认成关键字，好让它们红得有针对性
NO_LOOP = ("while", "for", "break", "continue", "return", "else")
# 「替你循环」的那五个名字也一并禁掉：占成保留名，好让想用它的人收到一句指着唯一方案的 E3，
# 而不是含糊的 E1 —— 更不许自己定义一个同名的循环器绕过这一节。
SUGAR = ("range", "map", "filter", "fold", "sort")

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
                    if e == "x":
                        h = src[j + 2:j + 4]
                        if len(h) < 2 or any(k not in "0123456789abcdefABCDEF" for k in h):
                            raise DropError("E3", line, "\\x 后面要紧跟两个十六进制数字")
                        out.append(chr(int(h, 16)))
                        j += 4; continue
                    if e not in '\\nt"':
                        raise DropError("E3", line, "不认 \\%s，只有 \\ \\n \\t \\\" 和 \\xHH" % e)
                    out.append({"\\": "\\", "n": "\n", "t": "\t", '"': '"'}[e])
                    j += 2; continue
                out.append(src[j]); j += 1
            toks.append(("str", "".join(out), line)); i = j + 1; continue
        if is_name_start(c):
            j = i
            while j < n and is_name(src[j]):
                j += 1
            w = src[i:j]
            k = "kw" if w in ("let", "fn", "if", "true", "false", "none") + NO_LOOP else "name"
            toks.append((k, w, line))
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
    for i, it in enumerate(items[:-1]):
        e = it[1] if it[0] == "expr" else None
        if e is not None and e[0] == "call" and e[1][0] == "name" and e[1][1] == "serve":
            raise DropError("E3", it[2], "serve 只能是程序体的最后一句 —— 它不返回，后面写什么都到不了")
    return items

def parse_params(p):
    params, seen = [], set()
    if not p.is_op(")"):
        while True:
            q = p.take()
            if q[0] != "name":
                raise DropError("E3", q[2], "形参要是一个名字")
            if q[1] in RESERVED:
                raise DropError("E3", q[2], "%s 是保留名，不能当形参" % q[1])
            if q[1] in seen:
                raise DropError("E12", q[2], "形参 %s 写了两次" % q[1])
            seen.add(q[1]); params.append(q[1])
            if p.is_op(","):
                p.take(); continue
            break
    p.expect(")")
    return params

def bad_name(tok, line, who):
    """let 名 / 函数名 / 形参名都必须是没占用的普通名字。"""
    if tok[0] != "name":
        raise DropError("E3", line, "%s 后面要是一个名字" % who)
    if tok[1] in RESERVED:
        raise DropError("E3", line, "%s 是保留名，不能当%s的名字" % (tok[1], who))

def parse_item(p):
    """let / fn / 表达式 —— 只有这三类，没有第四类"""
    if p.is_kw("let"):
        line = p.take()[2]
        bad_name(p.peek(), line, "let")
        name = p.take()[1]
        p.expect("=")
        return ("let", name, parse_expr(p, 0), line)
    if p.is_kw("fn"):
        line = p.take()[2]
        bad_name(p.peek(), line, "fn")
        name = p.take()[1]
        p.expect("(")
        params = parse_params(p)
        if p.is_op("{"):
            p.take()
            body = []
            while not p.is_op("}"):
                if p.peek()[0] == "eof":
                    raise DropError("E11", p.peek()[2], "花括号没闭合")
                body.append(parse_item(p))
            p.expect("}")
            return ("fn", name, params, ("block", body), line)
        raise DropError("E3", p.peek()[2], "函数体只有 { 若干句 } 这一种写法")
    if p.peek()[0] == "kw" and p.peek()[1] in NO_LOOP:
        raise DropError("E3", p.peek()[2],
                        "没有 %s：循环只有「函数在尾位调用自己」这一种写法" % p.peek()[1])
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
            if e[0] != "name":
                raise DropError("E3", p.peek()[2],
                                "被调的必须是一个名字：具名函数或内置名，别的东西不能放在 () 左边")
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
        raise DropError("E3", line, "没有匿名函数：函数只有一种写法是 名字(形参) { 句子 }")
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
    raise DropError("E5", 0, "内部：不认识的值 %r" % (type(v).__name__,))

def text_of(v, line=0):   # str 用它
    return show(v)

def quote(t):  # 表里的字符串要加引号，为的是 ["a,b"] 与 ["a","b"] 分得开
    return '"' + t.replace("\\", "\\\\").replace('\"', '\\"') + '"'

def print_text(v, line=0):
    return text_of(v, line)

# ---------------------------------------------------------------- 内置 9 个
def B_print(args, line):
    v = args[0]
    if not isinstance(v, str):
        raise DropError("E10", line,
                        "print 只吃字符串，给的是 %s —— 要转文本就自己写 str(...)" % kind(v))
    print(v); return None
def kind(v):
    """值种类的名字 —— 报错时用，不参与任何语义。"""
    if v is None:
        return "none"
    if isinstance(v, bool):
        return "true/false"
    if isinstance(v, int):
        return "整数"
    if isinstance(v, str):
        return "字符串"
    if isinstance(v, list):
        return "表"
    return "别的东西"

def B_len(args, line):
    v = args[0]
    if isinstance(v, (str, list)):
        return len(v)
    raise DropError("E5", line, "len 要的是字符串或表，给的是 %s" % kind(v))
def B_str(args, line):
    return text_of(args[0], line)
def B_num(args, line):
    s = args[0]
    if not isinstance(s, str):
        raise DropError("E10", line, "num 吃字符串，给的是 %s" % kind(s))
    body = s[1:] if s[:1] == "-" else s
    if body and body.isdigit():
        return int(s)
    raise DropError("E9", line, "%r 不是整数的写法" % s)
def _wire(v, name, line):
    """bytes 与 reply 共用的一条路：每个字符取其码，<=255 就是一个字节，更大的拆成 UTF-8。"""
    if not isinstance(v, str):
        raise DropError("E10", line, "%s 吃字符串，给的是 %s" % (name, kind(v)))
    out = bytearray()
    for ch in v:
        o = ord(ch)
        if o <= 255:
            out.append(o)
        else:
            out += ch.encode("utf-8")
    return bytes(out)

def B_bytes(args, line):
    """文本 -> 字节串：返回的还是字符串，但每个字符就是一个字节（所以 len 它就是字节数）。"""
    return _wire(args[0], "bytes", line).decode("latin-1")
def B_split(args, line):
    sv, sep = args
    if not isinstance(sv, str) or not isinstance(sep, str):
        raise DropError("E10", line, "split 两个参数都要字符串，给的是 %s 和 %s" % (kind(sv), kind(sep)))
    if sep == "":
        raise DropError("E10", line, "split 的分隔符不能是空字符串 —— 空串切不出东西来")
    return sv.split(sep)
def B_join(args, line):
    sep, xs = args
    if not isinstance(sep, str):
        raise DropError("E10", line, "join 的分隔符要是字符串，给的是 %s" % kind(sep))
    if not isinstance(xs, list):
        raise DropError("E5", line, "join 要的是表，给的是 %s" % kind(xs))
    out = []
    for x in xs:
        if not isinstance(x, str):
            raise DropError("E10", line, "join 只能拼字符串，遇到 %s" % kind(x))
        out.append(x)
    return sep.join(out)

# ------------------------------------------------ 服务器：serve 一个，reply 一个
HDR_END = "\x0D\x0A\x0D\x0A"        # CRLFCRLF，首部与正文之间那个空行
HDR_MAX = 65536                        # 首部超过这么多字节就不当请求（不是错误码，直接断这条连接）
BODY_MAX = 1 << 20                     # 正文上限 1 MiB，同上
IDLE_SEC = 10                          # 一条连接干等 10 秒没动静就断它：一次只接一条，不能吊死
SRV = {"port": None, "sock": None, "conn": None, "peer": "", "req": None, "done": False}

def _drop_conn():
    c = SRV["conn"]
    SRV["conn"] = None
    if c:
        try:
            c.shutdown(socket.SHUT_RDWR)
        except OSError:
            pass
        try:
            c.close()
        except OSError:
            pass

def _recv_some(c):
    try:
        return c.recv(4096)
    except socket.timeout:
        return b""           # 干等不是请求：当成「这条连接到此为止」
    except OSError:
        return b""

def _read_request(conn):
    """读到一个完整请求，返回「一字节一字符」的那一段。行分隔只认 CRLF：
       对端断了、超时了、或超过 HDR_MAX / BODY_MAX —— 断这条连接，返回 None，不报错误码。"""
    buf = b""
    while True:
        cut = buf.find(HDR_END.encode("latin-1"))
        if cut >= 0:
            break
        if len(buf) >= HDR_MAX:
            _drop_conn(); return None
        ch = _recv_some(conn)
        if not ch:
            _drop_conn(); return None
        buf += ch
    head = buf[:cut].decode("latin-1")
    body = buf[cut + len(HDR_END):]
    want = _content_length(head)
    if want < 0 or want > BODY_MAX:
        _drop_conn(); return None
    while len(body) < want:
        ch = _recv_some(conn)
        if not ch:
            _drop_conn(); return None
        body += ch
    return head + HDR_END + body[:want].decode("latin-1")

def _content_length(head):
    """只认「Content-Length: 数字」这一种写法（大小写敏感，不认 content-length）。
       数字畸形或超大返回 -1，由调用方按"断这条连接"处理，不报错码。"""
    for ln in head.split("\r\n")[1:]:
        if ln.startswith("Content-Length:"):
            d = ln.split(":", 1)[1].strip()
            return int(d) if d.isdigit() else -1
    return 0

def B_serve(args, line):
    """serve() 不接参数：端口只有命令行那一个来源（--serve <port>），程序里没有第二个口子。
       先挑程序自己的错（handle 在不在、接不接参数），再看端口 —— 和 reply 同一个次序。"""
    handler = TOP_ENV.get("handle") if TOP_ENV else None
    if not isinstance(handler, Fn):
        raise DropError("E1", line, "serve 要程序里有一个 fn handle() { ... }，没绑过 handle")
    if handler.params:
        raise DropError("E2", line, "处理函数 handle 不接参数，这里声明了 %d 个" % len(handler.params))
    port = SRV["port"]
    if port is None:
        raise DropError("E14", line, "端口只能由命令行给：python3 drop.py --serve <port> 程序.dr")
    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        sock.bind(("0.0.0.0", port))
        sock.listen(16)
    except OSError as e:
        raise DropError("E14", line, "绑不上端口 %d：%s" % (port, e.strerror or e))
    SRV["sock"] = sock
    _arm_quit()
    sys.stderr.write("drop 在听 :%d（Ctrl-C 停）\n" % port)
    sys.stderr.flush()
    while True:
        try:
            conn, addr = sock.accept()
        except OSError as e:
            _drop_conn()
            raise DropError("E14", line, "接不上： %s" % (e.strerror or e))
        SRV["conn"] = conn
        conn.settimeout(IDLE_SEC)
        SRV["peer"] = "%s:%d" % (addr[0], addr[1])
        while True:                       # 同一条连接上一个个请求（keep-alive）
            got = _read_request(conn)
            if got is None:
                break                     # 对端断了，或超过上限：这条连接收工
            SRV["req"] = got
            SRV["done"] = False
            TOP_ENV.set("request", got)
            TOP_ENV.set("peer", SRV["peer"])
            call(handler, [], line)       # 返回值不参与协议：断不断只看对端的 Connection 头
            if SRV["conn"] is not conn:
                break

def B_reply(args, line):
    status, body = args
    conn = SRV["conn"]
    # 先挑程序自己的错（该整数、该字符串），再看「现在有没有一个请求」—— 前者换个环境也一样错。
    if not is_int(status):
        raise DropError("E9", line, "reply 的状态码要整数，给的是 %s" % kind(status))
    raw = _wire(body, "reply", line)     # 和 bytes(body) 同一条路：正文出去的就是那些字节
    if SRV["req"] is None:
        raise DropError("E14", line, "reply 只能在一次请求里调用：还没接见过任何请求")
    if SRV["done"]:
        raise DropError("E14", line, "这个请求已经回过了 —— 一次请求只有一个响应")
    head_only = SRV["req"].split("\r\n", 1)[0].startswith("HEAD ")
    head = SRV["req"].split(HDR_END, 1)[0]
    reason = {200: "OK", 201: "Created", 204: "No Content", 400: "Bad Request",
              404: "Not Found", 405: "Method Not Allowed", 500: "Internal Server Error"}
    txt = "HTTP/1.1 %d %s\r\n" % (status, reason.get(status, "Drop"))
    txt += "Date: %s\r\n" % time.strftime("%a, %d %b %Y %H:%M:%S GMT", time.gmtime())
    txt += "Content-Type: text/plain; charset=utf-8\r\n"
    txt += "Content-Length: %d\r\n" % (0 if head_only else len(raw))
    close = "Connection: close" in head.split("\r\n")[1:]
    if close:
        txt += "Connection: close\r\n"   # 只在这一种情况下多出第五行：告诉对端真的会关
    try:
        conn.sendall(txt.encode("latin-1") + b"\r\n" + (b"" if head_only else raw))
    except OSError as e:
        SRV["done"] = True
        _drop_conn()
        raise DropError("E14", line, "发不出去：%s" % (e.strerror or e))
    SRV["done"] = True
    if close:
        _drop_conn()
    return None

def _arm_quit():
    """Ctrl-C 就停，不给 Python 堆栈；被 kill 时也不要把宿主 OOM 分留给这个进程。"""
    try:
        with open("/proc/self/oom_score_adj", "w") as f:
            f.write("1000")
    except OSError:
        pass
    def quit(signum, frame):
        try:
            sys.stdout.flush()
        except Exception:
            pass
        os._exit(0)
    signal.signal(signal.SIGINT, quit)

BUILTINS = {"print": (B_print, 1), "len": (B_len, 1), "str": (B_str, 1), "num": (B_num, 1),
            "bytes": (B_bytes, 1), "split": (B_split, 2), "join": (B_join, 2),
            "serve": (B_serve, 0), "reply": (B_reply, 2)}
RESERVED = set(BUILTINS) | {"let", "fn", "if", "request", "peer"} | set(NO_LOOP) | set(SUGAR)

_depth = [0]
TOP_ENV = None

def call(f, args, line):
    """非尾调用：涨一层。f 只可能是 Fn —— 函数不是值，内置走名字那条路。"""
    if isinstance(f, Fn):
        if len(args) != len(f.params):
            raise DropError("E2", line, "%s 收 %d 个，给了 %d 个" % (f.name, len(f.params), len(args)))
        if _depth[0] >= MAX_CALL_DEPTH:
            raise DropError("E13", line, "%s 往里缩到第 %d 层 —— 非尾调用的上限是 %d 层"
                            % (f.name, _depth[0] + 1, MAX_CALL_DEPTH))
        _depth[0] += 1
        try:
            return run_call(f, args)
        finally:
            _depth[0] -= 1
    raise DropError("E4", line, "%s 不能当函数调用" % kind(f))

def _tail_of(item, name):
    """这一句是不是「在尾位调 name」：直接是 → ('jump', 实参)；是 if → ('if', 节点)；都不是 → None。"""
    if item[0] != "expr":
        return None
    e = item[1]
    if e[0] == "call" and e[1][0] == "name" and e[1][1] == name:
        return ("jump", e[2])
    if e[0] == "if":
        return ("if", e)
    return None

def run_call(f, args):
    """函数体只有块这一种写法；块里最后一句如果是对自己的调用，就换绑参数再跳回去，不涨层数。
       最后一句是 if 时，两支里的自身调用都算尾位。"""
    e = Env(f.env)
    for k, v in zip(f.params, args):
        e.set(k, v)
    items = f.body[1]
    last = len(items) - 1
    while True:
        t = _tail_of(items[last], f.name) if last >= 0 else None
        upto = last if t is None else last - 1
        val = None
        for it in items[:upto + 1]:
            val = ev_item(it, e)
        if t is None:
            return val
        if t[0] == "if":
            node = t[1]
            c = ev(node[1], e)
            if not isinstance(c, bool):
                raise DropError("E7", node[4], "if 的条件要 true/false，给的是 %s" % kind(c))
            branch = node[2] if c else node[3]
            if branch[0] == "call" and branch[1][0] == "name" and branch[1][1] == f.name:
                vals = [ev(a, e) for a in branch[2]]
                if len(vals) != len(f.params):
                    raise DropError("E2", branch[3], "%s 收 %d 个，给了 %d 个"
                                    % (f.name, len(f.params), len(vals)))
                for k, v in zip(f.params, vals):
                    e.set(k, v)
                continue
            return ev(branch, e)
        vals = [ev(a, e) for a in t[1]]
        if len(vals) != len(f.params):
            raise DropError("E2", items[last][1][3], "%s 收 %d 个，给了 %d 个"
                            % (f.name, len(f.params), len(vals)))
        for k, v in zip(f.params, vals):
            e.set(k, v)

def run_body(body, env):
    """块当表达式用：一句接一句，值 = 最后一句；{} 的值是无。这里不当尾位看（不是函数体）。"""
    val = None
    for it in body[1]:
        val = ev_item(it, env)
    return val

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
            v = env.get(nm)
            if isinstance(v, Fn):
                raise DropError("E3", line, "函数不是值：%s 只能按名字调用，不能当值用" % nm)
            if v is NO_REQUEST:
                raise DropError("E14", line, "%s 要等接到第一个请求才有值" % nm)
            return v
        if nm in BUILTINS:
            raise DropError("E3", line, "%s 是内置名，只能调用它，不能把它当值用" % nm)
        raise DropError("E1", line, "没绑过 %r" % nm)
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
        nm, line = node[1][1], node[3]           # 语法上保证被调的必须是名字
        if nm in SUGAR:
            raise DropError("E3", line,
                            "没有 %s：迭代只有「函数在尾位调用自己」这一种写法，自己写一个具名函数" % nm)
        args = [ev(a, env) for a in node[2]]
        if env.has(nm):
            return call(env.get(nm), args, line)
        if nm in BUILTINS:
            fn, n = BUILTINS[nm]
            if len(args) != n:
                raise DropError("E2", line, "%s 收 %d 个，给了 %d 个" % (nm, n, len(args)))
            return fn(args, line)
        raise DropError("E1", line, "没绑过 %r，调不了" % nm)
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
    global TOP_ENV
    env = Env()
    env.set("request", NO_REQUEST)
    env.set("peer", NO_REQUEST)
    TOP_ENV = env
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
    if SRV["port"] is not None and "serve(" not in text:
        sys.stderr.write("挂：第 0 行 E14 —— 命令行给了 --serve，程序里没有 serve，这个端口没人接\n")
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
    ('E1', 'fn f() { nope() }\nf()'),
    ('E2', 'fn f(a) { a }\nprint(str(f(1, 2)))'),
    ('E2', 'print(str(len("a", "b")))'),
    ('E2', 'serve(1)'),
    ('E3', 'let x = )'),
    ('E3', 'fn f(a, b) { a + b }\nprint(str(f(1 2)))'),
    ('E3', 'let x = "a\\q"'),
    ('E3', 'let x = "a\\xZZ"'),
    ('E3', 'let len = 1'),
    ('E3', 'fn f(if) { 1 }'),
    ('E3', 'fn f(a, len) { a }'),
    ('E3', 'fn f(map, xs) { xs }'),
    ('E3', 'x = 1'),
    ('E3', 'print(str(1.5))'),
    ('E3', 'let x = 1;'),
    ('E3', 'fn f(a) = a'),
    ('E3', 'print(str(if(true, 1)))'),
    ('E3', 'print(str(if(true, 1, 2, 3)))'),
    ('E3', '/* 块注释 */\nprint("a")'),
    ('E3', 'let f = fn(x) { x }'),
    ('E3', 'fn f() { 1 }\nprint(str([f][0]))'),
    ('E3', 'while(true, 1)'),
    ('E3', 'for(1, 2, 3)'),
    ('E3', 'fn f(n) { if(n <= 0, 0, f(n - 1)) continue }'),
    ('E3', 'print(str(range(3)))'),
    ('E3', 'fn map(f, xs) { xs }\nprint(str(map(len, [1])))'),
    ('E3', 'serve()\nprint("到不了")'),
    ('E4', 'let x = 1\nx(2)'),
    ('E3', 'fn add(a, b) { a + b }\nlet f = add'),
    ('E5', 'let x = 1 + "a"'),
    ('E5', 'let x = [1] + "a"'),
    ('E5', 'let x = 1 - true'),
    ('E5', 'let x = "a" < 1'),
    ('E5', 'let x = true < false'),
    ('E5', 'let x = -"a"'),
    ('E5', 'print(str(len(1)))'),
    ('E6', 'print(str([1][3]))'),
    ('E6', 'print(str([1, 2, 3][-1]))'),
    ('E7', 'print(str(if(1, 2, 3)))'),
    ('E7', 'print(str(!1))'),
    ('E7', 'print(str(1 && true))'),
    ('E8', 'print(str(1 / 0))'),
    ('E8', 'print(str(1 % 0))'),
    ('E9', 'print(str(num("1a")))'),
    ('E9', 'print(str(num(" -42 ")))'),
    ('E9', 'reply("200", "x")'),
    ('E10', 'print(str(num(1)))'),
    ('E10', 'print(1)'),
    ('E10', 'print(str(join("-", [1])))'),
    ('E10', 'print(str(join(1, ["a"])))'),
    ('E10', 'print(str(split(1, ",")))'),
    ('E10', 'print(str(split("a", "")))'),
    ('E10', 'print(str(bytes(1)))'),
    ('E10', 'reply(200, 1)'),
    ('E11', 'let x = "abc'),
    ('E11', 'print(str(1)'),
    ('E11', 'let x = "a\nb"'),
    ('E11', 'fn f(a) { a'),
    ('E11', 'let xs = [1, 2'),
    ('E12', 'fn f(a, a) { a }'),
    ('E13', 'fn deep(n) { if(n <= 0, 0, 1 + deep(n - 1)) }\nprint(str(deep(201)))'),
    ('E1', 'serve()'),
    ('E2', 'fn handle(x) { x }\nserve()'),
    ('E14', 'fn handle() { reply(200, "ok") }\nserve()'),
    ('E14', 'print(request)'),
    ('E14', 'print(peer)'),
    ('E14', 'reply(200, "x")'),
]

# 干净样本：证明「不红」这条路也通 —— 语言能做的事里挑一件最小的
CLEAN_SRC = 'fn add(a, b) { a + b }\nprint(str(add(1, 2)))\n'

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
    clean = CLEAN_SRC
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
    rest = []
    i = 0
    while i < len(argv):
        a = argv[i]
        if a == "--serve":
            if i + 1 >= len(argv):
                sys.stderr.write("挂：--serve 后面要跟一个端口号\n")
                return 2
            d = argv[i + 1]
            if not (d.isdigit() and 1 <= int(d) <= 65535):
                sys.stderr.write("挂：--serve 的端口要是 1..65535 的整数，给的是 %r\n" % d)
                return 2
            if SRV["port"] is not None:
                sys.stderr.write("挂：--serve 只许给一次，端口只能有一个\n")
                return 2
            SRV["port"] = int(d)
            i += 2
            continue
        rest.append(a)
        i += 1
    argv = rest
    if not argv:
        sys.stderr.write("用法: python3 drop.py [--serve <端口>] 程序.dr | --self-check | --self-test\n")
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
