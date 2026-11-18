// dropc —— drop 的编译器（C11 写，单个文件）。产物是**一个能直接跑的 ELF**：
// 自己排 ELF 头、自己编机器码，不经过 as、不经过 ld、不经过任何宿主语言。
//
//   cc -O2 -o dropc dropc.c
//   ./dropc 程序.dr [-o 输出]
//
// 子集：fn（含尾位的自身调用）/ let / 整数与 + - * / %、比较、&& || !、一元 -、
//       if(c,a,b)、函数调用、print(str(整数))、print("字面量")、print(str(if(c,"甲","乙")))。
// 子集之外一律拒绝，退出码 2，并说清是第几行：编译器不许猜。
//
// 目标：x86-64 Linux（Ubuntu 上直接 ./产物）。运行时只发生 write(2) 与 exit(2)；
// 没有 libc、没有动态链接、没有解释器。两个调用号（1 写、60 退出）从 3.x 到 7.0 没变过。
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <stdarg.h>
#include <sys/stat.h>

// ---------------------------------------------------------------- 出错
static void die(int line, const char *fmt, ...) {
    va_list ap;
    fprintf(stderr, "挂：第 %d 行 ", line);
    va_start(ap, fmt);
    vfprintf(stderr, fmt, ap);
    va_end(ap);
    fputc('\n', stderr);
    exit(2);
}

// ---------------------------------------------------------------- 词法
enum { TK_EOF, TK_INT, TK_STR, TK_NAME, TK_PUNCT };

typedef struct {
    int kind, line;
    long long num;
    char *s;      // 名字 / 标点 / 字符串的**字节**（已解转义）
    int slen;
} Tok;

static Tok *toks;
static int ntoks, tpos;

static int is_name_start(unsigned char c) { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || c >= 0x80; }
static int is_name_char(unsigned char c) { return is_name_start(c) || (c >= '0' && c <= '9'); }

static void lex(const char *src) {
    int i = 0, line = 1;
    toks = calloc(strlen(src) + 2, sizeof(Tok));
    while (src[i]) {
        unsigned char c = (unsigned char)src[i];
        if (c == ' ' || c == '\t' || c == '\r' || c == '\n') {
            if (c == '\n') line++;
            i++;
            continue;
        }
        if (c == '/' && src[i + 1] == '/') {
            while (src[i] && src[i] != '\n') i++;
            continue;
        }
        Tok *t = &toks[ntoks++];
        t->line = line;
        if (c >= '0' && c <= '9') {
            long long v = 0;
            while (src[i] >= '0' && src[i] <= '9') v = v * 10 + (src[i++] - '0');
            t->kind = TK_INT;
            t->num = v;
        } else if (is_name_start(c)) {
            int s = i;
            while (is_name_char((unsigned char)src[i])) i++;
            t->kind = TK_NAME;
            t->s = strndup(src + s, i - s);
        } else if (c == '"') {
            char *buf = malloc(strlen(src) + 1);
            int n = 0;
            i++;
            while (src[i] && src[i] != '"') {
                if (src[i] == '\\') {
                    char e = src[i + 1];
                    if (e == 'n') { buf[n++] = '\n'; i += 2; }
                    else if (e == 't') { buf[n++] = '\t'; i += 2; }
                    else if (e == '\\' || e == '"') { buf[n++] = e; i += 2; }
                    else if (e == 'x') {
                        int hi = src[i + 2], lo = src[i + 3];
                        int v = (hi <= '9' ? hi - '0' : (hi | 32) - 'a' + 10) * 16
                              + (lo <= '9' ? lo - '0' : (lo | 32) - 'a' + 10);
                        buf[n++] = (char)v;
                        i += 4;
                    } else die(line, "子集之外：转义 \\%c 不认识", e);
                } else buf[n++] = src[i++];
            }
            if (src[i] != '"') die(line, "字符串没闭合");
            i++;
            t->kind = TK_STR;
            t->s = buf;
            t->slen = n;
        } else {
            static const char *ops[] = {"==", "!=", "<=", ">=", "&&", "||",
                                        "+", "-", "*", "/", "%", "<", ">", "=", "!",
                                        "(", ")", "{", "}", "[", "]", ",", ":", NULL};
            int k = 0;
            for (; ops[k]; k++) {
                int L = (int)strlen(ops[k]);
                if (!strncmp(src + i, ops[k], L)) { t->kind = TK_PUNCT; t->s = strndup(ops[k], L); i += L; break; }
            }
            if (!ops[k]) die(line, "子集之外：这个字符不认识 '%c'", c);
        }
    }
    toks[ntoks].kind = TK_EOF;
    toks[ntoks].line = line;
}

