// Package host 是 D0 的 host 能力表：每个「组.能力」在这里登记参数个数与落到运行时的
// 调用形状。
//
// 为什么单独成包：checker 需要知道「声明的能力运行时到底有没有」，生成器需要知道
// 「这个能力怎么调」。两处各存一份就会出现 check 说 ok、build 报错的对齐问题
// （见 docs/D0-DEVIATIONS.md §4）。依赖方向只能是 check / gen → host。
package host

// Cap 是一项能力：参数个数 + 生成 C 调用的方式。
// errv 非空表示调用方要接错误码，Emit 负责把它作为输出参数写进去。
type Cap struct {
	Arity int
	Emit  func(argv []string, errv string) string
}

// Caps 是全部能力的唯一清单。新增一项 = 这里加一条 + rt.h 加对应原型。
// 目前 10 项：Sys 4、BlobFs 5、Versioning 1，正好是 webapi 示例用到的全部。
var Caps = map[string]Cap{
	"Sys.log": {1, func(a []string, _ string) string {
		return "dp_sys_log(" + a[0] + ")"
	}},
	"Sys.hash64": {1, func(a []string, _ string) string {
		return "dp_sys_hash64(" + a[0] + ")"
	}},
	"Sys.u64_str": {1, func(a []string, _ string) string {
		return "dp_sys_u64_str(a, " + a[0] + ")"
	}},
	"Sys.now_unix": {0, func(_ []string, _ string) string {
		return "dp_sys_now_unix()"
	}},
	"BlobFs.read": {2, func(a []string, e string) string {
		return "dp_blob_read(a, " + a[0] + ", " + a[1] + ", &" + e + ")"
	}},
	"BlobFs.write": {3, func(a []string, e string) string {
		return "dp_blob_write(a, " + a[0] + ", " + a[1] + ", " + a[2] + ", &" + e + ")"
	}},
	"BlobFs.backup": {2, func(a []string, _ string) string {
		return "dp_blob_backup(a, " + a[0] + ", " + a[1] + ")"
	}},
	"BlobFs.create": {2, func(a []string, e string) string {
		return "dp_blob_create(a, " + a[0] + ", " + a[1] + ", &" + e + ")"
	}},
	"BlobFs.list_json": {1, func(a []string, e string) string {
		return "dp_blob_list(a, " + a[0] + ", &" + e + ")"
	}},
	"Versioning.commit_soon": {2, func(a []string, _ string) string {
		return "dp_ver_commit_soon(" + a[0] + ", " + a[1] + ")"
	}},
}

// Lookup 查一项能力；第二返回值表示运行时是否真的提供它。
func Lookup(name string) (Cap, bool) {
	c, ok := Caps[name]
	return c, ok
}
