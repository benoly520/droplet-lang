// drop v0.41 —— 第二个宿主（C# / net8.0 / 只用 BCL，不引任何包）。
// 唯一规范来源：/tmp/croom/spec.txt。凡规范没写清而这里做了选择的，REPORT.md 里都记了一条。
using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Net;
using System.Net.Sockets;
using System.Numerics;
using System.Text;
using System.Threading;

namespace DropLang
{
    // ========== 段 A：公共小工具 ==========
    static class U
    {
        // UTF-16 串 -> 码点数组。drop 的「一个字符」按码点算（"文" 的 len 是 1）。
        public static int[] Cp(string s)
        {
            var l = new List<int>(s.Length);
            for (int i = 0; i < s.Length;)
            {
                if (char.IsHighSurrogate(s[i]) && i + 1 < s.Length && char.IsLowSurrogate(s[i + 1]))
                { l.Add(char.ConvertToUtf32(s[i], s[i + 1])); i += 2; continue; }
                l.Add(s[i]);
                i++;
            }
            return l.ToArray();
        }

        public static string Text(int[] cps)
        {
            if (cps.Length == 0) return "";
            var sb = new StringBuilder(cps.Length);
            for (int i = 0; i < cps.Length; i++) sb.Append(char.ConvertFromUtf32(cps[i]));
            return sb.ToString();
        }

        public static int[] Slice(int[] a, int i, int j)
        {
            var r = new int[j - i];
            Array.Copy(a, i, r, 0, r.Length);
            return r;
        }

        public static string Kind(Val v)
        {
            if (v is IntV) return "整数";
            if (v is StrV) return "字符串";
            if (v is BoolV) return "true/false";
            if (v is NoneV) return "无";
            return "表";
        }

        public static int CmpCp(int[] a, int[] b)
        {
            int n = Math.Min(a.Length, b.Length);
            for (int i = 0; i < n; i++) if (a[i] != b[i]) return a[i] < b[i] ? -1 : 1;
            return a.Length.CompareTo(b.Length);
        }
    }

    // ========== 段 B：值与错误 ==========
    abstract class Val
    {
    }

    sealed class IntV : Val
    {
        public BigInteger V;
        public IntV(BigInteger v) { V = v; }
    }

    sealed class StrV : Val
    {
        public int[] Cp;                                  // 一个元素 = 一个字符
        public StrV(int[] cps) { Cp = cps; }
        public static StrV Of(string s) { return new StrV(U.Cp(s)); }
        public string S { get { return U.Text(Cp); } }
    }

    sealed class BoolV : Val
    {
        public bool V;
        BoolV(bool v) { V = v; }
        public static readonly BoolV T = new BoolV(true);
        public static readonly BoolV F = new BoolV(false);
        public static BoolV Of(bool v) { return v ? T : F; }
    }

    sealed class NoneV : Val
    {
        public static readonly NoneV I = new NoneV();
    }

    sealed class TabV : Val
    {
        public List<Val> Items;
        public TabV(List<Val> items) { Items = items; }
    }

    // 唯一的运行时错形态：码号 + 行号 + 一句中文说明。
    sealed class DropErr : Exception
    {
        public readonly int Code;                          // 0 = 不是「第 N 行 Ex」那种
        public readonly int Line;
        public DropErr(int code, int line, string msg) : base(msg) { Code = code; Line = line; }
    }

    sealed class Frame
    {
        public FuncDef Fn;
        public Env Env;
    }

    sealed class Ctx
    {
        public readonly List<Frame> Frames = new List<Frame>();
        public int StmtLine;                               // 正在执行的顶层句子所在行
        public int CurLine;                                // 正在求值的那个记号所在行
        public int Lit;                                    // 当前表字面量「[」所在行，0 = 不在表字面量里
        public readonly Dictionary<string, HObj> Handles = new Dictionary<string, HObj>();
        public int NextHandle = 1;
        public int? Port;                                  // --port 给的端口，没给就是 null

        // 行号规则（维护者 2026-11-08 第二次定调后的读法）：
        // 默认「N 是那个记号所在的行」—— 错在函数体里也报体里那一行，不搬到调用处。
        // 例外一：表字面量里的错报「[」那一行（定义明写）。
        // 例外二：「函数体里的错报在调用那一行」只在「那个记号本身就是这次调用」时成立，
        //         也就是 E2（参数个数不符）和 E13（叠到第 201 层）—— 由调用处传 callLine 进来。
        public DropErr E(int code, string msg, int callLine = 0)
        {
            int line = callLine != 0 ? callLine : Lit != 0 ? Lit : CurLine;
            if (line <= 0) line = StmtLine > 0 ? StmtLine : 1;
            return new DropErr(code, line, msg);
        }

        public int Allocated => Frames.Count;
    }

    // ========== 段 C：token ==========
    enum Tk { Int, Str, Name, Punct, New, Semi, Eof }

    sealed class Token
    {
        public Tk K;
        public int Line;
        public string S;                                   // 名字 / 标点
        public BigInteger I;                               // 整数面值
        public int[] Txt;                                  // 字符串码点

        public string Show()
        {
            if (K == Tk.Int) return "一个整数";
            if (K == Tk.Str) return "一个字符串";
            if (K == Tk.Name) return "名字「" + S + "」";
            if (K == Tk.Punct) return "「" + S + "」";
            if (K == Tk.Semi) return "分号";
            if (K == Tk.New) return "换行";
            return "文件尾";
        }
    }

