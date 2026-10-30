package main

import (
	"strings"
	"testing"
)

func TestSplitBuildArgs(t *testing.T) {
	o, err := splitBuildArgs([]string{"-o", "out/blobd", "-print-c", "a.dlt", "b.dlt"})
	if err != nil {
		t.Fatalf("正常参数报错：%v", err)
	}
	if o.out != "out/blobd" {
		t.Errorf("out 为 %q，期望 out/blobd", o.out)
	}
	if !o.printC || o.keepC {
		t.Errorf("标志位不对：printC=%v keepC=%v", o.printC, o.keepC)
	}
	if strings.Join(o.files, ",") != "a.dlt,b.dlt" {
		t.Errorf("文件顺序为 %v，期望保持命令行顺序", o.files)
	}
}

func TestSplitBuildArgsMissingOut(t *testing.T) {
	// -o 缺值不能被静默忽略：否则产物落在 a.out，人却以为在别处。
	for _, args := range [][]string{
		{"build", "a.dlt", "-o"},
		{"a.dlt", "-o", "-print-c"},
	} {
		if _, err := splitBuildArgs(args); err == nil {
			t.Errorf("%v 应报错", args)
		}
	}
}