// ---------------------------------------------------------------- 语法
enum { NK_INT, NK_BOOL, NK_STR, NK_NAME, NK_NEG, NK_NOT, NK_BIN, NK_IF, NK_CALL };

typedef struct Node Node;
struct Node {
    int kind, line;
    long long num;
    char *name;          // NAME / CALL 的名字，BIN 的运算符
    char *str; int slen; // STR
    Node *a, *b, *c;
    Node **args; int nargs;
};

typedef struct { int kind; char *name; Node *e; } Stmt;   // 0 = let，1 = 表达式
typedef struct { char *name; char **params; int nparams; Stmt *body; int nbody; } Fn;

static Fn *fns; static int nfns;
static Stmt *main_body; static int nmain;

static Tok *peek(int k) { return &toks[tpos + k < ntoks ? tpos + k : ntoks]; }
static int at(const char *v) { Tok *t = peek(0); return (t->kind == TK_PUNCT || t->kind == TK_NAME) && t->s && !strcmp(t->s, v); }
static Tok *take(const char *v) {
    Tok *t = peek(0);
    if (v && !(t->s && !strcmp(t->s, v))) die(t->line, "子集里这里要 '%s'，给的是 '%s'", v, t->s ? t->s : "<结束>");
    tpos++;
    return t;
}

static Node *parse_expr(void);
static Stmt *parse_block(int *n);

static Node *node(int kind, int line) {
    Node *n = calloc(1, sizeof(Node));
    n->kind = kind;
    n->line = line;
    return n;
}

static Node *parse_atom(void) {
    Tok *t = peek(0);
    if (t->kind == TK_INT) { tpos++; Node *n = node(NK_INT, t->line); n->num = t->num; return n; }
    if (t->kind == TK_STR) { tpos++; Node *n = node(NK_STR, t->line); n->str = t->s; n->slen = t->slen; return n; }
    if (t->kind == TK_NAME && (!strcmp(t->s, "true") || !strcmp(t->s, "false"))) {
        tpos++;
        Node *n = node(NK_BOOL, t->line);
        n->num = !strcmp(t->s, "true");
        return n;
    }
    if (t->kind == TK_NAME && !strcmp(t->s, "if")) {
        tpos++; take("(");
        Node *n = node(NK_IF, t->line);
        n->a = parse_expr(); take(",");
        n->b = parse_expr(); take(",");
        n->c = parse_expr(); take(")");
        return n;
    }
    if (at("(")) { tpos++; Node *e = parse_expr(); take(")"); return e; }
    if (at("{")) die(t->line, "子集之外：块当表达式用");
    if (at("[")) die(t->line, "子集之外：这里出现 '['（表不在子集里）");
    if (t->kind == TK_NAME) { tpos++; Node *n = node(NK_NAME, t->line); n->name = t->s; return n; }
    die(t->line, "子集之外：这里出现 '%s'", t->s ? t->s : "<结束>");
    return NULL;
}

static Node *parse_postfix(void) {
    Node *e = parse_atom();
    while (at("(")) {
        if (e->kind != NK_NAME) die(peek(0)->line, "子集之外：只有具名函数能被调用");
        int line = e->line;
        tpos++;
        Node **args = NULL; int n = 0;
        if (!at(")")) {
            args = realloc(args, sizeof(Node *) * (n + 1));
            args[n++] = parse_expr();
            while (at(",")) { tpos++; args = realloc(args, sizeof(Node *) * (n + 1)); args[n++] = parse_expr(); }
        }
        take(")");
        Node *c = node(NK_CALL, line);
        c->name = e->name; c->args = args; c->nargs = n;
        e = c;
    }
    return e;
}

static Node *parse_unary(void) {
    if (at("-")) { Tok *t = take("-"); Node *n = node(NK_NEG, t->line); n->a = parse_unary(); return n; }
    if (at("!")) { Tok *t = take("!"); Node *n = node(NK_NOT, t->line); n->a = parse_unary(); return n; }
    return parse_postfix();
}

static const char *BIN[6][4] = {
    {"||", NULL}, {"&&", NULL}, {"==", "!=", NULL},
    {"<", "<=", ">", ">="}, {"+", "-", NULL}, {"*", "/", "%", NULL},
};