    // ========== 段 D：词法 ==========
    static class Lexer
    {
        static bool IsDigit(int c) { return c >= '0' && c <= '9'; }
        static bool IsLatin(int c) { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'); }
        static bool IsAlpha(int c) { return IsLatin(c) || c == '_' || c > 127; }   // 汉字算字母
        static bool IsNameCont(int c) { return IsAlpha(c) || IsDigit(c); }
        static bool IsNameStart(int c) { return IsAlpha(c); }

        static int Hex(int c)
        {
            if (c >= '0' && c <= '9') return c - '0';
            if (c >= 'a' && c <= 'f') return c - 'a' + 10;
            if (c >= 'A' && c <= 'F') return c - 'A' + 10;
            return -1;
        }

        // 扫一行（调用方已经按换行切好，\r 也去掉了）。注释 // 到行尾。
        public static List<Token> Line(int ln, int[] s)
        {
            var list = new List<Token>();
            int p = 0, n = s.Length;
            while (p < n)
            {
                int c = s[p];
                if (c == ' ' || c == '\t') { p++; continue; }
                if (c == '/' && p + 1 < n && s[p + 1] == '/') break;
                if (c == '"') { list.Add(StrTok(ln, s, ref p)); continue; }
                if (IsDigit(c)) { list.Add(IntTok(ln, s, ref p)); continue; }
                if (IsNameStart(c)) { list.Add(NameTok(ln, s, ref p)); continue; }
                list.Add(PunctTok(ln, s, ref p));
            }
            return list;
        }

        static Token IntTok(int ln, int[] s, ref int p)
        {
            int st = p;
            while (p < s.Length && IsDigit(s[p])) p++;
            if (p < s.Length && s[p] == '.')
                throw new DropErr(3, ln, "语法错：没有小数（这里出现了小数点）");
            BigInteger v = BigInteger.Zero;
            for (int i = st; i < p; i++) v = v * 10 + (s[i] - '0');   // 07 就是 7
            return new Token { K = Tk.Int, Line = ln, I = v };
        }

        static Token NameTok(int ln, int[] s, ref int p)
        {
            int st = p;
            while (p < s.Length && IsNameCont(s[p])) p++;
            return new Token { K = Tk.Name, Line = ln, S = U.Text(U.Slice(s, st, p)) };
        }

        static Token PunctTok(int ln, int[] s, ref int p)
        {
            int c = s[p];
            int d = p + 1 < s.Length ? s[p + 1] : -1;
            string t;
            switch (c)
            {
                case ';': p++; return new Token { K = Tk.Semi, Line = ln, S = ";" };
                case '=': t = d == '=' ? "==" : "="; break;
                case '!': t = d == '=' ? "!=" : "!"; break;
                case '<': t = d == '=' ? "<=" : "<"; break;
                case '>': t = d == '=' ? ">=" : ">"; break;
                case '&': t = d == '&' ? "&&" : null; break;
                case '|': t = d == '|' ? "||" : null; break;
                case '+': t = "+"; break;
                case '-': t = "-"; break;
                case '*': t = "*"; break;
                case '/': t = "/"; break;
                case '%': t = "%"; break;
                case '(': t = "("; break;
                case ')': t = ")"; break;
                case '[': t = "["; break;
                case ']': t = "]"; break;
                case '{': t = "{"; break;
                case '}': t = "}"; break;
                case ',': t = ","; break;
                case ':': t = ":"; break;
                default: t = null; break;
            }
            if (t == null)
                throw new DropErr(3, ln, "语法错：不认识的符号「" + char.ConvertFromUtf32(c) + "」");
            p += t.Length;
            return new Token { K = Tk.Punct, Line = ln, S = t };
        }

        static Token StrTok(int ln, int[] s, ref int p)
        {
            p++;                                                  // 开头引号
            var cps = new List<int>();
            while (true)
            {
                if (p >= s.Length)
                    throw new DropErr(11, ln, "话说到一半：引号没有闭合（要换行请写 \\n）");
                int c = s[p];
                if (c == '"') { p++; break; }
                if (c == '\\')
                {
                    p++;
                    if (p >= s.Length)
                        throw new DropErr(11, ln, "话说到一半：字符串在反斜杠处断了");
                    int e = s[p++];
                    if (e == 'n') cps.Add('\n');
                    else if (e == 't') cps.Add('\t');
                    else if (e == '\\') cps.Add('\\');
                    else if (e == '"') cps.Add('"');
                    else if (e == 'x')
                    {
                        if (p + 1 >= s.Length || Hex(s[p]) < 0 || Hex(s[p + 1]) < 0)
                            throw new DropErr(3, ln, "语法错：\\x 后面要跟两个十六进制数字");
                        cps.Add(Hex(s[p]) * 16 + Hex(s[p + 1]));
                        p += 2;
                    }
                    else throw new DropErr(3, ln, "语法错：没有这种转义（反斜杠后是「" + char.ConvertFromUtf32(e) + "」）");
                    continue;
                }
                cps.Add(c);
                p++;
            }
            return new Token { K = Tk.Str, Line = ln, Txt = cps.ToArray() };
        }
    }

    // ========== 段 E：AST ==========
    abstract class St
    {
        public int Line;
    }

    sealed class LetS : St { public string Name; public Ex Rhs; }
    sealed class FnS : St { public string Name; public string[] Params; public List<St> Body; }
    sealed class ExS : St { public Ex E; }

    abstract class Ex
    {
        public int Line;
    }

    sealed class IntE : Ex { public BigInteger V; }
    sealed class StrE : Ex { public int[] V; }
    sealed class LitE : Ex { public Val V; }                              // true / false / none
    sealed class TabE : Ex { public List<Ex> Items; }
    sealed class NameE : Ex { public string Name; }
    sealed class CallE : Ex { public string Name; public List<Ex> Args; }
    sealed class PostE : Ex { public Ex Callee; public List<Ex> Args; }
    sealed class BlkE : Ex { public List<St> Body; }
    sealed class IdxE : Ex { public Ex T; public Ex I; }
    sealed class SlcE : Ex { public Ex T; public Ex A; public Ex B; }
    sealed class BinE : Ex { public string Op; public Ex L; public Ex R; }
    sealed class UnE : Ex { public string Op; public Ex E; }

    // ========== 段 F：语法 ==========
    sealed class Parser
    {
        public static readonly string[] Keywords = { "let", "fn", "if" };
        public static readonly string[] Builtins = { "str", "print", "num", "bytes", "len", "split", "join", "host" };
        public static readonly string[] NotThere =
            { "while", "for", "break", "continue", "return", "else", "range", "map", "filter", "fold", "sort" };

        static readonly HashSet<string> Reserved = Build();
        static HashSet<string> Build()
        {
            var h = new HashSet<string>();
            foreach (var s in Keywords) h.Add(s);
            foreach (var s in Builtins) h.Add(s);
            foreach (var s in NotThere) h.Add(s);
            return h;
        }

        public static bool IsBuiltin(string n) { return Array.IndexOf(Builtins, n) >= 0; }
        public static bool IsNotThere(string n) { return Array.IndexOf(NotThere, n) >= 0; }
        static bool IsKeyword(string n) { return Array.IndexOf(Keywords, n) >= 0; }

        readonly List<Token> ts;
        int p;
        int curLine;              // 正在解析的那一句从哪一行起
        int gdepth;               // 组嵌套总深度（宿主自己的防爆栈上限）

        static readonly string[][] Levels =
        {
            new[] { "||" },
            new[] { "&&" },
            new[] { "==", "!=" },
            new[] { "<", "<=", ">", ">=" },
            new[] { "+", "-" },
            new[] { "*", "/", "%" },
        };

        public Parser(List<Token> tokens) { ts = tokens; }

        Token Cur { get { return ts[p]; } }
        void Adv() { if (ts[p].K != Tk.Eof) p++; }

        // 严格读法（维护者 2026-11-08 定调）：一句话绝对不许跨行。
        // 换行永远是句子分界 —— 括号、方括号、逗号、运算符、等号、函数头后面都不算例外。
        // 块（{}）里的多句话各占一行仍然合法，那是好几个句子，不是一个句子跨行。
        // 所以这里不再有任何「跳过换行」的口子：碰到换行就是这一句到头了 -> E11。

        bool At(params string[] puncts)
        {
            if (Cur.K != Tk.Punct) return false;
            foreach (var s in puncts) if (Cur.S == s) return true;
            return false;
        }

        DropErr Err(int code, string why)
        {
            int line = Cur.K == Tk.Eof && p > 0 ? ts[p - 1].Line : Cur.Line;
            return new DropErr(code, line, why);
        }

        DropErr E3(string why) { return Err(3, "语法错：" + why); }

        Token Take(string punct)
        {
            if (At(punct)) { var t = Cur; Adv(); return t; }
            if (Cur.K == Tk.Eof || Cur.K == Tk.New) throw Err(11, "话说到一半：这里该有「" + punct + "」，句子先到头了");
            throw E3("这里该有「" + punct + "」，实际是" + Cur.Show());
        }

        bool AtLineEnd() { return Cur.K == Tk.New || Cur.K == Tk.Eof; }
        // 「同一行」= 还在写下这一句的那一行上；换行记号本身意味着这一句到头了。
        bool SameLine() { return Cur.K == Tk.Eof || (Cur.K != Tk.New && Cur.Line == curLine); }

        // 该有逗号 / 收尾符的那个位置（维护者 2026-11-08 第二次定调）：
        // 还露着真记号 —— 定义说「形状不对一律 E3：少括号少逗号」，所以这里算 E3；
        // 句子或文件到这里就断了、什么都不剩 —— 定义说「括号没闭合是 E11」，所以只有这种算 E11。
        // 同一个位置既可能缺收尾符也可能缺逗号，定义把它们归给同一个码，消息里不必分开。
        DropErr Close(string closer, string why11, string what)
        {
            if (AtLineEnd()) return Err(11, "话说到一半：" + why11);
            return Err(3, "语法错：" + what + "，这里该有「" + closer + "」，实际是" + Cur.Show());
        }

        // ---------- 句子表 ----------
        public List<St> Program()
        {
            var body = Block(true);
            while (Cur.K == Tk.New) Adv();
            if (Cur.K != Tk.Eof) throw E3("这里不该还有东西（" + Cur.Show() + "）");
            return body;
        }

