// Package verify 把 example 当成**可执行的 oracle**，跑在真实实现上。
//
// 它属于工具层（SPEC §7.8.3-4 对 `droplet explore` 的同一套纪律）：
// 产物不参与发布、不进构建图、`droplet build` 不读它。理由有两条 ——
// ① §7.8.1 的变异检验需要独立验证器，把它塞进编译器会破 H8；
// ② "给人看证据"与"能不能上线"必须分开，这是收敛判据 A 的前提。
//
// 这一版只做第一半：把封闭的 example 编进一个专用二进制并逐条求值。
// 变异那一半（改一处、重跑、看样例抓不抓得住）随后接在同一套驱动上。
package verify

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"droplet/internal/ast"
	"droplet/internal/check"
	"droplet/internal/gen"
	"droplet/internal/print"
	rt "droplet/internal/runtime"
	"droplet/internal/token"
)

// Case 是一条 example 的结果。
type Case struct {
	Pos   token.Pos
	Owner string // 所属函数
	Src   string // 规范形（打印器渲染）
	Skip  string // 非空 = 没跑，值是原因
	OK    bool   // 跑了且成立
}

// Result 是一次 verify 的结论。
type Result struct {
	Cases []Case
	Ran   int // 真正跑过的条数
}

// Passed 报告是否全部通过。没有可执行样例不算通过 —— 那等于没证据。
func (r *Result) Passed() bool {
	if r.Ran == 0 {
		return false
	}
	for _, c := range r.Cases {
		if c.Skip == "" && !c.OK {
			return false
		}
	}
	return true
}

// Run 收集程序里的 example、生成验证用编译单元、编译并逐条求值。
// 调用方负责先跑 check 并把错误报出去（这里假定 res.Errors 为空）。
func Run(prog *ast.Program, res *check.Result) (*Result, error) {
	if len(res.Routes) > 0 {
		return nil, fmt.Errorf("服务形态暂不支持 verify：实现函数依赖 env 与 host 能力，样例跑不起来")
	}

	out := &Result{}
	var exprs []ast.Expr
	for _, it := range prog.Items {
		fn, ok := it.(*ast.FnDecl)
		if !ok {
			continue
		}
		for _, in := range fn.Intents {
			if in.Kind != "example" || in.Expr == nil {
				continue
			}
			c := Case{Pos: in.Pos, Owner: fn.Name, Src: print.Expr(in.Expr)}
			if why := runnable(in.Expr, res); why != "" {
				c.Skip = why
				out.Cases = append(out.Cases, c)
				continue
			}
			exprs = append(exprs, in.Expr)
			out.Cases = append(out.Cases, c)
		}
	}
	if len(exprs) == 0 {
		return out, nil
	}

	src, err := gen.CVerify(prog, res, exprs)
	if err != nil {
		return nil, err
	}
	results, err := compileAndRun(src)
	if err != nil {
		return nil, err
	}

	// 按表达式顺序回填结果：被跳过的用例不进二进制，用 ran 游标对齐。
	ran := 0
	for i := range out.Cases {
		if out.Cases[i].Skip != "" {
			continue
		}
		ok, has := results[ran]
		if !has {
			return nil, fmt.Errorf("验证驱动少返回了第 %d 条例样", ran)
		}
		out.Cases[i].OK = ok
		ran++
	}
	out.Ran = ran
	return out, nil
}

// ---------- 可执行性 ----------