static Node *parse_bin(int lvl) {
    if (lvl == 6) return parse_unary();
    Node *e = parse_bin(lvl + 1);
    while (peek(0)->s) {
        int found = 0;
        for (int i = 0; i < 4 && BIN[lvl][i]; i++) if (!strcmp(peek(0)->s, BIN[lvl][i])) { found = 1; break; }
        if (!found) break;
        Tok *t = take(NULL);
        Node *n = node(NK_BIN, t->line);
        n->name = t->s; n->a = e; n->b = parse_bin(lvl + 1);
        e = n;
    }
    return e;
}
static Node *parse_expr(void) { return parse_bin(0); }

static Stmt *parse_block(int *n) {
    take("{");
    Stmt *out = NULL; *n = 0;
    while (!at("}")) {
        if (peek(0)->kind == TK_EOF) die(peek(0)->line, "块没闭合");
        Stmt s;
        if (at("let")) {
            Tok *t = take("let");
            Tok *nm = take(NULL);
            if (nm->kind != TK_NAME) die(nm->line, "let 后面要一个名字");
            take("=");
            s.kind = 0; s.name = nm->s; s.e = parse_expr();
            (void)t;
        } else if (at("fn")) {
            die(peek(0)->line, "子集之外：块里定义局部函数（嵌套）");
        } else {
            s.kind = 1; s.name = NULL; s.e = parse_expr();
        }
        out = realloc(out, sizeof(Stmt) * (*n + 1));
        out[(*n)++] = s;
    }
    take("}");
    return out;
}

static void parse_program(void) {
    while (peek(0)->kind != TK_EOF) {
        if (at("fn")) {
            take("fn");
            Tok *nm = take(NULL);
            if (nm->kind != TK_NAME) die(nm->line, "fn 后面要一个名字");
            take("(");
            char **ps = NULL; int np = 0;
            if (!at(")")) {
                ps = realloc(ps, sizeof(char *) * (np + 1));
                Tok *p = take(NULL);
                if (p->kind != TK_NAME) die(p->line, "形参要一个名字");
                ps[np++] = p->s;
                while (at(",")) {
                    take(",");
                    p = take(NULL);
                    if (p->kind != TK_NAME) die(p->line, "形参要一个名字");
                    ps = realloc(ps, sizeof(char *) * (np + 1));
                    ps[np++] = p->s;
                }
            }
            take(")");
            Fn f = {nm->s, ps, np, NULL, 0};
            f.body = parse_block(&f.nbody);
            fns = realloc(fns, sizeof(Fn) * (nfns + 1));
            fns[nfns++] = f;
        } else if (at("let") || at("(") || peek(0)->kind == TK_INT || peek(0)->kind == TK_STR ||
                   peek(0)->kind == TK_NAME) {
            Stmt s;
            if (at("let")) {
                take("let");
                Tok *nm = take(NULL);
                take("=");
                s.kind = 0; s.name = nm->s; s.e = parse_expr();
            } else { s.kind = 1; s.name = NULL; s.e = parse_expr(); }
            main_body = realloc(main_body, sizeof(Stmt) * (nmain + 1));
            main_body[nmain++] = s;
        } else die(peek(0)->line, "子集之外：这里出现 '%s'", peek(0)->s ? peek(0)->s : "<结束>");
    }
}

// ---------------------------------------------------------------- 机器码
static unsigned char code[1 << 17];
static int clen;

static void b(int v) { code[clen++] = (unsigned char)v; }
static void b4(int v) { for (int i = 0; i < 4; i++) b(v >> (8 * i)); }
static void b8(long long v) { for (int i = 0; i < 8; i++) b((int)(v >> (8 * i))); }
// REX：0100WRXB
static void rex(int w, int r, int x, int bb) { b(0x40 | (w << 3) | ((r & 8) ? 4 : 0) | ((x & 8) ? 2 : 0) | ((bb & 8) ? 1 : 0)); }
static void modrm_mem(int reg, int base, int off) {
    if (off >= -128 && off <= 127) { b(0x40 | ((reg & 7) << 3) | (base & 7)); b(off & 0xff); }
    else { b(0x80 | ((reg & 7) << 3) | (base & 7)); b4(off); }
}
static void modrm_reg(int reg, int rm) { b(0xC0 | ((reg & 7) << 3) | (rm & 7)); }

enum { RAX = 0, RCX = 1, RDX = 2, RBX = 3, RSP = 4, RBP = 5, RSI = 6, RDI = 7, R8 = 8, R9 = 9, R10 = 10 };