        List<St> Block(bool top)
        {
            var list = new List<St>();
            while (true)
            {
                while (Cur.K == Tk.New) Adv();
                if (Cur.K == Tk.Eof)
                {
                    if (top) return list;
                    throw Err(11, "话说到一半：「{」没有配上「}」，文件就到了尾");
                }
                if (!top && At("}")) return list;
                St s = Stmt();
                list.Add(s);
                // 顶层和块里同一套规矩（维护者 2026-11-08 第二次定调）：
                // 「一行一句」说的是「一句话不许跨行」，不是「一句独占一行」；
                // 所以顶层写 let a = 1 let b = 2 也合法，原先这里多出来的那条 E3 已抹掉。
                // 句子结束后剩下什么也不算：换行由下一圈开头吃掉，「}」由下一圈判断，
                // 其余记号就是同一行里的下一句。
            }
        }

        St Stmt()
        {
            if (Cur.K == Tk.Semi) throw E3("分号不是东西");
            if (At("=")) throw E3("没有赋值这回事（x = 1 是语法错）");
            if (Cur.K == Tk.Name && Cur.S == "let") return LetStmt();
            if (Cur.K == Tk.Name && Cur.S == "fn") return FnStmt();
            curLine = Cur.Line;
            return new ExS { Line = Cur.Line, E = E() };
        }

        Ex E() { return Bin(0); }

        St LetStmt()
        {
            int ln = Cur.Line;
            Adv();                                                  // let
            if (Cur.K != Tk.Name) throw E3(Cur.Show() + "不能当名字用");
            string nm = Cur.S;
            if (Reserved.Contains(nm)) throw E3("「" + nm + "」是保留名，占了");
            Adv();
            curLine = ln;
            if (AtLineEnd()) throw Err(11, "话说到一半：let 只有左边，没有右边");
            Take("=");
            if (AtLineEnd()) throw Err(11, "话说到一半：「=」右边没有东西");
            var rhs = E();
            return new LetS { Line = ln, Name = nm, Rhs = rhs };
        }

        St FnStmt()
        {
            int ln = Cur.Line;
            curLine = ln;
            Adv();                                                  // fn
            if (Cur.K != Tk.Name) throw E3(Cur.Show() + "不能当函数名");
            string nm = Cur.S;
            if (Reserved.Contains(nm)) throw E3("「" + nm + "」是保留名，占了");
            Adv();
            var ps = new List<string>();
            var seen = new HashSet<string>();
            Take("(");
            if (!At(")"))
            {
                while (true)
                {
                    if (AtLineEnd()) throw Err(11, "话说到一半：形参表没有闭合");
                    if (Cur.K != Tk.Name) throw E3("形参必须是名字，实际是" + Cur.Show());
                    string q = Cur.S;
                    if (Reserved.Contains(q)) throw E3("「" + q + "」是保留名，占了");
                    if (!seen.Add(q)) throw Err(12, "形参重名：「" + q + "」出现两次");
                    ps.Add(q);
                    Adv();
                    if (At(",")) { Adv(); continue; }
                    break;
                }
            }
            Take(")");
            if (Cur.K == Tk.New) throw Err(11, "话说到一半：函数头写完就换行了，「{」得跟在同一行");
            if (Cur.K == Tk.Eof) throw Err(11, "话说到一半：函数没有体，文件就到了尾");
            if (!At("{"))
                throw E3("函数只有一种写法：名字 + 形参 + 块（这里该有「{」，实际是" + Cur.Show() + "）");
            var body = BracedBlock();
            return new FnS { Line = ln, Name = nm, Params = ps.ToArray(), Body = body };
        }

        List<St> BracedBlock()
        {
            Take("{");
            gdepth++;
            if (gdepth > 64) throw E3("块套得太深（宿主上限 64 层）");
            int savedLine = curLine;
            curLine = Cur.Line;
            List<St> body;
            try { body = Block(false); }
            finally { gdepth--; }
            curLine = savedLine;
            Take("}");
            return body;
        }

        // ---------- 表达式 ----------
        Ex Bin(int lvl)
        {
            if (lvl == Levels.Length) return Unary();
            var left = Bin(lvl + 1);
            while (true)
            {
                if (!SameLine()) break;
                string op = null;
                if (Cur.K == Tk.Punct) foreach (var o in Levels[lvl]) if (Cur.S == o) { op = o; break; }
                if (op == null) break;
                int ol = Cur.Line;
                Adv();
                if (!SameLine()) throw new DropErr(11, ol, "话说到一半：「" + op + "」右边没有东西");
                var right = Bin(lvl + 1);
                left = new BinE { Line = ol, Op = op, L = left, R = right };
            }
            return left;
        }

        Ex Unary()
        {
            if (SameLine() && Cur.K == Tk.Punct && (Cur.S == "-" || Cur.S == "!"))
            {
                string op = Cur.S;
                int ln = Cur.Line;
                Adv();
                if (!SameLine()) throw new DropErr(11, ln, "话说到一半：「" + op + "」后面没有东西");
                return new UnE { Line = ln, Op = op, E = Unary() };
            }
            return Postfix();
        }

        Ex Postfix()
        {
            var e = Primary();
            while (true)
            {
                if (!(SameLine() && Cur.K == Tk.Punct)) break;
                if (Cur.S == "(") { e = CallOn(e); continue; }
                if (Cur.S == "[") { e = Bracket(e); continue; }
                break;
            }
            return e;
        }

        Ex CallOn(Ex callee)
        {
            int ln = Cur.Line;
            var args = Args();
            var ne = callee as NameE;
            if (ne != null) return new CallE { Line = ln, Name = ne.Name, Args = args };
            return new PostE { Line = ln, Callee = callee, Args = args };
        }

        List<Ex> Args()
        {
            Take("(");
            gdepth++;
            if (gdepth > 128) throw E3("括号套得太深（宿主上限 128 层）");
            var args = new List<Ex>();
            try
            {
                if (!At(")"))
                {
                    while (true)
                    {
                        if (AtLineEnd()) throw Err(11, "话说到一半：调用的实参没写完");
                        args.Add(E());
                        if (At(",")) { Adv(); continue; }
                        break;
                    }
                }
                if (!At(")")) throw Close(")", "调用的「(」没有配上「)」", "调用没写完");
                Adv();
            }
            finally { gdepth--; }
            return args;
        }

        Ex Bracket(Ex target)
        {
            int ln = Cur.Line;
            Take("[");
            if (Cur.K == Tk.New) throw Err(11, "话说到一半：「[」后面就换行了");
            gdepth++;
            if (gdepth > 128) throw E3("括号套得太深（宿主上限 128 层）");
            Ex a = null, b = null;
            bool slice = false;
            try
            {
                if (!At("]"))
                {
                    if (!At(":")) a = E();                          // 下端可省：xs[:3]
                    if (At(":"))
                    {
                        slice = true;
                        Adv();
                        if (!At("]")) b = E();                      // 上端可省：xs[3:]，两端都省 xs[:]
                    }
                }
                if (!At("]")) throw Close("]", "「[」没有配上「]」", "下标没写完");
                Adv();
            }
            finally { gdepth--; }
            if (!slice && a == null) throw new DropErr(3, ln, "语法错：「[]」中间要有一个下标（切片要有冒号）");
            return slice ? (Ex)new SlcE { Line = ln, T = target, A = a, B = b }
                         : (Ex)new IdxE { Line = ln, T = target, I = a };
        }

