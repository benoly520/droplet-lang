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
)

const usageText = `droplet - Droplet 语言工具链

用法：
  droplet parse <file.dlt>...            输出合并后的 AST（JSON）
  droplet check <file.dlt>...            类型与错误检查
  droplet build <file.dlt>... -o <out>   生成 C、调用 cc，产出可执行文件
                                         -print-c 只输出 C；-keep-c 保留 <out>.c
  droplet contract <file.dlt>...         导出契约 JSON（boundary / 函数签名 / tag 表）
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
