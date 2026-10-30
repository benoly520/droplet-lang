// Package check 对语法树做名字解析与类型检查。
//
// 本文件职责：env 声明的完整性，以及「服务形态必须自带配置」这条接线规则。
// 拆出来是因为这两件事都横跨声明（env 块）与签名（entry 的形态），
// 放在 decls.go 或 calls.go 任一侧都会让另一侧看不见规则的全貌。
package check

import (
	"sort"
	"strings"

	"droplet/internal/token"
)

// runtimeCfg 是服务底座：键名与类型都固定，因为生成的 main 要把它们填进 dp_cfg。
// 缺一个就是「拿默认值起服务」，而那正是 SPEC §5.7 要消灭的静默降级：token 缺失
// 等于不鉴权，root 缺失等于写到 ./data。业务私有键可以另加，由 handler 自己读。
var runtimeCfg = []struct {
	name string
	typ  *Type
}{
	{"port", tU64},
	{"bind", tStr},
	{"root", tStr},
	{"token", tStr},
	{"max_body", tU64},
}

// cfgKeyNames 用于报错文本：「运行时要 port / bind / root / token / max_body 五个键」。
func cfgKeyNames() string {
	names := make([]string, 0, len(runtimeCfg))
	for _, k := range runtimeCfg {
		names = append(names, k.name)
	}
	return strings.Join(names, " / ")
}

// envPrefix 由 env 块名推出环境变量前缀：App → APP，键 root 的读取面是 APP_ROOT。
// 源码里永远不出现环境变量名（SPEC §5.7），映射规则只在这里定义一次。
func envPrefix(name string) string {
	return strings.ToUpper(name)
}

// EnvVar 是一个配置键对应的环境变量名。导出给生成器与契约导出用。
func EnvVar(prefix, key string) string {
	return prefix + "_" + strings.ToUpper(key)
}

// detectService 判定入口是不是 HTTP 服务形态：带一个名为 Request 的 record 参数。
// 这条规则以前只长在 gen（requestParamIndex），于是 checker 无法据此要求配置；
// 搬到这里，「响应字段形状」那类 gen 独有知识就少了一条（见 D0-DEVIATIONS §4.1）。
func (c *checker) detectService(sig *FnSig) {
	for i, p := range sig.Params {
		if p.Kind == KRecord && p.Name == "Request" {
			sig.IsService = true
			sig.ReqIdx = i
			return
		}
	}
}

// checkEnvService 是所有声明与函数体都检查完之后跑的一遍：它要同时看到
// env 块、入口签名与程序形态，早跑会拿到不完整的事实。
func (c *checker) checkEnvService() {
	for _, sig := range c.fns {
		c.detectService(sig)
	}

	entry := c.entrySig()
	if entry == nil {
		// 纯契约程序（没有入口）：env 只作为契约面导出给运维核对，
		// 没有「起服务」这一步，所以也不要求底座五键。
		return
	}
	if entry.IsService {
		c.checkServiceResult(entry)
	}
	if c.env == nil {
		// 非服务程序没有配置面：hello / shape 这类命令行程序继续走 argv，不要求 env。
		if entry.IsService {
			c.errorf(entryNodePos(entry), "E1",
				"服务必须声明 env 块：运行时要 %s 五个键（见 SPEC §5.7）", cfgKeyNames())
		}
		return
	}

	if !entry.IsService {
		c.errorf(c.env.Pos, "E1",
			"env %s 无人消费：命令行程序的配置走 argv，env 只用于 HTTP 服务形态", c.env.Name)
		return
	}

	// 服务形态：五个底座键一个都不能少，且类型必须是生成代码能接的那些。
	got := map[string]*EnvKey{}
	for i := range c.env.Keys {
		got[c.env.Keys[i].Name] = &c.env.Keys[i]
	}
	for _, want := range runtimeCfg {
		k, ok := got[want.name]
		if !ok {
			c.errorf(c.env.Pos, "E1",
				"env %s 缺配置键 %s：运行时按 %s 服务，补齐它（默认值可以写在声明里）",
				c.env.Name, want.name, cfgKeyNames())
			continue
		}
		if k.Type != want.typ {
			c.errorf(k.Pos, "E1",
				"配置键 %s 必须是 %s：它直接映射到服务的监听参数，得到 %s",
				want.name, want.typ.Name, k.Type.Name)
		}
	}

	// 服务入口的参数必须都能绑定：Request 来自请求，host 来自能力表，
	// env 类型来自刚才加载的配置。其余类型（包括以前那个「第一个 str 参数就是 root」
	// 的位置魔法）没有绑定规则，静默给零值就是「起得来、全读空目录」。
	for i, p := range entry.Params {
		if i == entry.ReqIdx || p.Kind == KHost || p.IsEnv {
			continue
		}
		c.errorf(entryNodePos(entry), "E1",
			"entry %s 的参数 %s: %s 在服务形态里无法绑定：只接受 Request、host 能力与 env 类型",
			entry.Name, entry.ParamNames[i], p.Name)
	}
}

// checkServiceResult 限制服务入口的结果形状（D2 的 R1）：成功只有「有体的 bytes」与
// 「无体的 Unit」两种，错误只能是联合里的错误项。
//
// 以前这里放的是 `Response{status, body}` 记录：状态码在 handler 里手写，`check` 说 ok、
// `build` 才发现字段形状不对（D0-DEVIATIONS §2）。删掉记录之后，状态码只在
// 变体 → 状态码的映射表里出现，也就不存在「能构造出一个运行时不懂的响应」。
func (c *checker) checkServiceResult(sig *FnSig) {
	ret := sig.Ret
	if ret == nil || ret.Kind == KInvalid {
		return
	}
	if ret.Kind != KUnion {
		c.errorf(entryNodePos(sig), "E1",
			"entry %s 用于服务时必须返回 `bytes | 错误` 或 `Unit | 错误`，得到 %s", sig.Name, ret)
		return
	}
	ok := okType(ret)
	switch ok.Kind {
	case KBytes, KUnit, KInvalid:
		return
	}
	hint := ""
	if ok.Kind == KRecord && ok.FieldsByName["status"] != nil {
		hint = "；你写的 `Response{status: 200, body: X}` 在本语言里就是 `X`：状态码不是能构造的值，是标签表的投影"
	}
	c.errorf(entryNodePos(sig), "E1",
		"entry %s 的成功类型必须是 bytes 或 Unit，得到 %s%s", sig.Name, ok, hint)
}

// entrySig 取入口签名；没有入口（纯契约程序）返回 nil。
// c.fns 是 map，多入口时按名字排序取第一个 —— 与 Program() 选 EntryPoint 的规则一致。
func (c *checker) entrySig() *FnSig {
	var names []string
	for name, sig := range c.fns {
		if sig.IsEntry {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	return c.fns[names[0]]
}

// entryNodePos 给出 entry 的声明位置，用于把「缺 env」这类形态错误指到入口处。
func entryNodePos(sig *FnSig) token.Pos {
	if sig.Decl != nil {
		return sig.Decl.Pos
	}
	return sig.Pos
}