        Ex Primary()
        {
            Token t = Cur;
            if (t.K == Tk.Int) { Adv(); return new IntE { Line = t.Line, V = t.I }; }
            if (t.K == Tk.Str) { Adv(); return new StrE { Line = t.Line, V = t.Txt }; }
            if (t.K == Tk.Name) return NameExpr();
            if (t.K == Tk.Semi) throw E3("分号不是东西");
            if (t.K == Tk.New) throw Err(11, "话说到一半：这一句没有头");
            if (t.K == Tk.Eof) throw Err(11, "话说到一半：文件到这里就没头没尾");
            if (t.S == "(")
            {
                Adv();
                if (Cur.K == Tk.New) throw Err(11, "话说到一半：「(」后面就换行了");
                var e = E();
                if (!At(")")) throw Close(")", "「(」没有配上「)」", "括号没闭合");
                Adv();
                return e;                                                  // 括号只是表达式
            }
            if (t.S == "[")
            {
                Adv();
                gdepth++;
                if (gdepth > 128) throw E3("括号套得太深（宿主上限 128 层）");
                var items = new List<Ex>();
                try
                {
                    if (!At("]"))
                    {
                        while (true)
                        {
                            if (AtLineEnd()) throw Err(11, "话说到一半：表字面量没写完");
                            items.Add(E());
                            if (At(",")) { Adv(); continue; }
                            break;
                        }
                    }
                    if (!At("]")) throw Close("]", "表字面量的「[」没有配上「]」", "表字面量没闭合");
                    Adv();
                }
                finally { gdepth--; }
                return new TabE { Line = t.Line, Items = items };
            }
            if (t.S == "{") return BlockExpr();
            if (t.S == "=") throw E3("没有赋值这回事（x = 1 是语法错）");
            if (t.S == ")" || t.S == "]" || t.S == "}")
                throw E3("多出来一个「" + t.S + "」，形状不对（尾逗号这类写法定义没放行，这里按形状不对算）");
            if (t.S == ",") throw E3("这里不该有逗号");
            if (t.S == ":") throw E3("这里不该有冒号");
            throw E3("这里不该有" + t.Show());
        }

        Ex NameExpr()
        {
            Token t = Cur;
            string n = t.S;
            if (IsNotThere(n)) throw E3("「" + n + "」这里没有那种东西");
            if (n == "if" || IsBuiltin(n))
            {
                Adv();
                // 名字和它的「(」之间跨了行：那是「话说到一半」；不换行时缺「(」仍然按形状不对算 E3
                if (Cur.K == Tk.New && p + 1 < ts.Count && ts[p + 1].K == Tk.Punct && ts[p + 1].S == "(")
                    throw Err(11, "话说到一半：「" + n + "」和它的「(」不能分行写");
                if (t.K == Tk.Name && n == "if" && !At("(")) throw E3("if 只有一种写法：if(c, a, b)");
                if (!At("(")) throw E3("内置名「" + n + "」只能调用，不能当值用（let f = " + n + " 是语法错）");
                var args = Args();
                return new CallE { Line = t.Line, Name = n, Args = args };
            }
            if (IsKeyword(n)) throw E3("「" + n + "」是关键字，不能当名字使");
            if (n == "true") { Adv(); return new LitE { Line = t.Line, V = BoolV.T }; }
            if (n == "false") { Adv(); return new LitE { Line = t.Line, V = BoolV.F }; }
            if (n == "none") { Adv(); return new LitE { Line = t.Line, V = NoneV.I }; }
            Adv();
            return new NameE { Line = t.Line, Name = n };
        }

        Ex BlockExpr()
        {
            int ln = Cur.Line;
            var body = BracedBlock();
            return new BlkE { Line = ln, Body = body };
        }
    }

    // ========== 段 G：环境与闭包 ==========
    // 一个作用域 = 一条会往下长的绑定链；Scope 上另留一个「此刻最新」的指针。
    // 查找分两步，正好对上定义的两句话：
    //   「重名 = 新绑定遮住旧的，已绑的不会被改动」+「块里的局部函数捕获定义那一刻的环境」
    //     -> 第一趟只走捕获那一刻已经在的这条链，先撞上谁就是谁；
    //   「前向引用别的函数只在调用时才成立」
    //     -> 第一趟没撞上，才去看各层作用域的最新链（那是写下之后才出现的名字）。
    sealed class Scope
    {
        public Env Latest;
    }

    sealed class Env
    {
        public readonly Env Parent;
        public readonly string Name;
        public readonly object Val;                              // Val 或 FuncDef
        public readonly Scope Sc;
        public Env(Scope sc, Env parent, string n, object v)
        {
            Sc = sc; Parent = parent; Name = n; Val = v;
        }

        public static object Find(Env e, string n)
        {
            for (var x = e; x != null; x = x.Parent) if (x.Name == n) return x.Val;
            for (var x = e; x != null; x = x.Parent)
            {
                var s = x.Sc;
                if (s == null) continue;
                var p = x.Parent;
                if (p != null && p.Sc == s) continue;              // 只在跨过这个作用域的那个绑定上看一次
                if (s.Latest == null) continue;
                for (var y = s.Latest; y != null && y.Sc == s; y = y.Parent) if (y.Name == n) return y.Val;
            }
            return null;
        }
    }

    sealed class FuncDef
    {
        public string Name;
        public string[] Params;
        public List<St> Body;
        public Env Closure;                                    // 定义那一刻的环境
    }

    sealed class TailJump : Val                                // 只在本宿主的蹦床里走，语言里看不见
    {
        public FuncDef Fn;
        public Val[] Args;
    }

    // ========== 段 H：求值 ==========
    sealed class Interp
    {
        public readonly Ctx C;
        readonly TextWriter screen;
        public Env Top;
        public readonly Scope Global = new Scope();            // 并起来的那一个程序的顶层作用域
        public readonly int SelfCap = 200;                     // 定义：非尾自身调用最多叠 200 层
        public readonly int TotalCap = 4000;                   // 宿主自己的保险丝（定义没写）

        public Interp(Ctx ctx, TextWriter outStream) { C = ctx; screen = outStream; }

        // ---------- 句子 ----------
        public void Exec(List<St> body)
        {
            Env env = Top;
            for (int i = 0; i < body.Count; i++)
            {
                St s = body[i];
                C.StmtLine = s.Line;
                C.CurLine = s.Line;
                C.Lit = 0;
                if (s is LetS)
                {
                    var l = (LetS)s;
                    env = Add(Global, env, l.Name, Eval(l.Rhs, env, false));
                }
                else if (s is FnS)
                {
                    env = BindFn(Global, env, (FnS)s);
                }
                else
                {
                    Eval(((ExS)s).E, env, false);              // 表达式单独一行：算它、丢掉
                }
            }
            Top = env;
        }

        static Env Add(Scope sc, Env env, string name, Val v)
        {
            var e = new Env(sc, env, name, v);
            sc.Latest = e;
            return e;
        }

        static Env BindFn(Scope sc, Env env, FnS f)
        {
            var fd = new FuncDef { Name = f.Name, Params = f.Params, Body = f.Body };
            var e2 = new Env(sc, env, f.Name, fd);
            sc.Latest = e2;
            fd.Closure = e2;                                   // 递归可以引用自己
            return e2;
        }

        Val ExecBody(List<St> body, Env env)
        {
            var sc = new Scope();                              // 块里也能定义局部的东西，只在这个块里可见
            for (int i = 0; i < body.Count; i++)
            {
                St s = body[i];
                bool last = i == body.Count - 1;
                if (s is LetS)
                {
                    var l = (LetS)s;
                    C.CurLine = l.Line;
                    env = Add(sc, env, l.Name, Eval(l.Rhs, env, false));
                }
                else if (s is FnS)
                {
                    C.CurLine = s.Line;
                    env = BindFn(sc, env, (FnS)s);
                }
                else
                {
                    var x = (ExS)s;
                    C.CurLine = x.Line;
                    if (last) return Eval(x.E, env, true);     // 尾位 = 函数体块里的最后一句
                    Eval(x.E, env, false);
                }
            }
            return NoneV.I;                                    // {} 的值是无
        }