static void mov_imm64(int reg, long long v) { rex(1, 0, 0, reg); b(0xB8 + (reg & 7)); b8(v); }
static void mov_imm32(int reg, int v) { rex(0, 0, 0, reg); b(0xB8 + (reg & 7)); b4(v); }
static void store_rbp(int off, int reg) { rex(1, reg, 0, 0); b(0x89); modrm_mem(reg, RBP, off); }
static void load_rbp(int off, int reg) { rex(1, reg, 0, 0); b(0x8B); modrm_mem(reg, RBP, off); }
static void mov_rr(int dst, int src) { rex(1, src, 0, dst); b(0x89); modrm_reg(src, dst); }
static void add_rr(int dst, int src) { rex(1, src, 0, dst); b(0x01); modrm_reg(src, dst); }
static void sub_rr(int dst, int src) { rex(1, src, 0, dst); b(0x29); modrm_reg(src, dst); }
static void cmp_rr(int a, int rhs) { rex(1, rhs, 0, a); b(0x39); modrm_reg(rhs, a); }   // a - rhs
static void imul_rr(int dst, int src) { rex(1, dst, 0, src); b(0x0F); b(0xAF); modrm_reg(dst, src); }
static void idiv_r(int reg) { rex(1, 0, 0, reg); b(0xF7); modrm_reg(7, reg); }
static void div_r(int reg) { rex(1, 0, 0, reg); b(0xF7); modrm_reg(6, reg); }
static void neg_r(int reg) { rex(1, 0, 0, reg); b(0xF7); modrm_reg(3, reg); }
static void test_rr(int a, int bb) { rex(1, bb, 0, a); b(0x85); modrm_reg(bb, a); }
static void push_r(int reg) { if (reg & 8) b(0x41); b(0x50 + (reg & 7)); }
static void pop_r(int reg) { if (reg & 8) b(0x41); b(0x58 + (reg & 7)); }
static void cqo(void) { b(0x48); b(0x99); }
static void syscall_(void) { b(0x0F); b(0x05); }
static void ret_(void) { b(0xC3); }
static void setcc_al(int cc) { b(0x0F); b(0x90 + cc); b(0xC0); }
static void movzx_rax_al(void) { b(0x48); b(0x0F); b(0xB6); b(0xC0); }
static void add_rsp(int n) { b(0x48); b(0x81); b(0xC4); b4(n); }

enum { CC_E = 4, CC_NE = 5, CC_L = 0xC, CC_LE = 0xE, CC_G = 0xF, CC_GE = 0xD, CC_NS = 9 };

// 标签与回填
static int labels[8192], nlabels;
static struct { int pos, id; } fixups[8192];
static int nfix;

static int label_new(void) { labels[nlabels] = -1; return nlabels++; }
static void label_here(int id) { labels[id] = clen; }
static void jmp_label(int id) { b(0xE9); fixups[nfix].pos = clen; fixups[nfix++].id = id; b4(0); }
static void jcc_label(int cc, int id) { b(0x0F); b(0x80 | cc); fixups[nfix].pos = clen; fixups[nfix++].id = id; b4(0); }
static void call_label(int id) { b(0xE8); fixups[nfix].pos = clen; fixups[nfix++].id = id; b4(0); }

// 字符串与缓冲区（地址在布局后回填）
typedef struct { char *bytes; int len; int off; } Str;
static Str *strs; static int nstrs;
static struct { int pos, kind, idx; } absp[4096]; static int nabsp;   // kind 0=字符串 1=缓冲区

static int str_add(const char *p, int len) {
    strs = realloc(strs, sizeof(Str) * (nstrs + 1));
    strs[nstrs].bytes = malloc(len);
    memcpy(strs[nstrs].bytes, p, len);
    strs[nstrs].len = len;
    strs[nstrs].off = -1;
    return nstrs++;
}

// ---------------------------------------------------------------- 类型（只用来拒绝布尔当整数打）
enum { T_INT, T_BOOL, T_STR, T_ANY, T_NONE };
static const char *tname(int t) {
    return t == T_INT ? "int" : t == T_BOOL ? "bool" : t == T_STR ? "str" : t == T_NONE ? "none" : "any";
}

typedef struct { char *name; int off; int ty; } Slot;
static Slot slots[256]; static int nslots;
static char *cur_fn; static int cur_nparams;
static int fnbody_label;
static int fnret[256];

static int slot_find(const char *nm) {
    for (int i = 0; i < nslots; i++) if (!strcmp(slots[i].name, nm)) return i;
    return -1;
}