// runnable 判断一条样例能不能离线求值。判不了就给出人话原因 ——
// 让"这条样例没有牙齿"变成看得见的账，而不是悄悄跳过。
func runnable(e ast.Expr, res *check.Result) string {
	switch n := e.(type) {
	case nil:
		return "缺少表达式"
	case *ast.IntLit, *ast.StrLit, *ast.BytesLit, *ast.BoolLit, *ast.UnitLit:
		return ""
	case *ast.Ident:
		return "引用了变量 " + n.Name + "（样例必须封闭：只能写常量与调用）"
	case *ast.Propagate:
		return "样例里不能用 `!`（没有可传播的错误返回）"
	case *ast.Binary:
		if why := runnable(n.Left, res); why != "" {
			return why
		}
		return runnable(n.Right, res)
	case *ast.Call:
		id, ok := n.Callee.(*ast.Ident)
		if !ok {
			return "样例只能调用程序里的具名函数（能力调用不可重放）"
		}
		sig := res.Sigs[id.Name]
		if sig == nil {
			return "找不到函数 " + id.Name
		}
		if sig.Ret != nil && sig.Ret.Kind == check.KUnion {
			return "不能调用联合返回的函数 " + id.Name + "（要先拿到值，样例里没有 `!`）"
		}
		for _, a := range n.Args {
			if why := runnable(a, res); why != "" {
				return why
			}
		}
		return ""
	case *ast.Construct:
		for _, f := range n.Fields {
			if why := runnable(f.Value, res); why != "" {
				return why
			}
		}
		return ""
	case *ast.Select:
		// 只允许 `T.V` 这种零字段变体常量：目标必须是类型名。
		id, ok := n.Target.(*ast.Ident)
		if !ok {
			return "字段访问的目标不是类型名（样例必须封闭）"
		}
		if _, isType := res.Types[id.Name]; !isType {
			return "引用了变量 " + id.Name + "（样例必须封闭：只能写常量与调用）"
		}
		if t := res.ExprTypes[n]; t == nil || t.Kind != check.KSum {
			return "暂不支持的变体引用"
		}
		return ""
	}
	return fmt.Sprintf("暂不支持的表达式 %T", e)
}

// ---------- 编译与执行 ----------

const driver = `/* 由 droplet verify 生成：逐条求值 example，结果打到 stdout */
#include "rt.h"
#include <stdio.h>

int dp_verify_count(void);
int dp_verify_expr(int i, dp_arena* a);

int main(void) {
  dp_arena* a = dp_arena_new(1u << 20);
  if (a == NULL) { return 2; }
  int n = dp_verify_count();
  for (int i = 0; i < n; i++) {
    printf("%d %d\n", i, dp_verify_expr(i, a));
  }
  dp_arena_free(a);
  return 0;
}
`

// compileAndRun 把验证单元 + 驱动 + 内嵌运行时编成一个临时二进制并执行，
// 返回"第 i 条例样是否成立"。
func compileAndRun(src string) (map[int]bool, error) {
	dir, err := os.MkdirTemp("", "droplet-verify-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	if err := os.WriteFile(filepath.Join(dir, "gen.c"), []byte(src), 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "verify_main.c"), []byte(driver), 0o644); err != nil {
		return nil, err
	}
	if err := rt.WriteTo(dir); err != nil {
		return nil, err
	}

	bin := filepath.Join(dir, "verify")
	args := []string{"-std=c11", "-O2", "-Wall", "-Wextra", "-Werror", "-o", bin,
		filepath.Join(dir, "gen.c"), filepath.Join(dir, "verify_main.c"), "-I", dir}
	for _, name := range rt.Names {
		if strings.HasSuffix(name, ".c") {
			args = append(args, filepath.Join(dir, name))
		}
	}
	cc := exec.Command("cc", args...)
	if out, err := cc.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("cc 失败：%w\n%s", err, out)
	}

	run := exec.Command(bin)
	out, err := run.Output()
	if err != nil {
		return nil, fmt.Errorf("验证二进制执行失败：%w", err)
	}

	res := map[int]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 {
			return nil, fmt.Errorf("看不懂驱动输出 %q", line)
		}
		i, err := strconv.Atoi(f[0])
		if err != nil {
			return nil, fmt.Errorf("看不懂驱动输出 %q", line)
		}
		res[i] = f[1] == "1"
	}
	return res, nil
}