        // ---------- 表达式 ----------
        Val Eval(Ex e, Env env, bool tail)
        {
            C.CurLine = e.Line;
            if (e is IntE) return new IntV(((IntE)e).V);
            if (e is StrE) return new StrV(((StrE)e).V);
            if (e is LitE) return ((LitE)e).V;
            if (e is NameE) return NameVal((NameE)e, env);
            if (e is TabE) return TabVal((TabE)e, env);
            if (e is CallE) return NamedCall((CallE)e, env, tail);
            if (e is PostE) return PostCall((PostE)e, env);
            if (e is BlkE) return BlkVal((BlkE)e, env);
            if (e is IdxE) return IdxVal((IdxE)e, env);
            if (e is SlcE) return SlcVal((SlcE)e, env);
            if (e is UnE) return UnVal((UnE)e, env);
            if (e is BinE) return BinVal((BinE)e, env);
            throw C.E(3, "语法错：宿主不认识这种式子");
        }

        Val NameVal(NameE n, Env env)
        {
            object o = Env.Find(env, n.Name);
            if (o == null) throw C.E(1, "没绑过的名字：「" + n.Name + "」");
            var fd = o as FuncDef;
            if (fd != null) throw C.E(3, "函数不是值：「" + fd.Name + "」不能当值用");
            return (Val)o;
        }

        Val TabVal(TabE t, Env env)
        {
            int saved = C.Lit;
            C.Lit = t.Line;
            var items = new List<Val>(t.Items.Count);
            try
            {
                foreach (var x in t.Items) items.Add(Eval(x, env, false));
            }
            finally { C.Lit = saved; }
            return new TabV(items);
        }

        Val BlkVal(BlkE b, Env env)
        {
            int savedLit = C.Lit;
            C.Lit = 0;
            try { return ExecBody(b.Body, env); }
            finally { C.Lit = savedLit; }
        }

        Val[] ArgVals(List<Ex> xs, Env env)
        {
            var a = new Val[xs.Count];
            for (int i = 0; i < xs.Count; i++) a[i] = Eval(xs[i], env, false);   // 从左到右，实参不在尾位
            return a;
        }

        Val NamedCall(CallE c, Env env, bool tail)
        {
            string n = c.Name;
            if (n == "if")
            {
                if (c.Args.Count != 3)
                    throw C.E(3, "语法错：if 只有一种写法 if(c, a, b)，这里给了 " + c.Args.Count + " 支");
                var cv = Eval(c.Args[0], env, false);
                var bt = cv as BoolV;
                if (bt == null) throw C.E(7, "if 的条件必须是 true/false，这里是" + U.Kind(cv));
                return Eval(bt.V ? c.Args[1] : c.Args[2], env, tail);            // 只算选中的那支
            }
            if (Parser.IsBuiltin(n))
            {
                var b = Builtin.For(n);
                if (c.Args.Count != b.Argc)
                    throw C.E(2, "内置「" + n + "」要 " + b.Argc + " 个参数，这里给了 " + c.Args.Count + " 个", c.Line);
                var bav = ArgsOf(c.Args, env);
                C.CurLine = c.Line;                                // 内置出错时，「那个记号」就是这次调用
                return b.Fn(this, bav);
            }
            object o = Env.Find(env, n);
            if (o == null) throw C.E(1, "没绑过的名字：「" + n + "」");
            var fd = o as FuncDef;
            if (fd == null) throw C.E(4, "不是函数不能调用：「" + n + "」是" + U.Kind((Val)o));
            if (c.Args.Count != fd.Params.Length)
                throw C.E(2, "「" + n + "」要 " + fd.Params.Length + " 个参数，这里给了 " + c.Args.Count + " 个", c.Line);
            var args = ArgsOf(c.Args, env);
            if (tail && C.Frames.Count > 0 && C.Frames[C.Frames.Count - 1].Fn == fd)
                return new TailJump { Fn = fd, Args = args };                    // 尾位调用自己 = 转
            return Run(fd, args, c.Line);
        }

        Val[] ArgsOf(List<Ex> xs, Env env)
        {
            var a = new Val[xs.Count];
            for (int i = 0; i < xs.Count; i++) a[i] = Eval(xs[i], env, false);
            return a;
        }

        Val PostCall(PostE c, Env env)
        {
            var callee = Eval(c.Callee, env, false);
            ArgsOf(c.Args, env);
            throw C.E(4, "不是函数不能调用：只有具名函数和内置名能被调，这里被调用的是" + U.Kind(callee));
        }

        Env BindArgs(FuncDef f, Val[] args)
        {
            var sc = new Scope();                              // 形参单独占一层，挡住外面重名
            var env = f.Closure;
            for (int i = 0; i < f.Params.Length; i++) env = new Env(sc, env, f.Params[i], args[i]);
            if (f.Params.Length > 0) sc.Latest = env;
            return env;
        }

        Val Run(FuncDef f, Val[] args, int callLine)
        {
            int self = 0;
            for (int i = 0; i < C.Frames.Count; i++) if (C.Frames[i].Fn == f) self++;
            // E13 报「最里头那一次没成功的调用」在哪一行 —— 就是这次 Run 要进去的那个调用记号
            if (self >= SelfCap)
                throw C.E(13, "「" + f.Name + "」的非尾自身调用叠到第 " + (SelfCap + 1) + " 层，进不去", callLine);
            if (C.Frames.Count >= TotalCap)
                throw C.E(13, "调用叠到宿主上限 " + TotalCap + " 层（互相调用在定义里没有「转」的语义）", callLine);
            var env = BindArgs(f, args);
            C.Frames.Add(new Frame { Fn = f, Env = env });
            try
            {
                while (true)
                {
                    Val r = ExecBody(f.Body, env);
                    var tj = r as TailJump;
                    if (tj == null) return r;
                    env = BindArgs(f, tj.Args);                                  // 跳回函数开头，不涨层数
                    C.Frames[C.Frames.Count - 1].Env = env;
                }
            }
            finally { C.Frames.RemoveAt(C.Frames.Count - 1); }
        }

        // ---------- 下标 / 切片 ----------
        int Idx(Val v)
        {
            var iv = v as IntV;
            if (iv == null) throw C.E(5, "下标必须是整数，这里是" + U.Kind(v));
            if (iv.V > int.MaxValue || iv.V < int.MinValue) throw C.E(6, "下标大到不在范围内");
            return (int)iv.V;
        }

        Val IdxVal(IdxE x, Env env)
        {
            var t = Eval(x.T, env, false);
            int i = Idx(Eval(x.I, env, false));
            if (t is TabV)
            {
                var it = ((TabV)t).Items;
                if (i < 0 || i >= it.Count) throw C.E(6, "下标越界：长度 " + it.Count + "，要的是第 " + i + " 个");
                return it[i];
            }
            if (t is StrV)
            {
                var cps = ((StrV)t).Cp;
                if (i < 0 || i >= cps.Length) throw C.E(6, "下标越界：长度 " + cps.Length + "，要的是第 " + i + " 个");
                return new StrV(new[] { cps[i] });
            }
            throw C.E(5, "只有表和字符串能下标，这里是" + U.Kind(t));
        }

        Val SlcVal(SlcE x, Env env)
        {
            var t = Eval(x.T, env, false);
            int a = x.A == null ? 0 : Idx(Eval(x.A, env, false));
            int b = x.B == null ? int.MaxValue : Idx(Eval(x.B, env, false));
            if (t is TabV)
            {
                var src = ((TabV)t).Items;
                int lo = Clamp(a, src.Count), hi = Clamp(b, src.Count);
                var cut = new List<Val>();
                for (int i = lo; i < hi; i++) cut.Add(src[i]);
                return new TabV(cut);
            }
            if (t is StrV)
            {
                var src = ((StrV)t).Cp;
                int lo = Clamp(a, src.Length), hi = Clamp(b, src.Length);
                return new StrV(U.Slice(src, lo, hi));
            }
            throw C.E(5, "只有表和字符串能切片，这里是" + U.Kind(t));
        }

        static int Clamp(int i, int n)
        {
            if (i < 0) return 0;                                                 // 负数怎么办定义没说（REPORT）
            return i > n ? n : i;
        }

