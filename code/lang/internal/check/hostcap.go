// Package check 对语法树做名字解析与类型检查。
//
// 本文件由 check.go 同包拆分而来，职责：// host 能力表比对：声明的能力运行时必须真的提供，且参数与返回类型对得上。
package check

import (
	"droplet/internal/ast"
	"droplet/internal/host"
)

// ---------- 能力表比对 ----------
// checkHostCap 确认声明的能力运行时真的提供，且参数与返回类型对得上。
//
// 能力表是 check 与 gen 共用的（internal/host）：只有 checker 也查一次，
// `droplet check` 的 ok 才等于「能构建」。不查类型时，声明谎报参数类型只会
// 在 cc 那一层炸出 `incompatible type for argument`——没有 E 码，也没有 .dlt 行号。
func (c *checker) checkHostCap(group string, fn *ast.FnDecl, sig *FnSig) {
	key := group + "." + fn.Name
	cap, ok := host.Lookup(key)
	if !ok {
		c.errorf(fn.Pos, "E2", "运行时不提供能力 %s；D0 能力表见 internal/host/host.go", key)
		return
	}
	if len(sig.Params) != cap.Arity() {
		c.errorf(fn.Pos, "E2", "能力 %s 需要 %d 个参数，声明了 %d", key, cap.Arity(), len(sig.Params))
		return
	}
	for i, p := range sig.Params {
		ty, ok := hostTy(p)
		if !ok {
			c.errorf(fn.Params[i].Pos, "E2",
				"能力 %s 的第 %d 个参数不能是 %s：运行时只接受 str/bytes/u64/i64/bool/Unit",
				key, i+1, p)
			continue
		}
		if ty != cap.Params[i] {
			c.errorf(fn.Params[i].Pos, "E2",
				"能力 %s 的第 %d 个参数运行时是 %s，声明为 %s", key, i+1, cap.Params[i], ty)
		}
	}

	ret := sig.Ret
	if ret == nil || ret.Kind == KInvalid {
		return // 类型解析已经报过错了，别再级联
	}
	if ret.Kind == KUnion {
		ret = ret.Items[0]
	}
	want := cap.RetName
	if want == "" {
		want = cap.Ret.String()
	}
	if ret.Name != want {
		c.errorf(fn.Pos, "E2", "能力 %s 运行时返回 %s，声明为 %s", key, want, ret)
	}
}

// hostTy 把检查期类型映射成运行时 ABI 类型。能力的参数只能是这六种标量；
// record 只会出现在返回值上（由 Cap.RetName + Cap.Wrap 负责包装）。
func hostTy(t *Type) (host.Ty, bool) {
	switch t.Kind {
	case KStr:
		return host.Str, true
	case KBytes:
		return host.Bytes, true
	case KU64:
		return host.U64, true
	case KI64:
		return host.I64, true
	case KBool:
		return host.Bool, true
	case KUnit:
		return host.Unit, true
	}
	return 0, false
}
