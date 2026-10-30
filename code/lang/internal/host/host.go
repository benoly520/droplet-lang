// Package host 是 D0 的 host 能力表：每个「组.能力」在这里登记参数与返回的
// 运行时类型，以及落到 C 的调用形状。
//
// 为什么单独成包：checker 需要知道「声明的能力运行时到底有没有、签名对不对」，
// 生成器需要知道「这个能力怎么调」。两处各存一份就会出现 check 说 ok、build
// 报错的对齐问题（见 docs/D0-DEVIATIONS.md §4）。依赖方向只能是 check / gen → host。
package host

// Ty 是能力参数与返回值的运行时 ABI 类型。语言层的 str / bytes / u64 / i64 /
// bool / Unit 与它们一一对应；record 类返回值靠 Cap.RetName + Cap.Wrap 包出来。
type Ty uint8

const (
	Str   Ty = iota // dp_str
	Bytes           // dp_bytes
	U64             // uint64_t
	I64             // int64_t
	Bool            // bool
	Unit            // void
)

// String 是语言侧的类型名：报错信息与 checker 的比对都用它。
func (t Ty) String() string {
	switch t {
	case Str:
		return "str"
	case Bytes:
		return "bytes"
	case U64:
		return "u64"
	case I64:
		return "i64"
	case Bool:
		return "bool"
	}
	return "Unit"
}

// Cap 是一项能力。
//
//   - Params / Ret 是运行时原型里的类型，checker 拿 .dlt 里的 host 声明逐个比对；
//   - RetName 是语言侧成功类型的名字，空表示与 Ret.String() 相同；
//   - Wrap 把运行时返回值包成语言类型（如 u64 → ETag），nil 表示直接赋值；
//   - Emit 生成 C 调用；errv 非空表示调用方要接错误码，由它作为输出参数写入。
type Cap struct {
	Params  []Ty
	Ret     Ty
	RetName string
	Wrap    func(expr string) string
	Emit    func(argv []string, errv string) string
}

// Arity 是参数个数。
func (c Cap) Arity() int { return len(c.Params) }

// Caps 是全部能力的唯一清单。新增一项 = 这里加一条 + rt.h 加对应原型。
// 目前 10 项：Sys 4、BlobFs 5、Versioning 1，正好是 webapi 示例用到的全部。
var Caps = map[string]Cap{
	"Sys.log": {
		Params: []Ty{Str},
		Ret:    Unit,
		Emit: func(a []string, _ string) string {
			return "dp_sys_log(" + a[0] + ")"
		},
	},
	"Sys.hash64": {
		Params: []Ty{Bytes},
		Ret:    U64,
		Emit: func(a []string, _ string) string {
			return "dp_sys_hash64(" + a[0] + ")"
		},
	},
	"Sys.u64_str": {
		Params: []Ty{U64},
		Ret:    Str,
		Emit: func(a []string, _ string) string {
			return "dp_sys_u64_str(a, " + a[0] + ")"
		},
	},
	"Sys.now_unix": {
		Ret: I64,
		Emit: func(_ []string, _ string) string {
			return "dp_sys_now_unix()"
		},
	},
	"BlobFs.read": {
		Params: []Ty{Str, Str},
		Ret:    Bytes,
		Emit: func(a []string, e string) string {
			return "dp_blob_read(a, " + a[0] + ", " + a[1] + ", &" + e + ")"
		},
	},
	"BlobFs.write": {
		Params:  []Ty{Str, Str, Bytes},
		Ret:     U64,
		RetName: "ETag",
		Wrap:    wrapETag,
		Emit: func(a []string, e string) string {
			return "dp_blob_write(a, " + a[0] + ", " + a[1] + ", " + a[2] + ", &" + e + ")"
		},
	},
	"BlobFs.backup": {
		Params: []Ty{Str, Str},
		Ret:    Unit,
		Emit: func(a []string, _ string) string {
			return "dp_blob_backup(a, " + a[0] + ", " + a[1] + ")"
		},
	},
	"BlobFs.create": {
		Params: []Ty{Str, Str},
		Ret:    Bool,
		Emit: func(a []string, e string) string {
			return "dp_blob_create(a, " + a[0] + ", " + a[1] + ", &" + e + ")"
		},
	},
	"BlobFs.list_json": {
		Params: []Ty{Str},
		Ret:    Bytes,
		Emit: func(a []string, e string) string {
			return "dp_blob_list(a, " + a[0] + ", &" + e + ")"
		},
	},
	"Versioning.commit_soon": {
		Params: []Ty{Str, Str},
		Ret:    Unit,
		Emit: func(a []string, _ string) string {
			return "dp_ver_commit_soon(" + a[0] + ", " + a[1] + ")"
		},
	},
}

// wrapETag 把 dp_blob_write 的 u64 包成契约里的 ETag。
// 这里出现 C 类型名是正常的：Emit 本来就产出 C，检查期不碰这一段。
func wrapETag(expr string) string {
	return "((dp_T_ETag){ .m_hash = " + expr + " })"
}

// Lookup 查一项能力；第二返回值表示运行时是否真的提供它。
func Lookup(name string) (Cap, bool) {
	c, ok := Caps[name]
	return c, ok
}
