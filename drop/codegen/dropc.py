#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""dropc —— 第一个编译器（PoC）：把定义的一个子集编成裸 x86-64 ELF，只走 syscall。

  python3 dropc.py 程序.dr [-o 输出] [--keep-asm]

子集（明确的样子，别的一律拒绝，退出码 2）：
  fn 名(形参) { … }         函数、尾位的自身调用（跳回开头，不涨栈）
  let 名 = 表达式
  print(str(整数表达式))    print(字符串字面量)    print(str(if(c, "甲", "乙")))
  整数、+ - * / %、比较、&& || !、一元 -、if(c, a, b)、函数调用
  类型只做一件事：**拒绝把布尔当整数打**（定义里 str(false) 是 "false"，不是 0），
  也拒绝拿整数当条件（定义要 true/false）。说不准的地方（形参、未定的返回类型）放过 ——
  宁可漏，也不拿猜出来的类型去改语义。
不在子集里的东西（表、字符串运算、嵌套块、闭包、宿主）会当场拒绝，并说出是第几行 ——
编译器不许猜；猜出来的东西，第二个平台就会给出另一个答案。

产物：静态 ELF，入口 _start，没有解释器、没有 libc、没有动态链接。跑的时候只发生 write/exit。
工具链：as + ld（binutils）只做「汇编文本 → 字节」这一段搬砖，语义与运行时全部在这里。
"""
import os
import re
import subprocess
import sys
import tempfile

# ------------------------------------------------------------------ 词法
TOK = re.compile(r"""
    (?P<ws>[ \t\r\n]+)
  | (?P<comment>//[^\n]*)
  | (?P<num>\d+)
  | (?P<name>[A-Za-z_\u4e00-\u9fff][A-Za-z0-9_\u4e00-\u9fff]*)
  | (?P<str>"(?:\\.|[^"\\])*")
  | (?P<op>==|!=|<=|>=|&&|\|\||[-+*/%<>()\[\]{},:=!])
