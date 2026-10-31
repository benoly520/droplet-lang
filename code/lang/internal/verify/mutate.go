// 本文件实现 §7.8.1 的机械变异里的四类，分两条轴：
//
//	表达式轴：常量替换、边界翻转 —— 改一个值、翻一个比较符。
//	语句轴：分支删除（条件换成 false）、提前返回（删掉守卫块末尾的 return）。
//
// 它们不需要理解程序在干什么：改一处，重新生成、重新跑同一批样例。如果有样例翻面，
// 说明这处改动被意图抓住了；全都没翻面，就是"意图在这处没有牙齿"的证据 —— 这正是
// E10 想要的事实，只不过由工具报出来，而不是让构建失败。
//
// 另两类（效果注入、写入删除）**不做**：它们要看到能力调用与写入真的发生了，而
// droplet verify 没有那条观测通道（服务形态、能力调用、开放样例一律不跑，见 verify.go）。
// 要做它们等于把这工具变成服务运行器 —— 那不是多一类变异，是另一个工具。
package verify

import (
	"fmt"
	"strconv"

	"droplet/internal/ast"
	"droplet/internal/check"
	"droplet/internal/gen"
	"droplet/internal/token"
)

// Mutant 是一处被改动的实现，以及它有没有被样例抓住。
type Mutant struct {
	Pos      token.Pos
	Desc     string // 人话描述，如「常量 2 → 3」「边界 < → <=」
	CaughtBy string // 抓住它的样例；空 = 没有样例抓住
}

// mutation 是"改一处"的记录：apply 改、restore 还原。
type mutation struct {
	pos     token.Pos
	desc    string
	apply   func()
	restore func()
}

// boundaryFlip 是边界翻转的四种对应关系。
var boundaryFlip = map[string]string{"<": "<=", "<=": "<", ">": ">=", ">=": ">"}

// mutate 逐条应用变异、重跑样例，返回每条变异是否被抓住。
//
// base 是基线结果（第 i 条例样是否成立），srcs 是样例的规范形（用来在报告里点名）。
func mutate(prog *ast.Program, res *check.Result, exprs []ast.Expr, srcs []string, base map[int]bool) ([]Mutant, error) {
	ms := collect(prog)
	out := make([]Mutant, 0, len(ms))
	for _, m := range ms {
		m.apply()
		src, err := gen.CVerify(prog, res, exprs)
		if err != nil {
			m.restore()
			return nil, fmt.Errorf("%s 的变异生不出 C：%w", m.desc, err)
		}
		got, err := compileAndRun(src)
		m.restore()
		if err != nil {
			// 变异把程序改成编不过：那是编译器抓住的，不该算作意图的功劳，
			// 但也确实不是"存活"——单独记一笔，不掩盖。
			out = append(out, Mutant{Pos: m.pos, Desc: m.desc, CaughtBy: "（编译期）"})
			continue
		}
		caught := ""
		for i := range exprs {
			if got[i] != base[i] {
				caught = srcs[i]
				break
			}
		}
		out = append(out, Mutant{Pos: m.pos, Desc: m.desc, CaughtBy: caught})
	}
	return out, nil
}

// collector 走一遍所有函数体，收集可机械改动的点。
// 只改实现（函数体），不改意图 —— 意图是 oracle，改它等于改考卷。
type collector struct{ out []mutation }

func collect(prog *ast.Program) []mutation {
	c := &collector{}
	for _, it := range prog.Items {
		if fn, ok := it.(*ast.FnDecl); ok && fn.Body != nil {
			c.block(fn.Body)
		}
	}
	return c.out
}

func (c *collector) expr(e ast.Expr) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.IntLit:
		v, err := strconv.ParseUint(n.Value, 10, 64)
		if err != nil || v == ^uint64(0) {
			return // 读不懂或会溢出：这处变异构造不出来
		}
		old := n.Value
		c.add(n.Pos, fmt.Sprintf("常量 %s → %d", old, v+1),
			func() { n.Value = strconv.FormatUint(v+1, 10) },
			func() { n.Value = old })
	case *ast.Binary:
		flip, ok := boundaryFlip[n.Op]
		if !ok {
			return
		}
		old := n.Op
		c.add(n.Pos, "边界 "+old+" → "+flip, func() { n.Op = flip }, func() { n.Op = old })
	case *ast.Call:
		c.expr(n.Callee)
		for _, a := range n.Args {
			c.expr(a)
		}
	case *ast.Select:
		c.expr(n.Target)
	case *ast.Construct:
		for _, f := range n.Fields {
			c.expr(f.Value)
		}
	case *ast.Propagate:
		c.expr(n.Inner)
	}
}

func (c *collector) add(pos token.Pos, desc string, apply, restore func()) {
	c.out = append(c.out, mutation{pos: pos, desc: desc, apply: apply, restore: restore})
}

// ---------- 语句轴 ----------

func (c *collector) block(b *ast.Block) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		c.stmt(s)
	}
}

func (c *collector) stmt(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.Let:
		c.expr(n.Value)
	case *ast.Return:
		c.expr(n.Value)
	case *ast.ExprStmt:
		c.expr(n.X)
	case *ast.If:
		c.expr(n.Cond)
		// 分支删除：清空 then —— 条件还在（参数因此仍被用到），那个分支里的事没了。
		// 换成一个 false 字面量更"语义化"，但会让只在分支里用到的参数变成未使用，
		// cc 的 -Wunused-parameter 会把它记成「（编译期）」，那是工具自己的噪声。
		old := n.Then.Stmts
		c.add(n.Pos, "分支删除 清空 then", func() { n.Then.Stmts = nil }, func() { n.Then.Stmts = old })
		c.guard(n.Then)
		switch e := n.Else.(type) {
		case *ast.Block:
			c.guard(e)
		case ast.Stmt:
			c.stmt(e)
		}
	case *ast.Match:
		c.expr(n.Subject)
		for i := range n.Arms {
			switch b := n.Arms[i].Body.(type) {
			case *ast.Block:
				// 分支删除：清空这条 match 分支的处理体（§7.8.1 的原文写法）。
				old := b.Stmts
				c.add(n.Arms[i].Pos, "分支删除 清空 match 分支",
					func() { b.Stmts = nil }, func() { b.Stmts = old })
				c.guard(b)
			case ast.Expr:
				c.expr(b)
			}
		}
	}
}

// guard 处理一个分支体：先收集语句轴的点，再普通地走一遍。
func (c *collector) guard(b *ast.Block) {
	c.earlyReturn(b)
	c.block(b)
}

// earlyReturn 删掉分支体末尾那条 return：守卫不再提前退出，控制流继续往下走。
// 删掉之后编不过的（比如整个 if 的值没了）由 mutate 记「（编译期）」，这里不猜。
func (c *collector) earlyReturn(b *ast.Block) {
	if b == nil || len(b.Stmts) == 0 {
		return
	}
	r, ok := b.Stmts[len(b.Stmts)-1].(*ast.Return)
	if !ok {
		return
	}
	old := b.Stmts
	c.add(r.Pos, "提前返回 删掉 return",
		func() { b.Stmts = old[:len(old)-1] },
		func() { b.Stmts = old })
}