        // ---------- 运算 ----------
        BigInteger Tally(Val v, string op)
        {
            var i = v as IntV;
            if (i == null) throw C.E(5, "「" + op + "」只吃整数，这边是" + U.Kind(v));
            return i.V;
        }

        BoolV Need(Val v, string op)
        {
            var b = v as BoolV;
            if (b == null) throw C.E(7, "「" + op + "」只吃 true/false，这里是" + U.Kind(v));
            return b;
        }

        Val UnVal(UnE u, Env env)
        {
            var v = Eval(u.E, env, false);
            if (u.Op == "!")
            {
                var b = Need(v, "!");
                return BoolV.Of(!b.V);
            }
            var i = v as IntV;
            if (i == null) throw C.E(5, "取负只吃整数，这里是" + U.Kind(v));
            return new IntV(-i.V);
        }

        Val BinVal(BinE b, Env env)
        {
            string op = b.Op;
            if (op == "&&")
            {
                if (!Need(Eval(b.L, env, false), "&&").V) return BoolV.F;         // 左边定了就不算右边
                return BoolV.Of(Need(Eval(b.R, env, false), "&&").V);
            }
            if (op == "||")
            {
                if (Need(Eval(b.L, env, false), "||").V) return BoolV.T;
                return BoolV.Of(Need(Eval(b.R, env, false), "||").V);
            }
            var x = Eval(b.L, env, false);
            var y = Eval(b.R, env, false);
            if (op == "==") return BoolV.Of(Same(x, y));
            if (op == "!=") return BoolV.Of(!Same(x, y));
            if (op == "+") return Plus(x, y);
            if (op == "-") return new IntV(Tally(x, "-") - Tally(y, "-"));
            if (op == "*") return new IntV(Tally(x, "*") * Tally(y, "*"));
            if (op == "/")
            {
                var d = Tally(y, "/");
                if (d.IsZero) throw C.E(8, "除数为 0");
                return new IntV(BigInteger.Divide(Tally(x, "/"), d));             // 向零取整
            }
            if (op == "%")
            {
                var d = Tally(y, "%");
                if (d.IsZero) throw C.E(8, "除数为 0");
                return new IntV(BigInteger.Remainder(Tally(x, "%"), d));          // 跟被除数同号
            }
            if (op == "<" || op == "<=" || op == ">" || op == ">=") return Cmp(op, x, y);
            throw C.E(3, "语法错：不认识的运算符「" + op + "」");
        }

        Val Cmp(string op, Val x, Val y)
        {
            int r;
            var xi = x as IntV;
            var yi = y as IntV;
            var xs = x as StrV;
            var ys = y as StrV;
            if (xi != null && yi != null) r = xi.V.CompareTo(yi.V);
            else if (xs != null && ys != null) r = U.CmpCp(xs.Cp, ys.Cp);
            else if (x is BoolV || y is BoolV || x is NoneV || y is NoneV)
                throw C.E(5, "true/false/none 不能比大小");
            else throw C.E(5, "比大小要两边同为整数或同为字符串，这里是" + U.Kind(x) + "和" + U.Kind(y));
            if (op == "<") return BoolV.Of(r < 0);
            if (op == "<=") return BoolV.Of(r <= 0);
            if (op == ">") return BoolV.Of(r > 0);
            return BoolV.Of(r >= 0);
        }

        Val Plus(Val x, Val y)
        {
            var xi = x as IntV;
            var yi = y as IntV;
            if (xi != null && yi != null) return new IntV(xi.V + yi.V);
            var xs = x as StrV;
            var ys = y as StrV;
            if (xs != null && ys != null) return new StrV(Concat(xs.Cp, ys.Cp));
            var xt = x as TabV;
            var yt = y as TabV;
            if (xt != null && yt != null)
            {
                var c = new List<Val>(xt.Items);
                c.AddRange(yt.Items);
                return new TabV(c);
            }
            throw C.E(5, "「+」只做两个整数相加、两个字符串拼接或两个表拼接，这里是" + U.Kind(x) + "和" + U.Kind(y));
        }

        static int[] Concat(int[] a, int[] b)
        {
            var c = new int[a.Length + b.Length];
            Array.Copy(a, 0, c, 0, a.Length);
            Array.Copy(b, 0, c, a.Length, b.Length);
            return c;
        }

        public static bool Same(Val x, Val y)
        {
            var xi = x as IntV;
            var yi = y as IntV;
            if (xi != null && yi != null) return xi.V == yi.V;
            var xs = x as StrV;
            var ys = y as StrV;
            if (xs != null && ys != null) return U.CmpCp(xs.Cp, ys.Cp) == 0;
            var xb = x as BoolV;
            var yb = y as BoolV;
            if (xb != null && yb != null) return xb.V == yb.V;
            if (x is NoneV && y is NoneV) return true;
            var xt = x as TabV;
            var yt = y as TabV;
            if (xt != null && yt != null)
            {
                if (xt.Items.Count != yt.Items.Count) return false;
                for (int i = 0; i < xt.Items.Count; i++) if (!Same(xt.Items[i], yt.Items[i])) return false;
                return true;
            }
            return false;                                                    // 不同种一律 false
        }

        // ---------- str：唯一的「值变文本」 ----------
        // 顶层给字符串就是它自己（不加引号）；只有当它是表里的元素时才套引号并转义。
        public static void StrInto(StringBuilder sb, Val v) { StrInto(sb, v, false); }

        static void StrInto(StringBuilder sb, Val v, bool inTab)
        {
            if (v is IntV) { sb.Append(((IntV)v).V.ToString(CultureInfo.InvariantCulture)); return; }
            var s = v as StrV;
            if (s != null)
            {
                if (!inTab) { sb.Append(s.S); return; }
                sb.Append('"');
                foreach (int c in s.Cp)
                {
                    if (c == '\\') sb.Append("\\\\");
                    else if (c == '"') sb.Append("\\\"");
                    else sb.Append(char.ConvertFromUtf32(c));
                }
                sb.Append('"');
                return;
            }
            var b = v as BoolV;
            if (b != null) { sb.Append(b.V ? "true" : "false"); return; }
            if (v is NoneV) { sb.Append("none"); return; }
            var t = (TabV)v;
            sb.Append('[');
            for (int i = 0; i < t.Items.Count; i++)
            {
                if (i > 0) sb.Append(',');
                StrInto(sb, t.Items[i], true);
            }
            sb.Append(']');
        }

        public void Print(string s) { screen.Write(s); screen.Write('\n'); screen.Flush(); }
    }

    // ========== 段 I：内置名 ==========
    sealed class BI
    {
        public string Name;
        public int Argc;
        public Func<Interp, Val[], Val> Fn;
    }

    static class Builtin
    {
        static readonly Dictionary<string, BI> All = Build();

        public static BI For(string n) { BI b; return All.TryGetValue(n, out b) ? b : null; }

        static StrV WantStr(Ctx c, Val v, string who)
        {
            var s = v as StrV;
            if (s == null) throw c.E(10, who + "要字符串，这边是" + U.Kind(v));
            return s;
        }

