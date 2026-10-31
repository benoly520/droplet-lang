// 本文件是 explore 那一半：同一批意图，多个候选各跑一遍，点名分歧（§7.8.2）。
//
// 它与取值 / 变异共用同一个求值器（Run），也共用同一条纪律：工具层，产物不发布、
// 不进构建图。两段：① 比**已声明**的封闭 example（规范形对齐）；
// ② 比**没声明过的**输入 —— 按签名造边界值，见 probe.go。第二段只覆盖 u64 / i64 / bool，
// 且只比标量返回，缺口记在 docs/D0-DEVIATIONS.md「候选收敛」。
package verify

import (
	"droplet/internal/ast"
	"droplet/internal/check"
)

// Source 是一个候选：已经 parse + check 过的程序。
type Source struct {
	Name string
	Prog *ast.Program
	Res  *check.Result
}

// Cand 是一个候选的汇总。样例按规范形索引 —— 两份候选写了同一条 example，规范形
// 就相同，这正是"同一个意图"的判定依据。结论只有三种：PASS / FAIL / skip: 原因。
type Cand struct {
	Name  string
	Cases map[string]string
	Ran   int
	Err   error
}

// Report 是 explore 的结论。
type Report struct {
	Cands     []Cand
	Labels    []string            // 全部样例，按首次出现顺序
	OnlyIn    map[string][]string // 样例 → 缺这条样例的候选（意图集不一致）
	Divergent []string            // 候选之间结论不一致的样例
	Alive     []string            // 全部已声明样例都成立的候选

	Probes    int         // 自动生成的输入条数（0 = 没造：全是 str/bytes 参数或存活者不足）
	ProbeDiff []ProbeDiff // 自动输入上出现分歧的那几条，按生成顺序
}

// ProbeDiff 是一条自动生成的输入上出现的分歧。
type ProbeDiff struct {
	Label string            // 规范形，例如 `add(0, 1)`
	Vals  map[string]string // 候选名 → 该输入上的结果（打印顺序用 r.Cands）
}

// Explore 逐个候选跑同一批样例并归并。单个候选跑不起来只影响它自己。
func Explore(srcs []Source) *Report {
	r := &Report{OnlyIn: map[string][]string{}}
	for _, s := range srcs {
		c := Cand{Name: s.Name, Cases: map[string]string{}}
		out, err := Run(s.Prog, s.Res, Options{})
		if err != nil {
			c.Err = err
			r.Cands = append(r.Cands, c)
			continue
		}
		c.Ran = out.Ran
		for _, cs := range out.Cases {
			switch {
			case cs.Skip != "":
				c.Cases[cs.Src] = "skip: " + cs.Skip
			case !cs.OK:
				c.Cases[cs.Src] = "FAIL"
			default:
				c.Cases[cs.Src] = "PASS"
			}
			if _, ok := r.OnlyIn[cs.Src]; !ok {
				r.OnlyIn[cs.Src] = nil
				r.Labels = append(r.Labels, cs.Src)
			}
		}
		r.Cands = append(r.Cands, c)
	}

	for _, l := range r.Labels {
		var kinds, missing []string
		for _, c := range r.Cands {
			if v, ok := c.Cases[l]; ok {
				kinds = append(kinds, v)
			} else {
				missing = append(missing, c.Name)
			}
		}
		if len(missing) == 0 {
			delete(r.OnlyIn, l)
		} else {
			r.OnlyIn[l] = missing
		}
		for _, k := range kinds {
			if k != kinds[0] {
				r.Divergent = append(r.Divergent, l)
				break
			}
		}
	}

	for i := range r.Cands {
		c := &r.Cands[i]
		if c.Err != nil || c.Ran == 0 {
			continue // 跑不起来或零可执行样例：不算存活，也不算被意图否掉
		}
		alive := true
		for _, v := range c.Cases {
			alive = alive && v != "FAIL"
		}
		if alive {
			r.Alive = append(r.Alive, c.Name)
		}
	}
	r.probe(srcs)
	return r
}

// probe 在**还活着的候选**之间比自动生成的输入。
//
// 只在存活者之间比：已经被样例否掉的候选不再参与，否则报出来的分歧里一半是
// "实现本来就是错的"。少于两个存活者时没有可比对象，直接跳过。
func (r *Report) probe(srcs []Source) {
	if len(r.Alive) < 2 {
		return
	}
	var alive []Source
	for _, s := range srcs {
		for _, name := range r.Alive {
			if s.Name == name {
				alive = append(alive, s)
			}
		}
	}
	probes := ProbeSet(alive)
	if len(probes) == 0 {
		return
	}

	vals := make([][]string, len(alive))
	for i, s := range alive {
		out, err := RunProbes(s.Prog, s.Res, probes)
		if err != nil {
			return // 造不出可跑的编译单元（能力调用、联合返回等）：这一半就静默不做
		}
		vals[i] = out
	}
	r.Probes = len(probes)

	for p := range probes {
		d := ProbeDiff{Label: probes[p].Label, Vals: map[string]string{}}
		same := true
		for i := range alive {
			d.Vals[alive[i].Name] = vals[i][p]
			same = same && vals[i][p] == vals[0][p]
		}
		if !same {
			r.ProbeDiff = append(r.ProbeDiff, d)
		}
	}
}