""", re.X)


def lex(src):
    toks, line, i = [], 1, 0
    while i < len(src):
        m = TOK.match(src, i)
        if not m:
            die(line, "子集之外：这个字符不认识 %r" % src[i])
        t = m.lastgroup
        v = m.group()
        if t == "ws":
            pass
        elif t == "comment":
            pass
        else:
            toks.append((t, v, line))
        line += v.count("\n")
        i = m.end()
    toks.append(("eof", "", line))
    return toks


def die(line, why):
    sys.stderr.write("挂：第 %d 行 %s\n" % (line, why))
    sys.exit(2)


# ------------------------------------------------------------------ 语法（AST 就是元组）
class P:
    def __init__(self, toks):
        self.t, self.i = toks, 0

    def peek(self, k=0):
        return self.t[min(self.i + k, len(self.t) - 1)]

    def take(self, v=None):
        t = self.t[self.i]
        if v is not None and t[1] != v:
            die(t[2], "子集里这里要 %r，给的是 %r" % (v, t[1]))
        self.i += 1
        return t

    def at(self, v):
        return self.peek()[1] == v

    def program(self):
        fns, main = [], []
        while self.peek()[0] != "eof":
            if self.at("fn"):
                self.take()
                name = self.take()[1]
                self.take("(")
                params = []
                if not self.at(")"):
                    params.append(self.take()[1])
                    while self.at(","):
                        self.take()
                        params.append(self.take()[1])
                self.take(")")
                body = self.block()
                fns.append((name, params, body))
            else:
                main.append(self.stmt())
        return fns, main

    def block(self):
        self.take("{")
        out = []
        while not self.at("}"):
            out.append(self.stmt())
        self.take("}")
        return out

    def stmt(self):
        if self.at("let"):
            self.take()
            name = self.take()[1]
            self.take("=")
            return ("let", name, self.expr())
        if self.at("fn"):
            die(self.peek()[2], "子集之外：块里定义局部函数（嵌套）")
        return ("expr", self.expr())

    def expr(self):
        return self.binary(0)

    BIN = [("||",), ("&&",), ("==", "!="), ("<", "<=", ">", ">="), ("+", "-"), ("*", "/", "%")]

    def binary(self, lvl):
        if lvl == len(self.BIN):
            return self.unary()
        e = self.binary(lvl + 1)
        while self.peek()[1] in self.BIN[lvl]:
            op = self.take()[1]
            e = ("bin", op, e, self.binary(lvl + 1))
        return e

    def unary(self):
        if self.at("-"):
            self.take()
            return ("neg", self.unary())
        if self.at("!"):
            self.take()
            return ("not", self.unary())
        return self.postfix()

    def postfix(self):
        e = self.atom()
        while self.at("("):
            if e[0] != "name":
                die(self.peek()[2], "子集之外：只有具名函数能被调用")
            self.take()
            args = []
            if not self.at(")"):
                args.append(self.expr())
                while self.at(","):
                    self.take()
                    args.append(self.expr())
            self.take(")")
            e = ("call", e[1], args, e[2] if e[0] == "name" else 0)
        return e

    def atom(self):
        t, v, ln = self.peek()
        if t == "num":
            self.take()
            return ("int", int(v))
        if t == "str":
            self.take()
            return ("str", unesc(v))
        if v in ("true", "false"):
            self.take()
            return ("bool", 1 if v == "true" else 0)
        if v == "if":
            self.take()
            self.take("(")
            c = self.expr()
            self.take(",")
            a = self.expr()
            self.take(",")
            b = self.expr()
            self.take(")")
            return ("if", c, a, b, ln)
        if v == "(":
            self.take()
            e = self.expr()
            self.take(")")
            return e
        if v == "{":
            die(ln, "子集之外：块当表达式用")
        if t == "name":
            self.take()
            return ("name", v, ln)
        die(ln, "子集之外：这里出现 %r" % v)


def unesc(s):
    body, out, i = s[1:-1], [], 0
    while i < len(body):
        c = body[i]
        if c != "\\":
            out.append(c)
            i += 1
            continue
        n = body[i + 1]
        out.append({"n": "\n", "t": "\t", "\\": "\\", '"': '"'}.get(n, n))
        i += 2
    return "".join(out)


# ------------------------------------------------------------------ 代码生成
class Gen:
    def __init__(self):
        self.text = []
        self.rodata = []
        self.n = 0
        self.fn = None          # 当前函数名
        self.nparams = 0
        self.slots = {}         # 名字 -> 帧内偏移
        self.types = {}         # 名字 -> int / bool / str / any（静态，只用来拒绝「拿布尔当整数打」）
        self.fnret = {}         # 函数名 -> 返回类型（any = 说不准）
        self.nslots = 0

    def label(self, p):
        self.n += 1
        return ".L%s%d" % (p, self.n)

    def emit(self, s):
        self.text.append("    " + s)

    def label_at(self, l):
        self.text.append(l + ":")

    def str_lit(self, s):
        self.n += 1
        name = ".Lstr%d" % self.n
        bs = ",".join(str(b) for b in s.encode("utf-8"))
        self.rodata.append("%s: .byte %s" % (name, bs))
        return name, len(s.encode("utf-8"))

    def write_lit(self, s):
        name, n = self.str_lit(s + "\n")
        self.emit("movl $1, %edi")
        self.emit("movq $%s, %%rsi" % name)
        self.emit("movl $%d, %%edx" % n)
        self.emit("movl $1, %eax")
        self.emit("syscall")

    # ---- 函数
    def function(self, name, params, body):
        self.fn, self.nparams = name, len(params)
        self.slots = {p: 16 + 8 * (len(params) - 1 - i) for i, p in enumerate(params)}
        self.types = {p: "any" for p in params}
        self.nslots = 0
        saved, self.text = self.text, []
        term = self.stmts(body, tail=True)
        if not term:
            self.emit("movq %rbp, %rsp")
            self.emit("popq %rbp")
            self.emit("ret")
        inner, self.text = self.text, saved
        self.text.append(".Lfn_%s:" % name)
        self.emit("pushq %rbp")
        self.emit("movq %rsp, %rbp")
        if self.nslots:
            self.emit("subq $%d, %%rsp" % (8 * self.nslots))
        self.text.append(".Lbody_%s:" % name)
        self.text.extend(inner)
        self.slots, self.types, self.nslots, self.fn = {}, {}, 0, None

    def stmts(self, body, tail):
        """生成一串语句；最后一句在 tail 位。返回「这条路已经 jmp 走了」= True。"""
        for i, st in enumerate(body):
            last = i == len(body) - 1
            if st[0] == "let":
                term = self.expr(st[2])
                self.nslots += 1
                self.slots[st[1]] = -8 * self.nslots
                self.types[st[1]] = self.type_of(st[2])
                self.emit("movq %%rax, %d(%%rbp)" % self.slots[st[1]])
            elif st[1][0] == "call" and st[1][1] == "print":
                if len(st[1][2]) != 1:
                    die(0, "子集之外：print 只吃一个实参")
                self.print_(st[1][2][0])
                term = False
            else:
                term = self.expr(st[1], tail and last)
            if term and not last:
                die(0, "子集之外：尾调用后面还有语句")
        return term if body else False

    # ---- 表达式：值一律放在 %rax
    def expr(self, e, tail=False):
        k = e[0]
        if k == "bool":
            self.emit("movq $%d, %%rax" % e[1])
            return False
        if k == "int":
            self.emit("%s $%d, %%rax" % ("movabsq" if abs(e[1]) > 2 ** 31 - 1 else "movq", e[1]))
            return False
        if k == "name":
            if e[1] not in self.slots:
                die(e[2], "子集之外/未绑定：%r 不在本函数的形参与 let 里" % e[1])
            self.emit("movq %d(%%rbp), %%rax" % self.slots[e[1]])
            return False
        if k == "neg":
            self.expr(e[1])
            self.emit("negq %rax")
            return False
        if k == "not":
            self.expr(e[1])
            self.emit("testq %rax, %rax")
            self.emit("sete %al")
            self.emit("movzbq %al, %rax")
            return False
        if k == "bin":
            return self.binop(e)
        if k == "if":
            return self.ife(e, tail)
        if k == "call":
            return self.call(e, tail)
        die(0, "子集之外：表达式 %r" % (k,))

    def binop(self, e):
        op, a, b = e[1], e[2], e[3]
        if op in ("&&", "||"):
            l_short, l_end = self.label("log"), self.label("log")
            self.expr(a)
            self.emit("testq %rax, %rax")
            self.emit(("jz " if op == "&&" else "jnz ") + l_short)
            self.expr(b)
            self.emit("testq %rax, %rax")
            self.emit("setne %al")
            self.emit("movzbq %al, %rax")
            self.emit("jmp %s" % l_end)
            self.label_at(l_short)
            self.emit("movq $%d, %%rax" % (0 if op == "&&" else 1))
            self.label_at(l_end)
            return False
        self.expr(a)
        self.emit("pushq %rax")
        self.expr(b)
        self.emit("popq %rcx")
        if op == "+":
            self.emit("addq %rcx, %rax")
        elif op == "-":
            self.emit("movq %rax, %rdx")
            self.emit("movq %rcx, %rax")
            self.emit("subq %rdx, %rax")
        elif op == "*":
            self.emit("imulq %rcx, %rax")
        elif op in ("/", "%"):
            self.emit("movq %rax, %r8")
            self.emit("movq %rcx, %rax")
            self.emit("cqto")
            self.emit("idivq %r8")
            if op == "%":
                self.emit("movq %rdx, %rax")
        else:
            self.emit("cmpq %rax, %rcx")
            self.emit({"==": "sete", "!=": "setne", "<": "setl", "<=": "setle",
                       ">": "setg", ">=": "setge"}[op] + " %al")
            self.emit("movzbq %al, %rax")
        return False

    def ife(self, e, tail):
        _, c, a, b, ln = e
        ct = self.type_of(c)
        if ct in ("int", "str"):
            die(ln, "子集之外：if 的条件是 %s —— 定义要 true/false（比值和 && || ! 给的是布尔）" % ct)
        l_else, l_end = self.label("if"), self.label("if")
        self.expr(c)
        self.emit("testq %rax, %rax")
        self.emit("jz %s" % l_else)
        ta = self.expr(a, tail)
        if not ta:
            self.emit("jmp %s" % l_end)
        self.label_at(l_else)
        tb = self.expr(b, tail)
        if not (ta and tb):
            self.label_at(l_end)
        return ta and tb

    def call(self, e, tail):
        name, args = e[1], e[2]
        if name == "print":
            die(0, "子集之外：print 只能当语句用")
        if tail and name == self.fn and len(args) == self.nparams:
            for a in args:
                self.expr(a)
                self.emit("pushq %rax")
            for i in reversed(range(len(args))):
                self.emit("popq %rax")
                self.emit("movq %%rax, %d(%%rbp)" % (16 + 8 * (self.nparams - 1 - i)))
            self.emit("jmp .Lbody_%s" % name)
            return True
        for a in args:
            self.expr(a)
            self.emit("pushq %rax")
        self.emit("call .Lfn_%s" % name)
        if args:
            self.emit("addq $%d, %%rsp" % (8 * len(args)))
        return False

    def type_of(self, e):
        """子集里的静态类型：只用来拒绝「拿布尔当整数打 / 拿整数当条件」。
        any = 说不准（形参、还没算出来的函数），说不准就放过 —— 但绝不用猜出来的类型去改语义。"""
        if e is None:
            return "any"
        k = e[0]
        if k in ("int", "bool", "str"):
            return k
        if k == "name":
            return self.types.get(e[1], "any")
        if k == "neg":
            return "int"
        if k == "not":
            return "bool"
        if k == "bin":
            return "bool" if e[1] in ("==", "!=", "<", "<=", ">", ">=", "&&", "||") else "int"
        if k == "if":
            a, b = self.type_of(e[2]), self.type_of(e[3])
            if a == b:
                return a
            return b if a == "any" else (a if b == "any" else "any")
        if k == "call":
            if e[1] == "str":
                return "str"
            if e[1] == "print":
                return "none"
            return self.fnret.get(e[1], "any")
        return "any"

    def print_(self, arg):
        if arg[0] == "str":
            self.write_lit(arg[1])
            return
        if arg[0] == "call" and arg[1] == "str" and len(arg[2]) == 1:
            inner = arg[2][0]
            it = self.type_of(inner)
            if it == "bool":
                die(arg[3] if len(arg) > 3 else 0, "子集之外：str(布尔) 在定义里是 \"true\"/\"false\"，子集还没实现布尔转文本 —— 拒绝，不许印成 1/0")
            if inner[0] == "if" and inner[2][0] == "str" and inner[3][0] == "str":
                l_else, l_end = self.label("pf"), self.label("pf")
                self.expr(inner[1])
                self.emit("testq %rax, %rax")
                self.emit("jz %s" % l_else)
                self.write_lit(inner[2][1])
                self.emit("jmp %s" % l_end)
                self.label_at(l_else)
                self.write_lit(inner[3][1])
                self.label_at(l_end)
                return
            if inner[0] == "str":
                self.write_lit(inner[1])
                return
            self.expr(inner)
            self.emit("call .Lrt_itox")
            return
        die(0, "子集之外：print 的参数只有 str(整数)、字符串字面量、str(if(c, \"甲\", \"乙\"))")


RUNTIME = r"""
.Lrt_itox:                      # %rax = 有符号整数 -> 十进制 + 换行，直接 write(1, …)
    leaq .Lbuf+32(%rip), %rsi
    decq %rsi
    movb $10, (%rsi)
    movq %rax, %r8
    xorl %r10d, %r10d
    testq %r8, %r8
    jns 1f
    negq %r8
    movl $1, %r10d
