// Package check 对语法树做名字解析与类型检查。
//
// 与 parse 的差别：parse 遇到第一个语法错误就停（fail-fast），
// check 收集全部错误（collect-all）并排序输出，避免写代码时改一个错跑一次。
package check

import (
	"droplet/internal/ast"
	"droplet/internal/token"
	"fmt"
	"sort"
)

// ---------- 结果 ----------

// Error 是一条检查错误。
type Error struct {
	Pos  token.Pos
	Code string
	Msg  string
}

func (e Error) Error() string {
	return fmt.Sprintf("%s: %s: %s", e.Pos, e.Code, e.Msg)
}

// Result 是检查结果。Errors 为空表示通过。
//
// ExprTypes / LetTypes / Sigs / Types 是给 codegen 用的副产物：
// 生成阶段不再重新推导类型，避免两套推导漂移。
type Result struct {
	Errors     []Error
	Decls      int
	FnCount    int
	Tags       map[string]int // sum 变体名 → 编号，0 保留给成功值
	EntryPoint string

	ExprTypes map[ast.Expr]*Type
	LetTypes  map[*ast.Let]*Type
	Sigs      map[string]*FnSig
	Types     map[string]*Type
	Hosts     map[string]*Type
	Env       *EnvSpec // 程序的 env 声明；没有就是 nil
}

// ---------- 检查器 ----------

type checker struct {
	prog  *ast.Program
	types map[string]*Type
	hosts map[string]*Type
	fns   map[string]*FnSig

	errs []Error

	scope *scope
	curFn *FnSig

	env *EnvSpec // 已见过的 env 块（D1 只允许一个）

	variantOrder []string          // 变体声明顺序，保证 tag 分配稳定
	variantOwner map[string]string // 变体名 → 首次声明它的 sum，保证 tag 全局唯一
	errorVariant map[string]bool   // 进入过 `!` 传播的错误变体
	seenPos      map[string]bool   // 去重：同一位置的同一错误只报一次

	exprTypes map[ast.Expr]*Type
	letTypes  map[*ast.Let]*Type
}

// ---------- 入口 ----------
// Program 检查一个编译单元。返回值总是非 nil；错误在 Result.Errors 里。
func Program(p *ast.Program) *Result {
	c := &checker{
		prog:         p,
		types:        map[string]*Type{},
		hosts:        map[string]*Type{},
		fns:          map[string]*FnSig{},
		variantOwner: map[string]string{},
		errorVariant: map[string]bool{},
		seenPos:      map[string]bool{},
		exprTypes:    map[ast.Expr]*Type{},
		letTypes:     map[*ast.Let]*Type{},
	}
	c.declare()
	c.fillTypes()
	c.declareFns()
	c.checkBodies()
	c.checkEnvService()

	// 入口只能有一个：c.fns 是 map，多入口时按迭代顺序选 EntryPoint
	// 等于生成哪个 main 由随机数决定 —— 按名字排之后报错，取第一个。
	var entries []string
	for name, sig := range c.fns {
		if sig.IsEntry {
			entries = append(entries, name)
		}
	}
	sort.Strings(entries)
	if len(entries) > 1 {
		c.errorf(c.fns[entries[1]].Decl.Pos, "E1",
			"入口只能有一个：%s 与 %s 都标了 entry，留一个", entries[0], entries[1])
	}

	sort.SliceStable(c.errs, func(i, j int) bool {
		a, b := c.errs[i].Pos, c.errs[j].Pos
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})

	res := &Result{
		Errors:    c.errs,
		Decls:     len(p.Items),
		FnCount:   len(c.fns),
		Tags:      c.assignTags(),
		ExprTypes: c.exprTypes,
		LetTypes:  c.letTypes,
		Sigs:      c.fns,
		Types:     c.types,
		Hosts:     c.hosts,
	}
	if c.env != nil {
		res.Env = c.env
	}
	if len(entries) > 0 {
		res.EntryPoint = entries[0]
	}
	return res
}

// ---------- 辅助 ----------

func (c *checker) errorf(pos token.Pos, code, format string, a ...any) {
	key := fmt.Sprintf("%s|%s|%s", pos, code, fmt.Sprintf(format, a...))
	if c.seenPos[key] {
		return
	}
	c.seenPos[key] = true
	c.errs = append(c.errs, Error{Pos: pos, Code: code, Msg: fmt.Sprintf(format, a...)})
}

// markError 把错误类型里的变体标记为错误变体，供 tag 分配使用。
func (c *checker) markError(t *Type) {
	if t.Kind != KSum {
		return
	}
	for _, v := range t.Variants {
		c.errorVariant[v.Name] = true
	}
}

func (c *checker) assignTags() map[string]int {
	tags := map[string]int{"<ok>": 0}
	next := 100
	for _, name := range c.variantOrder {
		if c.errorVariant[name] {
			if _, ok := tags[name]; !ok {
				tags[name] = next
				next++
			}
		}
	}
	next = 1
	for _, name := range c.variantOrder {
		if _, ok := tags[name]; ok {
			continue
		}
		tags[name] = next
		next++
	}
	return tags
}