static int type_of(Node *e) {
    if (!e) return T_ANY;
    switch (e->kind) {
    case NK_INT: return T_INT;
    case NK_BOOL: return T_BOOL;
    case NK_STR: return T_STR;
    case NK_NAME: { int i = slot_find(e->name); return i < 0 ? T_ANY : slots[i].ty; }
    case NK_NEG: return T_INT;
    case NK_NOT: return T_BOOL;
    case NK_BIN:
        return (!strcmp(e->name, "==") || !strcmp(e->name, "!=") || !strcmp(e->name, "<") ||
                !strcmp(e->name, "<=") || !strcmp(e->name, ">") || !strcmp(e->name, ">=") ||
                !strcmp(e->name, "&&") || !strcmp(e->name, "||")) ? T_BOOL : T_INT;
    case NK_IF: {
        int a = type_of(e->b), c = type_of(e->c);
        if (a == c) return a;
        if (a == T_ANY) return c;
        if (c == T_ANY) return a;
        return T_ANY;
    }
    case NK_CALL:
        if (!strcmp(e->name, "str")) return T_STR;
        if (!strcmp(e->name, "print")) return T_NONE;
        for (int i = 0; i < nfns; i++) if (!strcmp(fns[i].name, e->name)) return fnret[i];
        return T_ANY;
    }
    return T_ANY;
}

// ---------------------------------------------------------------- 生成
static void gen_expr(Node *e, int tail, int *terminated);
static int itox_label = -1;

static void gen_write_str(Node *s) {
    int id = str_add(s->str, s->slen);
    // 末尾补换行
    strs[id].bytes = realloc(strs[id].bytes, strs[id].len + 1);
    strs[id].bytes[strs[id].len++] = '\n';
    mov_imm32(RDI, 1);
    rex(1, 0, 0, RSI); b(0xB8 + (RSI & 7)); absp[nabsp].pos = clen; absp[nabsp].kind = 0; absp[nabsp++].idx = id; b8(0);
    mov_imm32(RDX, strs[id].len);
    mov_imm32(RAX, 1);
    syscall_();
}

static void gen_print(Node *arg) {
    if (arg->kind == NK_STR) { gen_write_str(arg); return; }
    if (arg->kind == NK_CALL && !strcmp(arg->name, "str") && arg->nargs == 1) {
        Node *in = arg->args[0];
        if (type_of(in) == T_BOOL)
            die(arg->line, "子集之外：str(布尔) 在定义里是 \"true\"/\"false\" —— 子集还没实现布尔转文本，拒绝，不许印成 1/0");
        if (in->kind == NK_IF && in->b->kind == NK_STR && in->c->kind == NK_STR) {
            int l_else = label_new(), l_end = label_new();
            gen_expr(in->a, 0, NULL);
            test_rr(RAX, RAX);
            jcc_label(CC_E, l_else);
            gen_write_str(in->b);
            jmp_label(l_end);
            label_here(l_else);
            gen_write_str(in->c);
            label_here(l_end);
            return;
        }
        if (in->kind == NK_STR) { gen_write_str(in); return; }
        gen_expr(in, 0, NULL);
        call_label(itox_label);
        return;
    }
    die(arg->line, "子集之外：print 的参数只有 str(整数)、字符串字面量、str(if(c, \"甲\", \"乙\"))");
}

static void gen_bin(Node *e) {
    const char *op = e->name;
    if (!strcmp(op, "&&") || !strcmp(op, "||")) {
        int l_short = label_new(), l_end = label_new();
        gen_expr(e->a, 0, NULL);
        test_rr(RAX, RAX);
        jcc_label(!strcmp(op, "&&") ? CC_E : CC_NE, l_short);
        gen_expr(e->b, 0, NULL);
        test_rr(RAX, RAX);
        setcc_al(CC_NE);
        movzx_rax_al();
        jmp_label(l_end);
        label_here(l_short);
        mov_imm32(RAX, !strcmp(op, "||"));
        label_here(l_end);
        return;
    }
    gen_expr(e->a, 0, NULL);
    push_r(RAX);
    gen_expr(e->b, 0, NULL);
    pop_r(RCX);
    if (!strcmp(op, "+")) add_rr(RAX, RCX);
    else if (!strcmp(op, "-")) { mov_rr(RDX, RAX); mov_rr(RAX, RCX); sub_rr(RAX, RDX); }
    else if (!strcmp(op, "*")) imul_rr(RAX, RCX);
    else if (!strcmp(op, "/") || !strcmp(op, "%")) {
        mov_rr(R8, RAX); mov_rr(RAX, RCX); cqo(); idiv_r(R8);
        if (!strcmp(op, "%")) mov_rr(RAX, RDX);
    } else {
        cmp_rr(RCX, RAX);
        int cc = !strcmp(op, "==") ? CC_E : !strcmp(op, "!=") ? CC_NE : !strcmp(op, "<") ? CC_L
               : !strcmp(op, "<=") ? CC_LE : !strcmp(op, ">") ? CC_G : CC_GE;
        setcc_al(cc);
        movzx_rax_al();
    }
}