1:  movl $10, %r9d
2:  xorl %edx, %edx
    movq %r8, %rax
    divq %r9
    addl $48, %edx
    decq %rsi
    movb %dl, (%rsi)
    movq %rax, %r8
    testq %r8, %r8
    jnz 2b
    testl %r10d, %r10d
    jz 3f
    decq %rsi
    movb $45, (%rsi)
3:  leaq .Lbuf+32(%rip), %rdx
    subq %rsi, %rdx
    movl $1, %edi
    movl $1, %eax
    syscall
    ret
"""


def build(fns, main):
    g = Gen()
    # 先算一遍每个函数返回什么类型：两遍足够让互相调用的一圈也定下来（定不下来就留 any）
    for name, _, _ in fns:
        g.fnret[name] = "any"
    for _ in range(2):
        for name, params, body in fns:
            g.types = {p: "any" for p in params}
            for st in body:
                if st[0] == "let":
                    g.types[st[1]] = g.type_of(st[2])
            tail = body[-1][1] if body and body[-1][0] == "expr" else None
            g.fnret[name] = g.type_of(tail)
    g.types = {}
    for name, params, body in fns:
        g.function(name, params, body)
    g.function("main", [], main)
    out = [".text", ".global _start", "_start:",
           "    xorl %ebp, %ebp",
           "    andq $-16, %rsp",
           "    call .Lfn_main",
           "    movl $60, %eax",
           "    xorl %edi, %edi",
           "    syscall"]
    out += g.text
    out += RUNTIME.strip("\n").splitlines()
    if g.rodata:
        out += [".section .rodata"] + g.rodata
    out += [".section .bss", ".align 16", ".Lbuf: .skip 64"]
    return "\n".join(out) + "\n"


def main_cli(argv):
    if not argv:
        sys.stderr.write("用法: python3 dropc.py 程序.dr [-o 输出] [--keep-asm]\n")
        return 2
    src_path, out_path, keep = argv[0], "a.out", False
    if "-o" in argv:
        out_path = argv[argv.index("-o") + 1]
    keep = "--keep-asm" in argv
    src = open(src_path, encoding="utf-8").read()
    fns, main = P(lex(src)).program()
    asm = build(fns, main)
    with tempfile.TemporaryDirectory() as td:
        s = os.path.join(td, "a.s")
        o = os.path.join(td, "a.o")
        open(s, "w", encoding="utf-8").write(asm)
        subprocess.run(["as", "--64", "-o", o, s], check=True)
        subprocess.run(["ld", "-o", out_path, o], check=True)
        if keep:
            open(os.path.splitext(out_path)[0] + ".s", "w", encoding="utf-8").write(asm)
    return 0


if __name__ == "__main__":
    sys.exit(main_cli(sys.argv[1:]))