        static Dictionary<string, BI> Build()
        {
            var d = new Dictionary<string, BI>();

            d["str"] = new BI
            {
                Name = "str",
                Argc = 1,
                Fn = (ip, a) =>
                {
                    var sb = new StringBuilder();
                    Interp.StrInto(sb, a[0]);
                    return StrV.Of(sb.ToString());
                }
            };

            d["print"] = new BI
            {
                Name = "print",
                Argc = 1,
                Fn = (ip, a) =>
                {
                    var s = a[0] as StrV;
                    if (s == null)
                        throw ip.C.E(10, "print 只收字符串，这里是" + U.Kind(a[0]) + "（打数字要写 print(str(...))）");
                    ip.Print(s.S);
                    return NoneV.I;
                }
            };

            d["num"] = new BI
            {
                Name = "num",
                Argc = 1,
                Fn = (ip, a) =>
                {
                    var s = WantStr(ip.C, a[0], "num");
                    int[] c = s.Cp;
                    int i = 0;
                    bool neg = false;
                    if (c.Length > 0 && c[0] == '-') { neg = true; i = 1; }
                    if (i >= c.Length) throw ip.C.E(9, "num：该是整数的地方不是（空串不算整数）");
                    BigInteger v = BigInteger.Zero;
                    for (; i < c.Length; i++)
                    {
                        if (c[i] < '0' || c[i] > '9')
                            throw ip.C.E(9, "num：只认「可选负号紧贴一串数字」，前后空格也算别的");
                        v = v * 10 + (c[i] - '0');
                    }
                    return new IntV(neg ? -v : v);
                }
            };

            d["bytes"] = new BI
            {
                Name = "bytes",
                Argc = 1,
                Fn = (ip, a) =>
                {
                    var s = WantStr(ip.C, a[0], "bytes");
                    var outp = new List<int>(s.Cp.Length);
                    foreach (int cp in s.Cp)
                    {
                        if (cp <= 255) { outp.Add(cp); continue; }
                        foreach (byte b in Encoding.UTF8.GetBytes(char.ConvertFromUtf32(cp))) outp.Add(b);
                    }
                    return new StrV(outp.ToArray());
                }
            };

            d["len"] = new BI
            {
                Name = "len",
                Argc = 1,
                Fn = (ip, a) =>
                {
                    var s = a[0] as StrV;
                    if (s != null) return new IntV(new BigInteger(s.Cp.Length));
                    var t = a[0] as TabV;
                    if (t != null) return new IntV(new BigInteger(t.Items.Count));
                    throw ip.C.E(5, "len 只看字符串和表，这里是" + U.Kind(a[0]));
                }
            };

            d["split"] = new BI
            {
                Name = "split",
                Argc = 2,
                Fn = (ip, a) =>
                {
                    var s = WantStr(ip.C, a[0], "split 的原串");
                    var sep = WantStr(ip.C, a[1], "split 的分隔符");
                    if (sep.Cp.Length == 0) throw ip.C.E(10, "split 的分隔符不能是空串");
                    var outp = new List<Val>();
                    foreach (var piece in Cut(s.Cp, sep.Cp)) outp.Add(new StrV(piece));
                    return new TabV(outp);
                }
            };

            d["join"] = new BI
            {
                Name = "join",
                Argc = 2,
                Fn = (ip, a) =>
                {
                    var sep = WantStr(ip.C, a[0], "join 的分隔符");
                    var t = a[1] as TabV;
                    if (t == null) throw ip.C.E(10, "join 的第二个参数要表，这里是" + U.Kind(a[1]));
                    var sb = new StringBuilder();
                    for (int i = 0; i < t.Items.Count; i++)
                    {
                        var s = t.Items[i] as StrV;
                        if (s == null) throw ip.C.E(10, "join 只连字符串，表里第 " + i + " 个是" + U.Kind(t.Items[i]));
                        if (i > 0) sb.Append(sep.S);
                        sb.Append(s.S);
                    }
                    return StrV.Of(sb.ToString());
                }
            };

            d["host"] = new BI
            {
                Name = "host",
                Argc = 2,
                Fn = (ip, a) =>
                {
                    var cmd = a[0] as StrV;
                    if (cmd == null) throw ip.C.E(10, "host 的命令要字符串，这里是" + U.Kind(a[0]));
                    var arg = a[1] as StrV;
                    if (arg == null) throw ip.C.E(10, "host 的参数要字符串，这里是" + U.Kind(a[1]));
                    string r = Host.Do(ip.C, cmd.S, arg.S);
                    if (r == null)
                        throw ip.C.E(14, "宿主不肯做：命令只认 bind accept recv send peer close date 这七个，这里是「" + cmd.S + "」");
                    return StrV.Of(r);
                }
            };

            return d;
        }

        // 空段保留：split("a,,b", ",") = ["a","","b"]，split("", ",") = [""]
        static List<int[]> Cut(int[] s, int[] sep)
        {
            var res = new List<int[]>();
            int start = 0;
            for (int i = 0; i + sep.Length <= s.Length; i++)
            {
                bool hit = true;
                for (int j = 0; j < sep.Length; j++) if (s[i + j] != sep[j]) { hit = false; break; }
                if (!hit) continue;
                res.Add(U.Slice(s, start, i));
                i += sep.Length - 1;
                start = i + 1;
            }
            res.Add(U.Slice(s, start, s.Length));
            return res;
        }
    }

    // ========== 段 J：宿主 host 的七个命令（真 TCP） ==========
    class HObj
    {
        public bool Dead;
    }

    sealed class HListen : HObj
    {
        public TcpListener L;
    }

    sealed class HConn : HObj
    {
        public TcpClient C;
        public NetworkStream S;
        public string PeerEp;
    }

    static class Host
    {
        public const int IdleMs = 10000;                    // 定义：10 秒没动静回空串

        public static string Do(Ctx c, string cmd, string arg)
        {
            switch (cmd)
            {
                case "bind": return Bind(c, arg);
                case "accept": return Accept(c, arg);
                case "recv": return Recv(c, arg);
                case "send": return Send(c, arg);
                case "peer": return Peer(c, arg);
                case "close": return Close(c, arg);
                case "date": return Date(c, arg);
            }
            return null;
        }

        // 程序收尾：还开着的句柄一律放下，不然宿主自己吊着不走。
        public static void Shutdown(Ctx c)
        {
            foreach (var kv in c.Handles)
            {
                var cn = kv.Value as HConn;
                if (cn != null) { try { cn.S.Close(); } catch (Exception) { } try { cn.C.Close(); } catch (Exception) { } continue; }
                var li = kv.Value as HListen;
                if (li != null) { try { li.L.Stop(); } catch (Exception) { } }
            }
        }

        static DropErr No(Ctx c, string why) { return c.E(14, "宿主不肯做：" + why); }

        static string Handshake(Ctx c, HObj o)
        {
            string h = c.NextHandle.ToString(CultureInfo.InvariantCulture);
            c.NextHandle++;
            c.Handles[h] = o;
            return h;
        }

        static HObj Lookup(Ctx c, string h)
        {
            HObj o;
            if (!c.Handles.TryGetValue(h, out o)) throw No(c, "没有「" + h + "」这个句柄");
            return o;
        }

        static string Bind(Ctx c, string arg)
        {
            if (arg != "") throw No(c, "bind 只认空串：端口是宿主起来时 --port 给的那个");
            if (c.Port == null) throw No(c, "宿主起来时没给 --port");
            var l = new TcpListener(IPAddress.Any, c.Port.Value);
            // 故意不设 SO_REUSEADDR：端口被占着就是「宿主不肯做」（E14），不跟别人共用一个端口
            try
            {
                l.Start();
            }
            catch (SocketException e)
            {
                throw No(c, "端口 " + c.Port.Value + " 绑不上（" + e.SocketErrorCode + "）");
            }
            return Handshake(c, new HListen { L = l });
        }

        static string Accept(Ctx c, string arg)
        {
            var o = Lookup(c, arg);
            var li = o as HListen;
            if (li == null) throw No(c, "accept 要的是监听句柄");
            if (o.Dead) throw No(c, "那个监听句柄已经关掉了");
            TcpClient cl;
            try { cl = li.L.AcceptTcpClient(); }
            catch (Exception) { throw No(c, "连接没等到（监听已停）"); }
            cl.NoDelay = true;
            cl.ReceiveTimeout = IdleMs;
            string peer = "?";
            try
            {
                var ep = cl.Client.RemoteEndPoint as IPEndPoint;
                if (ep != null) peer = ep.Address.ToString() + ":" + ep.Port.ToString(CultureInfo.InvariantCulture);
            }
            catch (Exception) { }
            return Handshake(c, new HConn { C = cl, S = cl.GetStream(), PeerEp = peer });
        }