static void gen_if(Node *e, int tail, int *term) {
    int t = type_of(e->a);
    if (t == T_INT || t == T_STR)
        die(e->line, "子集之外：if 的条件是 %s —— 定义要 true/false（比值和 && || ! 给的是布尔）", tname(t));
    int l_else = label_new(), l_end = label_new();
    gen_expr(e->a, 0, NULL);
    test_rr(RAX, RAX);
    jcc_label(CC_E, l_else);
    int ta = 0, tb = 0;
    gen_expr(e->b, tail, &ta);
    if (!ta) jmp_label(l_end);
    label_here(l_else);
    gen_expr(e->c, tail, &tb);
    if (!(ta && tb)) label_here(l_end);
    if (term) *term = ta && tb;
}

static void gen_call(Node *e, int tail, int *term) {
    if (!strcmp(e->name, "print")) die(e->line, "子集之外：print 只能当语句用");
    if (!strcmp(e->name, "str")) die(e->line, "子集之外：str 只认 print(str(...)) 这一种用法");
    int idx = -1;
    for (int i = 0; i < nfns; i++) if (!strcmp(fns[i].name, e->name)) idx = i;
    if (idx < 0) die(e->line, "子集之外/未绑定：函数 %s 不在这个程序里", e->name);
    if (tail && !strcmp(e->name, cur_fn) && e->nargs == cur_nparams) {
        for (int i = 0; i < e->nargs; i++) { gen_expr(e->args[i], 0, NULL); push_r(RAX); }
        for (int i = e->nargs - 1; i >= 0; i--) { pop_r(RAX); store_rbp(16 + 8 * (cur_nparams - 1 - i), RAX); }
        jmp_label(fnbody_label);
        if (term) *term = 1;
        return;
    }
    for (int i = 0; i < e->nargs; i++) { gen_expr(e->args[i], 0, NULL); push_r(RAX); }
    call_label(idx);
    if (e->nargs) add_rsp(8 * e->nargs);
    if (term) *term = 0;
}

static void gen_expr(Node *e, int tail, int *term) {
    if (term) *term = 0;
    switch (e->kind) {
    case NK_INT: mov_imm64(RAX, e->num); return;
    case NK_BOOL: mov_imm64(RAX, e->num); return;
    case NK_STR: die(e->line, "子集之外：字符串只能出现在 print 里"); return;
    case NK_NAME: {
        int i = slot_find(e->name);
        if (i < 0) die(e->line, "子集之外/未绑定：%s 不在本函数的形参与 let 里", e->name);
        load_rbp(slots[i].off, RAX);
        return;
    }
    case NK_NEG: gen_expr(e->a, 0, NULL); neg_r(RAX); return;
    case NK_NOT: gen_expr(e->a, 0, NULL); test_rr(RAX, RAX); setcc_al(CC_E); movzx_rax_al(); return;
    case NK_BIN: gen_bin(e); return;
    case NK_IF: gen_if(e, tail, term); return;
    case NK_CALL: gen_call(e, tail, term); return;
    }
}

static void gen_stmts(Stmt *b, int n, int *term) {
    *term = 0;
    for (int i = 0; i < n; i++) {
        int last = (i == n - 1);
        if (b[i].kind == 0) {
            gen_expr(b[i].e, 0, NULL);
            if (nslots >= 256) die(b[i].e->line, "子集之外：局部变量太多");
            slots[nslots].name = b[i].name;
            slots[nslots].off = -8 * (nslots + 1);
            slots[nslots].ty = type_of(b[i].e);
            nslots++;
            store_rbp(-8 * nslots, RAX);
        } else if (b[i].e->kind == NK_CALL && !strcmp(b[i].e->name, "print")) {
            if (b[i].e->nargs != 1) die(b[i].e->line, "子集之外：print 只吃一个实参");
            gen_print(b[i].e->args[0]);
        } else {
            int t = 0;
            gen_expr(b[i].e, last, &t);
            if (t && !last) die(b[i].e->line, "子集之外：尾调用后面还有语句");
            if (last) *term = t;
        }
    }
}

