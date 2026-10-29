// Package runtime 内嵌 C 运行时，供 droplet build 释放到临时目录后编译。
//
// 内嵌的好处：droplet 是一个单文件二进制，构建时不依赖磁盘上是否有 rt.c。
// 源文件放在 assets/ 子目录：Go 不允许包目录里出现 .c 文件（除非启用 cgo）。
package runtime

import (
	"embed"
	"os"
	"path/filepath"
)

//go:embed assets/rt.h assets/rt_core.c assets/rt_http.c assets/rt_blob.c assets/rt_ver.c
var files embed.FS

// Names 是需要释放到构建目录的文件（相对 assets/）。
// 顺序即 cc 的输入顺序，不影响链接结果。
var Names = []string{"rt.h", "rt_core.c", "rt_http.c", "rt_blob.c", "rt_ver.c"}

// WriteTo 把运行时源文件写入 dir。
func WriteTo(dir string) error {
	for _, name := range Names {
		b, err := files.ReadFile("assets/" + name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}
