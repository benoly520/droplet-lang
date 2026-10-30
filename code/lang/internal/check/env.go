// Package check 对语法树做名字解析与类型检查。
//
// 本文件职责：env 声明的完整性，以及「服务形态必须自带配置」这条接线规则。
// D3 起服务形态由 boundary 的路由表定义（不再由「entry 带 Request 参数」定义），
// 所以这条规则也跟着路由走：有路由就必须有 env，且五个底座键齐备。
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

// checkEnvService 是所有声明与函数体都检查完之后跑的一遍：它要同时看到
// env 块、路由表与程序形态，早跑会拿到不完整的事实。
//
// D3 之前「服务形态」= 入口带 Request 参数；现在 = boundary 里有路由。
// 后者是唯一的事实源，所以 entry 与路由不能并存：一个程序要有两个入口，
// 就等于「路由由谁定义」又变成两处。
func (c *checker) checkEnvService() {
	entry := c.entrySig()

	if !c.serviceForm {
		// 没有路由：CLI 程序或纯契约程序。配置面不存在，env 也没人消费。
		if c.env != nil {
			c.errorf(c.env.Pos, "E1",
				"env %s 无人消费：env 只用于 HTTP 服务形态（boundary 里有 `@get(\"/path\") impl <函数>` 才行）",
				c.env.Name)
		}
		return
	}

	if entry != nil {
		c.errorf(entryNodePos(entry), "E1",
			"服务形态的入口是 boundary 的路由表，entry %s 不能并存：把它的分支改写成路由的实现函数",
			entry.Name)
	}

	if c.env == nil {
		c.errorf(c.routesPos(), "E1",
			"服务必须声明 env 块：运行时要 %s 五个键（见 SPEC §5.7）", cfgKeyNames())
		return
	}
	c.checkEnvKeys()
}

// checkEnvKeys 校验底座五个键：一个都不能少，类型必须是生成代码能接的那些。
// 业务私有键不在这里管 —— 多出来的键由实现函数自己读。
func (c *checker) checkEnvKeys() {
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
}

// entrySig 取入口签名；没有入口（纯契约程序或服务形态）返回 nil。
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

// entryNodePos 给出 entry 的声明位置，用于把「不能并存」这类形态错误指到入口处。
func entryNodePos(sig *FnSig) token.Pos {
	if sig.Decl != nil {
		return sig.Decl.Pos
	}
	return sig.Pos
}
