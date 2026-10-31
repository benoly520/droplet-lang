// 本文件实现 §7.8.1 的机械变异里那两个**纯语法**的类别：常量替换与边界翻转。
//
// 它们不需要理解程序在干什么：改一处字面量、翻一个比较符，重新生成、重新跑同一批
// 样例。如果有样例翻面，说明这处改动被意图抓住了；全都没翻面，就是"意图在这处没有
// 牙齿"的证据 —— 这正是 E10 想要的事实，只不过由工具报出来，而不是让构建失败。
//
// 另外四类（分支删除、效果注入、写入删除、提前返回）要改控制流与调用点，
// 体积超出 H8 余量，等预算复议后再接（见 docs/NEXT.md）。
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

// collect 走一遍所有函数体，收集可机械改动的点。
// 只改实现（函数体），不改意图 —— 意图是 oracle，改它等于改考卷。
func collect(prog *ast.Program) []mutation {
	var out []mutation
	visit := func(e ast.Expr) {
		switch n := e.(type) {
		case *ast.IntLit:
			v, err := strconv.ParseUint(n.Value, 10, 64)
			if err != nil || v == ^uint64(0) {
				return // 读不懂或会溢出：这处变异构造不出来
			}
			old := n.Value
			out = append(out, mutation{
				pos:     n.Pos,
				desc:    fmt.Sprintf("常量 %s → %d", old, v+1),
				apply:   func() { n.Value = strconv.FormatUint(v+1, 10) },
				restore: func() { n.Value = old },
			})
		case *ast.Binary:
			flip, ok := boundaryFlip[n.Op]
			if !ok {
				return
			}
			old := n.Op
			out = append(out, mutation{
				pos:     n.Pos,
				desc:    "边界 " + old + " → " + flip,
				apply:   func() { n.Op = flip },
				restore: func() { n.Op = old },
			})
		}
	}
	for _, it := range prog.Items {
		fn, ok := it.(*ast.FnDecl)
		if !ok || fn.Body == nil {
			continue
		}
		walkBlock(fn.Body, visit)
	}
	return out
}

// ---------- 遍历 ----------

func walkBlock(b *ast.Block, visit func(ast.Expr)) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		walkStmt(s, visit)
	}
}

func walkStmt(s ast.Stmt, visit func(ast.Expr)) {
	switch n := s.(type) {
	case *ast.Let:
		walkExpr(n.Value, visit)
	case *ast.Return:
		walkExpr(n.Value, visit)
	case *ast.ExprStmt:
		walkExpr(n.X, visit)
	case *ast.If:
		walkExpr(n.Cond, visit)
		walkBlock(n.Then, visit)
		switch e := n.Else.(type) {
		case *ast.Block:
			walkBlock(e, visit)
		case ast.Stmt:
			walkStmt(e, visit)
		}
	case *ast.Match:
		walkExpr(n.Subject, visit)
		for _, a := range n.Arms {
			switch b := a.Body.(type) {
			case *ast.Block:
				walkBlock(b, visit)
			case ast.Expr:
				walkExpr(b, visit)
			}
		}
	}
}

func walkExpr(e ast.Expr, visit func(ast.Expr)) {
	if e == nil {
		return
	}
	visit(e)
	switch n := e.(type) {
	case *ast.Call:
		walkExpr(n.Callee, visit)
		for _, a := range n.Args {
			walkExpr(a, visit)
		}
	case *ast.Binary:
		walkExpr(n.Left, visit)
		walkExpr(n.Right, visit)
	case *ast.Select:
		walkExpr(n.Target, visit)
	case *ast.Construct:
		for _, f := range n.Fields {
			walkExpr(f.Value, visit)
		}
	case *ast.Propagate:
		walkExpr(n.Inner, visit)
	}
}