static void gen_function(const char *name, char **params, int np, Stmt *body, int nb, int is_main) {
    cur_fn = (char *)name;
    cur_nparams = np;
    nslots = 0;
    for (int i = 0; i < np; i++) {
        slots[nslots].name = params[i];
        slots[nslots].off = 16 + 8 * (np - 1 - i);
        slots[nslots].ty = T_ANY;
        nslots++;
    }
    fnbody_label = label_new();
    // 函数体先生成到别处？这里用「先占位 sub rsp，回头改立即数」的办法
    push_r(RBP);
    mov_rr(RBP, RSP);                    // 序言：rbp := rsp（不是反过来）
    b(0x48); b(0x81); b(0xEC); int sub_pos = clen; b4(0);
    label_here(fnbody_label);
    int term = 0;
    gen_stmts(body, nb, &term);
    if (!term) {
        mov_rr(RSP, RBP);
        pop_r(RBP);
        ret_();
    }
    int frame = 8 * nslots + 16;          // 16 = push rbp 之后的 8 字节返回地址 + 对齐余量
    frame = (frame + 15) & ~15;
    for (int k = 0; k < 4; k++) code[sub_pos + k] = (unsigned char)(frame >> (8 * k));
    (void)is_main;
}

// ---------------------------------------------------------------- ELF
#define BASE 0x400000
#define CODE_OFF 128

