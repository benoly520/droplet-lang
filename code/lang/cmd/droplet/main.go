// Command droplet 是 Droplet 语言的工具链入口。
//
// M0：parse；M1：check；M2：build；M3：contract。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"droplet/internal/ast"
	"droplet/internal/check"
	"droplet/internal/contract"
	"droplet/internal/dump"
	"droplet/internal/gen"
	"droplet/internal/parse"
	rt "droplet/internal/runtime"
	"droplet/internal/verify"
)

const usageText = `droplet - Droplet 语言工具链

用法：
  droplet parse <file.dlt>...            输出合并后的 AST（JSON）
  droplet check <file.dlt>...            类型与错误检查
  droplet build <file.dlt>... -o <out>   生成 C、调用 cc，产出可执行文件
                                         -print-c 只输出 C；-keep-c 保留 <out>.c
  droplet contract <file.dlt>...         导出契约 JSON（boundary / 函数签名 / tag 表 / env 配置）
  droplet verify <file.dlt>...           把 example 当可执行 oracle 跑一遍（工具层，产物不可发布）
  droplet explore <a.dlt> <b.dlt>...      多个候选跑同一批 example，点名分歧（工具层，§7.8.2）
  droplet help                           显示本帮助
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}

	switch args[0] {
	case "parse":
		cmdParse(args[1:])
	case "check":
		cmdCheck(args[1:])
	case "build":
		cmdBuild(args[1:])
	case "contract":
		cmdContract(args[1:])
	case "verify":
		cmdVerify(args[1:])
	case "explore":
		cmdExplore(args[1:])
	case "help", "-h", "--help":
		fmt.Print(usageText)
	default:
		fmt.Fprintf(os.Stderr, "droplet: 未知命令 %q\n\n%s", args[0], usageText)
		os.Exit(2)
	}
}

func cmdParse(files []string) {
	needFiles("parse", files)
	prog := loadProgram(files)

	out, err := encodeJSON(dump.Program(prog))
	if err != nil {
		fail(err)
	}
	fmt.Print(out)
}

func cmdCheck(files []string) {
	needFiles("check", files)
	prog := loadProgram(files)
	res := check.Program(prog)
	reportCheck(res)

	fmt.Printf("ok: %d 项声明，%d 个函数", res.Decls, res.FnCount)
	if res.EntryPoint != "" {
		fmt.Printf("，入口 %s", res.EntryPoint)
	}
	fmt.Println()
}

// cmdContract 输出契约 JSON。它要求检查通过：拿有错的程序导出契约没有意义。
func cmdContract(files []string) {
	needFiles("contract", files)
	prog := loadProgram(files)
	res := check.Program(prog)
	reportCheck(res)

	out, err := encodeJSON(contract.Build(prog, res))
	if err != nil {
		fail(err)
	}
	fmt.Print(out)
}

// cmdVerify 把 example 当可执行 oracle 跑一遍（工具层，见 internal/verify）。
//
// 退出码：0 = 全部可执行样例成立；1 = 有样例不成立，或一条可执行的都没有。
// "没有可执行样例"按失败处理 —— 那意味着意图没有任何牙齿（§7.8.1）。
// cmdExplore 是 §7.8.2 的最小形态：只比**已声明**的 example，不生成输入。
// 退出码：0 唯一存活；1 零存活或欠定（§7.8.3 A：不自动挑一个）；2 用法错。
func cmdExplore(files []string) {
	if len(files) < 2 {
		fmt.Fprintln(os.Stderr, "droplet: explore 至少要两个候选（单个候选用 verify）")
		os.Exit(2)
	}
	var srcs []verify.Source
	for _, f := range files {
		prog := loadProgram([]string{f})
		res := check.Program(prog)
		reportCheck(res)
		srcs = append(srcs, verify.Source{Name: f, Prog: prog, Res: res})
	}

	rep := verify.Explore(srcs)
	for i := range rep.Cands {
		c := &rep.Cands[i]
		if c.Err != nil {
			fmt.Printf("  %s  跑不起来 — %v\n", c.Name, c.Err)
			continue
		}
		fmt.Printf("  %s  %d 条可执行 / %d 条声明\n", c.Name, c.Ran, len(c.Cases))
	}
	if len(rep.Divergent) > 0 {
		fmt.Println("分歧（同一条样例，候选之间结论不一致）：")
		for _, l := range rep.Divergent {
			fmt.Printf("  %s\n", l)
			for i := range rep.Cands {
				if v, ok := rep.Cands[i].Cases[l]; ok {
					fmt.Printf("    %s  %s\n", rep.Cands[i].Name, v)
				}
			}
		}
	}
	if len(rep.OnlyIn) > 0 {
		fmt.Println("意图集不一致（下面这些样例只在部分候选里声明）：")
		for _, l := range rep.Labels {
			if who := rep.OnlyIn[l]; len(who) > 0 {
				fmt.Printf("  %s  缺：%v\n", l, who)
			}
		}
	}

	switch len(rep.Alive) {
	case 1:
		fmt.Printf("存活：%s —— 意图把候选集收敛到唯一一个\n", rep.Alive[0])
		return
	case 0:
		fmt.Println("存活：无")
		for i := range rep.Cands {
			for _, l := range rep.Labels {
				if rep.Cands[i].Cases[l] == "FAIL" {
					fmt.Printf("  %s 被 %s 否掉\n", rep.Cands[i].Name, l)
				}
			}
		}
		fmt.Fprintln(os.Stderr, "droplet: 没有候选同时满足全部意图 —— 要么实现坏了，要么意图互相矛盾")
	default:
		fmt.Printf("存活：%v —— 欠定（§7.8.3 A：不自动挑一个）\n", rep.Alive)
		fmt.Fprintln(os.Stderr, "droplet: 还有多个候选存活 —— 补一条 example / invariant 让它们分开")
	}
	os.Exit(1)
}

func cmdVerify(files []string) {
	needFiles("verify", files)
	prog := loadProgram(files)
	res := check.Program(prog)
	reportCheck(res)

	out, err := verify.Run(prog, res, verify.Options{Mutate: true})
	if err != nil {
		fail(err)
	}
	for _, c := range out.Cases {
		switch {
		case c.Skip != "":
			fmt.Printf("  skip  %s   %s — %s\n", c.Src, c.Pos, c.Skip)
		case c.OK:
			fmt.Printf("  PASS  %s   %s\n", c.Src, c.Pos)
		default:
			fmt.Printf("  FAIL  %s   %s（%s 的样例不成立：实现改变了这条样例的期望）\n", c.Src, c.Pos, c.Owner)
		}
	}
	fmt.Printf("verify: %d 条样例，%d 条可执行\n", len(out.Cases), out.Ran)
	if out.Ran == 0 {
		fmt.Fprintln(os.Stderr, "droplet: 没有可执行的样例 —— 意图没有牙齿（§7.8.1）；样例要写成封闭表达式，例如 `example f(3) == 9`")
	}

	// 变异报告：存活下来的那几处就是"意图在这里没有牙齿"的证据（§7.8.1）。
	miss := 0
	for _, m := range out.Mutants {
		if m.CaughtBy == "" {
			miss++
			fmt.Printf("  MISS  %s   %s — 没有样例抓住这处改动\n", m.Desc, m.Pos)
			continue
		}
		fmt.Printf("  CAUGHT  %s   %s — %s\n", m.Desc, m.Pos, m.CaughtBy)
	}
	if len(out.Mutants) > 0 {
		fmt.Printf("mutate: %d 处变异，%d 处没被抓住\n", len(out.Mutants), miss)
	}
	if miss > 0 {
		fmt.Fprintln(os.Stderr, "droplet: 有变异存活 —— 意图在那些位置没有约束力（§7.8.1 的 E10 事实，由工具报出而不是让构建失败）")
	}

	if !out.Passed() || miss > 0 {
		os.Exit(1)
	}
}

// cmdBuild 生成 C 源码，释放内嵌运行时，再调用系统 cc 产出可执行文件。
func cmdBuild(args []string) {
	opts, err := splitBuildArgs(args)
	if err != nil {
		fail(err)
	}
	out := opts.out
	if out == "" {
		out = "a.out"
	}
	needFiles("build", opts.files)

	prog := loadProgram(opts.files)
	res := check.Program(prog)
	reportCheck(res)

	src, err := gen.C(prog, res)
	if err != nil {
		fail(err)
	}
	if opts.printC {
		fmt.Print(src)
		return
	}
	if opts.keepC {
		if err := os.WriteFile(out+".c", []byte(src), 0o644); err != nil {
			fail(err)
		}
	}

	dir, err := os.MkdirTemp("", "droplet-build-*")
	if err != nil {
		fail(err)
	}
	defer os.RemoveAll(dir)

	if err := os.WriteFile(filepath.Join(dir, "gen.c"), []byte(src), 0o644); err != nil {
		fail(err)
	}
	if err := rt.WriteTo(dir); err != nil {
		fail(err)
	}

	ccArgs := []string{"-std=c11", "-O2", "-Wall", "-Wextra", "-o", out,
		filepath.Join(dir, "gen.c"), "-I", dir}
	for _, name := range rt.Names {
		if strings.HasSuffix(name, ".c") {
			ccArgs = append(ccArgs, filepath.Join(dir, name))
		}
	}
	cc := exec.Command("cc", ccArgs...)
	cc.Stdout, cc.Stderr = os.Stdout, os.Stderr
	if err := cc.Run(); err != nil {
		fail(fmt.Errorf("cc 失败：%w", err))
	}

	fmt.Printf("ok: %s\n", out)
}

type buildOpts struct {
	files  []string
	out    string
	printC bool
	keepC  bool
}

// splitBuildArgs 拆出 -o 与其值，其余视为输入文件。
//
// 缺值的 -o 直接报错：静默回退到 a.out 会让人以为产物在指定的位置上。
func splitBuildArgs(args []string) (buildOpts, error) {
	var o buildOpts
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-o":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return o, fmt.Errorf("-o 需要一个输出路径")
			}
			o.out = args[i+1]
			i++
		case "-print-c":
			o.printC = true
		case "-keep-c":
			o.keepC = true
		default:
			o.files = append(o.files, args[i])
		}
	}
	return o, nil
}

// reportCheck 打印全部检查错误并以非零码退出。
func reportCheck(res *check.Result) {
	if len(res.Errors) == 0 {
		return
	}
	for _, e := range res.Errors {
		fmt.Fprintln(os.Stderr, e.Error())
	}
	fmt.Fprintf(os.Stderr, "droplet: %d 个错误\n", len(res.Errors))
	os.Exit(1)
}

func needFiles(cmd string, files []string) {
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "droplet: %s 需要至少一个 .dlt 文件\n", cmd)
		os.Exit(2)
	}
}

// loadProgram 把命令行给出的全部文件按顺序合并为一个编译单元。
func loadProgram(files []string) *ast.Program {
	prog := &ast.Program{}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			fail(err)
		}
		p, err := parse.File(f, string(src))
		if err != nil {
			fail(err)
		}
		prog.Items = append(prog.Items, p.Items...)
	}
	return prog
}

// encodeJSON 以两空格缩进输出，且不做 HTML 转义（避免 < > & 变成 \u003c）。
func encodeJSON(v any) (string, error) {
	var sb strings.Builder
	enc := json.NewEncoder(&sb)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return sb.String(), nil
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "droplet: %v\n", err)
	os.Exit(1)
}