        static string Recv(Ctx c, string arg)
        {
            var o = Lookup(c, arg);
            var cn = o as HConn;
            if (cn == null) throw No(c, "recv 要的是连接句柄");
            if (o.Dead) return "";                                   // 关掉的句柄：一开口就是「断」
            var buf = new byte[65536];
            try
            {
                int got = cn.S.Read(buf, 0, buf.Length);             // 阻塞到至少一个字节
                if (got <= 0) { HangUp(cn); return ""; }             // 对端断了
                var cps = new int[got];
                for (int i = 0; i < got; i++) cps[i] = buf[i];       // 一字节一字符
                return U.Text(cps);
            }
            catch (IOException) { HangUp(cn); return ""; }           // 10 秒没动静 = 断
            catch (SocketException) { HangUp(cn); return ""; }
            catch (ObjectDisposedException) { return ""; }
        }

        static void HangUp(HConn cn)
        {
            try { cn.S.Close(); } catch (Exception) { }
            try { cn.C.Close(); } catch (Exception) { }
        }

        static string Send(Ctx c, string arg)
        {
            int nl = arg.IndexOf('\n');
            if (nl < 0) throw No(c, "send 的形状是 句柄 + 换行 + 字节");
            string h = arg.Substring(0, nl);
            var cps = U.Cp(arg.Substring(nl + 1));
            var o = Lookup(c, h);
            var cn = o as HConn;
            if (cn == null) throw No(c, "send 要的是连接句柄");
            if (o.Dead) throw No(c, "那个句柄已经关掉了，发不出东西");
            var bytes = new byte[cps.Length];                        // 换行后面一个字符发一个字节
            for (int i = 0; i < cps.Length; i++)
            {
                if (cps[i] > 255)
                    throw No(c, "第 " + (i + 1) + " 个字符「" + char.ConvertFromUtf32(cps[i]) + "」不是一个字节（先过 bytes()）");
                bytes[i] = (byte)cps[i];
            }
            try
            {
                cn.S.Write(bytes, 0, bytes.Length);                    // 内部会写满为止
                cn.S.Flush();
            }
            catch (Exception) { throw No(c, "对端不收（连接已经断了）"); }
            return "";
        }

        static string Peer(Ctx c, string arg)
        {
            var o = Lookup(c, arg);
            var cn = o as HConn;
            if (cn == null) throw No(c, "peer 要的是连接句柄");
            if (o.Dead) throw No(c, "那个句柄已经关掉了");
            return cn.PeerEp;
        }

        static string Close(Ctx c, string arg)
        {
            var o = Lookup(c, arg);
            if (o.Dead) throw No(c, "那个句柄已经关掉了");
            o.Dead = true;
            var cn = o as HConn;
            if (cn != null) { HangUp(cn); return "ok"; }
            var li = o as HListen;
            if (li != null) { try { li.L.Stop(); } catch (Exception) { } return "ok"; }
            return "ok";
        }

        static string Date(Ctx c, string arg)
        {
            if (arg != "") throw No(c, "date 只认空串");
            // 定义只说「回时钟」，没说什么形状；这里取「星期, DD Mon YYYY HH:MM:SS GMT」这一串（REPORT 里记了）
            return DateTime.UtcNow.ToString("r", CultureInfo.InvariantCulture);
        }
    }

    // ========== 段 K：命令行与总装 ==========
    static class Program
    {
        static int Main(string[] argv)
        {
            int code = 1;
            // 独立线程 + 大栈：蹦床已经把尾调用摊平了，这里只是给深表达式兜底。
            var t = new Thread(() => { code = Run(argv, false); }, 1 << 26);
            t.Start();
            t.Join();
            return code;
        }

        static int Run(string[] argv, bool debug)
        {
            var stdout = new StreamWriter(Console.OpenStandardOutput(), new UTF8Encoding(false)) { AutoFlush = true };
            var stderr = new StreamWriter(Console.OpenStandardError(), new UTF8Encoding(false)) { AutoFlush = true };
            Console.SetOut(stdout);
            Console.SetError(stderr);

            var ctx = new Ctx();
            var files = new List<string>();
            try
            {
                bool wantPort = false;
                for (int i = 0; i < argv.Length; i++)
                {
                    string a = argv[i];
                    if (wantPort)
                    {
                        int port;
                        if (!int.TryParse(a, NumberStyles.None, CultureInfo.InvariantCulture, out port) ||
                            port > 65535) return Die(stderr, "挂：命令行用错：--port 后面要一个端口号");
                        ctx.Port = port;
                        wantPort = false;
                        continue;
                    }
                    if (a == "--port") { wantPort = true; continue; }
                    if (a.StartsWith("--", StringComparison.Ordinal))
                        return Die(stderr, "挂：命令行用错：不认得的选项「" + a + "」");
                    files.Add(a);
                }
                if (wantPort) return Die(stderr, "挂：命令行用错：--port 后面少了端口号");
                if (files.Count == 0)
                    return Die(stderr, "挂：命令行用错：跑法是 [--port <端口>] 库.dr 程序.dr");

                List<Token> toks = LexAll(files);

                List<St> prog;
                try { prog = new Parser(toks).Program(); }        // 先全部解析，再从头执行
                catch (DropErr de) { return Fail(stderr, de, null); }

                var interp = new Interp(ctx, stdout);
                try
                {
                    interp.Exec(prog);
                }
                catch (DropErr de) { return Fail(stderr, de, null); }
                finally { Host.Shutdown(ctx); }
                return 0;
            }
            catch (DropErr de)
            {
                if (de.Code == 0) return Die(stderr, "挂：" + de.Message);   // 文件读不到这类，没有行号
                return Fail(stderr, de, null);
            }
            catch (StackOverflowException)
            {
                return Die(stderr, "挂：式子套得太深，宿主撑不住");           // 到不了这里，留着说明意图
            }
            catch (Exception)
            {
                if (debug) throw;
                return Die(stderr, "挂：宿主自己出了错（这不是十四种里的任何一种）");
            }
        }

        static int Fail(TextWriter err, DropErr de, object unused)
        {
            int line = de.Line > 0 ? de.Line : 1;
            err.WriteLine("挂：第 " + line.ToString(CultureInfo.InvariantCulture) + " 行 E" +
                          de.Code.ToString(CultureInfo.InvariantCulture) + " —— " + de.Message);
            return 1;
        }

        static int Die(TextWriter err, string msg) { err.WriteLine(msg); return 1; }

        // 「宿主的解释器 [--port] 库.dr 程序.dr —— 宿主按这个顺序并起来」：
        // 并起来 = 把几个文件的文本按命令行给的顺序接成一条长串，行号就按这条长串从头数。
        // 文件之间只隔「每个 .dr 末尾本来就带的那一个换行」；再接一个换行就凭空多出一行。
        // 所以这里不再逐文件数行号，也不再在每个文件尾巴上加一个换行记号（维护者第二次定调改的）。
        static List<Token> LexAll(List<string> files)
        {
            var text = new List<int>();
            foreach (var path in files)
            {
                int[] src;
                try
                {
                    byte[] raw = File.ReadAllBytes(path);
                    src = U.Cp(Encoding.UTF8.GetString(raw));
                }
                catch (Exception) { throw new DropErr(0, 0, "读不到文件：「" + path + "」"); }
                if (src.Length > 0 && src[0] == 0xFEFF) src = U.Slice(src, 1, src.Length);   // BOM
                text.AddRange(src);
            }
            int[] a = text.ToArray();
            var toks = new List<Token>();
            int line = 0, p = 0, n = a.Length;
            while (p < n)
            {
                line++;
                int st = p;
                while (p < n && a[p] != '\n') p++;
                int en = p;
                if (p < n) p++;                                          // 吃掉换行
                if (en > st && a[en - 1] == '\r') en--;                  // 行尾那个回车符不算内容
                toks.AddRange(Lexer.Line(line, U.Slice(a, st, en)));
                toks.Add(new Token { K = Tk.New, Line = line });
            }
            toks.Add(new Token { K = Tk.Eof, Line = line });
            return toks;
        }
    }
}