int main(int argc, char **argv) {
    if (argc < 2) { fprintf(stderr, "用法: %s 程序.dr [-o 输出]\n", argv[0]); return 2; }
    const char *out = "a.out";
    for (int i = 2; i < argc; i++) if (!strcmp(argv[i], "-o") && i + 1 < argc) out = argv[i + 1];
    FILE *f = fopen(argv[1], "rb");
    if (!f) { fprintf(stderr, "挂：读不到 %s\n", argv[1]); return 2; }
    fseek(f, 0, SEEK_END); long sz = ftell(f); fseek(f, 0, SEEK_SET);
    char *src = malloc(sz + 1);
    if (fread(src, 1, sz, f) != (size_t)sz) { fprintf(stderr, "挂：读不到 %s\n", argv[1]); return 2; }
    src[sz] = 0; fclose(f);

    lex(src);
    parse_program();

    // 返回类型两遍（互相调用的一圈也够定下来）
    for (int i = 0; i < nfns; i++) fnret[i] = T_ANY;
    for (int pass = 0; pass < 2; pass++) {
        for (int i = 0; i < nfns; i++) {
            nslots = 0;
            for (int k = 0; k < fns[i].nparams; k++) {
                slots[nslots].name = fns[i].params[k]; slots[nslots].off = 0; slots[nslots].ty = T_ANY; nslots++;
            }
            for (int k = 0; k < fns[i].nbody; k++)
                if (fns[i].body[k].kind == 0) {
                    slots[nslots].name = fns[i].body[k].name;
                    slots[nslots].off = 0;
                    slots[nslots].ty = type_of(fns[i].body[k].e);
                    nslots++;
                }
            Node *tail = NULL;
            if (fns[i].nbody && fns[i].body[fns[i].nbody - 1].kind == 1) tail = fns[i].body[fns[i].nbody - 1].e;
            fnret[i] = type_of(tail);
        }
    }

    // _start
    b(0x31); b(0xED);                       // xor ebp, ebp
    b(0x48); b(0x83); b(0xE4); b(0xF0);     // and rsp, -16
    // 先预留 call main 的位置：main 的标签在所有函数之后才知道，先建标签
    int main_label = nfns;                  // 约定：标签 id = 函数下标，main = nfns
    call_label(main_label);
    mov_imm32(RAX, 60);                     // exit(0)
    b(0x31); b(0xFF);                       // xor edi, edi
    syscall_();

    // 函数标签要在生成函数体前占位：标签 id 必须等于函数下标
    itox_label = nfns + 1;
    while (nlabels <= nfns + 1) label_new();

    for (int i = 0; i < nfns; i++) {
        label_here(i);
        gen_function(fns[i].name, fns[i].params, fns[i].nparams, fns[i].body, fns[i].nbody, 0);
    }
    label_here(main_label);
    gen_function("main", NULL, 0, main_body, nmain, 1);

    // 运行时 itoa：rax -> 十进制 + 换行
    label_here(itox_label);
    rex(1, 0, 0, RSI); b(0xB8 + (RSI & 7)); absp[nabsp].pos = clen; absp[nabsp].kind = 1; absp[nabsp++].idx = 32; b8(0);  // mov rsi, buf+32
    b(0x48); b(0xFF); b(0xCE);              // dec rsi
    b(0xC6); b(0x06); b(0x0A);              // mov byte [rsi], 10
    mov_rr(R8, RAX);                        // 49 89 C0
    b(0x45); b(0x31); b(0xD2);              // xor r10d, r10d
    test_rr(R8, R8);
    int l_pos = label_new(), l_loop = label_new(), l_done = label_new();
    jcc_label(CC_NS, l_pos);
    neg_r(R8);
    b(0x41); b(0xBA); b4(1);                // mov r10d, 1
    label_here(l_pos);
    b(0x41); b(0xB9); b4(10);               // mov r9d, 10
    label_here(l_loop);
    b(0x31); b(0xD2);                       // xor edx, edx
    mov_rr(RAX, R8);
    div_r(R9);
    b(0x83); b(0xC2); b(0x30);              // add edx, 48
    b(0x48); b(0xFF); b(0xCE);              // dec rsi
    b(0x88); b(0x16);                       // mov [rsi], dl
    mov_rr(R8, RAX);
    test_rr(R8, R8);
    jcc_label(CC_NE, l_loop);
    b(0x45); b(0x85); b(0xD2);              // test r10d, r10d
    jcc_label(CC_E, l_done);
    b(0x48); b(0xFF); b(0xCE);              // dec rsi
    b(0xC6); b(0x06); b(0x2D);              // mov byte [rsi], '-'
    label_here(l_done);
    rex(1, 0, 0, RDX); b(0xB8 + (RDX & 7)); absp[nabsp].pos = clen; absp[nabsp].kind = 1; absp[nabsp++].idx = 32; b8(0);  // mov rdx, buf+32
    b(0x48); b(0x29); b(0xF2);              // sub rdx, rsi
    mov_imm32(RDI, 1);
    mov_imm32(RAX, 1);
    syscall_();
    ret_();

    // 布局：代码 + 字符串 + 64 字节缓冲
    int data_off = clen;
    for (int i = 0; i < nstrs; i++) { strs[i].off = data_off; data_off += strs[i].len; }
    int buf_off = data_off;
    int total = data_off + 64;

    // 回填绝对地址（代码在文件里的偏移是 CODE_OFF，虚拟地址 = BASE + CODE_OFF + 代码内偏移）
    for (int i = 0; i < nabsp; i++) {
        long long addr = BASE + CODE_OFF + (absp[i].kind == 0 ? strs[absp[i].idx].off : buf_off + absp[i].idx);
        for (int k = 0; k < 8; k++) code[absp[i].pos + k] = (unsigned char)(addr >> (8 * k));
    }
    // 回填相对跳转
    for (int i = 0; i < nfix; i++) {
        int rel = labels[fixups[i].id] - (fixups[i].pos + 4);
        for (int k = 0; k < 4; k++) code[fixups[i].pos + k] = (unsigned char)(rel >> (8 * k));
    }
    for (int i = 0; i < nlabels; i++)
        if (labels[i] < 0) { fprintf(stderr, "内部错：标签 %d 没绑\n", i); return 1; }

    int code_off = CODE_OFF;
    int file_size = code_off + total;

    unsigned char *img = calloc(1, file_size);
    // ELF64 header
    img[0] = 0x7F; img[1] = 'E'; img[2] = 'L'; img[3] = 'F';
    img[4] = 2; img[5] = 1; img[6] = 1;
    *(uint16_t *)(img + 16) = 2;        // ET_EXEC
    *(uint16_t *)(img + 18) = 62;       // x86-64
    *(uint32_t *)(img + 20) = 1;
    *(uint64_t *)(img + 24) = BASE + code_off;
    *(uint64_t *)(img + 32) = 64;       // phoff
    *(uint16_t *)(img + 52) = 64;       // ehsize
    *(uint16_t *)(img + 54) = 56;       // phentsize
    *(uint16_t *)(img + 56) = 1;        // phnum
    // program header
    unsigned char *ph = img + 64;
    *(uint32_t *)(ph + 0) = 1;          // PT_LOAD
    *(uint32_t *)(ph + 4) = 7;          // RWE
    *(uint64_t *)(ph + 8) = 0;          // offset
    *(uint64_t *)(ph + 16) = BASE;
    *(uint64_t *)(ph + 24) = BASE;
    *(uint64_t *)(ph + 32) = file_size;
    *(uint64_t *)(ph + 40) = file_size;
    *(uint64_t *)(ph + 48) = 0x1000;

    memcpy(img + code_off, code, clen);
    int d = code_off + clen;
    for (int i = 0; i < nstrs; i++) { memcpy(img + d, strs[i].bytes, strs[i].len); d += strs[i].len; }

    FILE *o = fopen(out, "wb");
    if (!o) { fprintf(stderr, "挂：写不了 %s\n", out); return 2; }
    fwrite(img, 1, file_size, o);
    fclose(o);
    chmod(out, 0755);
    printf("dropc: %s -> %s（%d B：代码 %d + 数据 %d）\n", argv[1], out, file_size, clen, total - clen);
    return 0;
}
