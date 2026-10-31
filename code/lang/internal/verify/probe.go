// 这一半是 §7.8.2 缺的那块：**源码没声明过的输入**。前两段（example、样例对齐）
// 比的都是人写下来的东西，回答不了"在没声明的输入上有没有分歧"。
// 这里按签名造值：底座全取池首，再逐个参数换成池里的其余值（一次只动一个）。
//
// 三条边界：① 只造 u64 / i64 / bool 的值 —— str / bytes 要跨候选一致地序列化，
// 比这一批本身还长；② 只比标量返回（投影成 u64，i64 取位模式，比相等不比大小）；
// ③ 一条输入一个进程 —— 造出来的值可能让实现崩掉，隔离让崩溃只丢这一条。
package verify

import (
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"droplet/internal/ast"
	"droplet/internal/check"
	"droplet/internal/gen"
	"droplet/internal/print"
)

// Probe 是一条自动生成的输入。
type Probe struct {
	Label string   // 规范形，例如 `add(0, 1)`
	Call  ast.Expr // 合成出来的调用节点（实参是字面量）
}

// pools 是各类型的取值池。顺序固定 —— 多个候选必须拿到同序的输入集，
// 否则"第 i 条"对不上。池首是底座（"1" / true）：底座全零会让 `a / b` 这类
// 实现永远只看到除零，别的组合一次也测不到。8/16/255/256 是边界两侧：
// 分支删除与边界翻转正是在这些位置翻面。
var pools = map[check.Kind][]string{
	check.KU64:  {"1", "0", "2", "3", "8", "9", "16", "255", "256", "4294967296", "18446744073709551615"},
	check.KI64:  {"1", "0", "2", "3", "8", "9", "127", "128", "2147483648", "9223372036854775807"},
	check.KBool: {"true", "false"},
}

// ProbeSet 取候选集共有的函数（同名、参数与返回类型全同），按返回类型筛出可投影的，
// 再按池生成输入。顺序只由函数名与池决定，所以每个候选拿到的输入集完全一致。
func ProbeSet(cands []Source) []Probe {
	if len(cands) < 2 {
		return nil
	}
	names := make([]string, 0, len(cands[0].Res.Sigs))
	for n := range cands[0].Res.Sigs {
		names = append(names, n)
	}
	sort.Strings(names)

	var out []Probe
	for _, name := range names {
		if sig := sharedSig(cands, name); sig != nil {
			out = append(out, probesFor(sig)...)
		}
	}
	return out
}

// sharedSig 返回所有候选都声明了的那份签名；参数或返回类型有任何不同就算不同函数。
// 顺带做完两道筛：返回值能投影成 u64（u64 / i64 / bool），实参造得出、且不超过 3 个。
func sharedSig(cands []Source, name string) *check.FnSig {
	var sig *check.FnSig
	for _, c := range cands {
		s := c.Res.Sigs[name]
		if s == nil || s.IsEntry || s.Host != "" {
			return nil
		}
		if sig == nil {
			sig = s
			continue
		}
		if len(sig.Params) != len(s.Params) || sig.Ret.Kind != s.Ret.Kind {
			return nil
		}
		for i := range s.Params {
			if sig.Params[i].Kind != s.Params[i].Kind {
				return nil
			}
		}
	}
	if _, ok := pools[sig.Ret.Kind]; !ok || len(sig.Params) > 3 {
		return nil
	}
	for _, p := range sig.Params {
		if _, ok := pools[p.Kind]; !ok {
			return nil
		}
	}
	return sig
}

// probesFor 生成输入集：底座 + 每个参数最多 8 个替换值。
func probesFor(sig *check.FnSig) []Probe {
	mk := func(vals []string) Probe {
		args := make([]ast.Expr, len(vals))
		for i, v := range vals {
			if sig.Params[i].Kind == check.KBool {
				args[i] = &ast.BoolLit{Value: v == "true"}
			} else {
				args[i] = &ast.IntLit{Value: v}
			}
		}
		c := &ast.Call{Callee: &ast.Ident{Name: sig.Name}, Args: args}
		return Probe{Label: print.Expr(c), Call: c}
	}

	base := make([]string, len(sig.Params))
	for i, p := range sig.Params {
		base[i] = pools[p.Kind][0]
	}
	out := []Probe{mk(base)}
	for i, p := range sig.Params {
		pool := pools[p.Kind]
		if len(pool) > 9 {
			pool = pool[:9]
		}
		for _, v := range pool {
			if v == base[i] {
				continue
			}
			vals := append(append([]string{}, base[:i]...), v)
			vals = append(vals, base[i+1:]...)
			out = append(out, mk(vals))
		}
	}
	return out
}

// RunProbes 逐条求值，返回与 probes 同序的文本结果（"崩溃" / "超时" 也是结果 ——
// 崩溃本身可能就是候选之间的分歧，不能当成没跑）。
func RunProbes(prog *ast.Program, res *check.Result, probes []Probe) ([]string, error) {
	calls := make([]ast.Expr, len(probes))
	for i := range probes {
		calls[i] = probes[i].Call
	}
	src, err := gen.CProbe(prog, res, calls)
	if err != nil {
		return nil, err
	}
	// 探针用 -O0：参数是字面量，-O2 会把函数体折进 switch 再按 UB 优化掉
	// （除零可以被折成任意值而不 trap），崩溃就永远看不见。
	bin, cleanup, err := buildUnit(src, probeDriver, "probe_main", "-O0")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	out := make([]string, len(probes))
	for i := range probes {
		out[i] = runProbe(bin, i)
	}
	return out, nil
}

const probeDriver = `/* 由 droplet explore 生成：求一条输入的值，进程隔离由外部负责 */
#include "rt.h"
#include <stdio.h>
#include <stdlib.h>
uint64_t dp_probe(int i, dp_arena* a);
int main(int argc, char** argv) {
  dp_arena* a = dp_arena_new(1u << 20);
  if (argc < 2 || a == NULL) { return 2; }
  printf("%llu\n", (unsigned long long)dp_probe(atoi(argv[1]), a));
  dp_arena_free(a);
  return 0;
}
`

// runProbe 跑一条输入。最后一行才是值：实现自己可能往 stdout 打日志。
func runProbe(bin string, i int) string {
	out, err := exec.Command(bin, strconv.Itoa(i)).Output()
	if err != nil {
		return "崩溃"
	}
	if f := strings.Fields(string(out)); len(f) > 0 {
		return f[len(f)-1]
	}
	return "无输出"
}
