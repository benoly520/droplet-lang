// Package check 对语法树做名字解析与类型检查。
//
// 本文件职责：把 boundary 声明合成成一张路由表（D3 的 B1，IMPL-PLAN §8.5）。
// 在此之前 `boundary` 只是契约导出与 AST 转储的输入，gen 完全不读它 —— 路由活在
// handler 的字符串比较里、条件请求活在运行时里、契约活在自己的世界里，三者可以各说各话。
package check

import (
	"strings"

	"droplet/internal/token"
)

// Route 是一条由声明合成出来的 HTTP 路由。生成器按它发出 dispatcher 与守卫，
// 契约导出按它给出完整的对外面 —— 两边读的是同一张表，不再各写一遍。
type Route struct {
	Verb         string   // 大写动词：GET / PUT / POST / DELETE
	Path         string   // 路由路径
	Fn           string   // 实现函数名（生成 dp_f_<Fn>）
	Slots        []string // 从请求里取值的前 N 个参数名（key / body），顺序即签名顺序
	Precondition bool     // 进 handler 之前比对 If-Match
	Ret          *Type    // 成功类型：bytes 或 Unit
	Method       string   // boundary 方法名（报错与契约用）
	Pos          token.Pos
}

// httpVerbs 是路由动词的白名单。动词在词法层是普通标识符（get 仍可作变量名），
// 只有出现在 `@` 后面才按路由解释 —— 这里是唯一的合法性判定点。
var httpVerbs = map[string]string{
	"get": "GET", "put": "PUT", "post": "POST", "delete": "DELETE",
}

// requestSlots 是 boundary 参数能从 HTTP 请求里取到值的名字表。
// 不在表里的名字没有绑定规则，静默给零值就是「起得来、全读空 key」——
// 所以 check 直接拒绝，并要求改名或改成能力参数。
var requestSlots = map[string]bool{"key": true, "body": true}

// isWriteVerb 判定动词是否会改状态（由此决定预条件门禁）。
func isWriteVerb(v string) bool {
	return v == "put" || v == "post" || v == "delete"
}

// checkRouting 合成路由表并逐条校验。它要同时看到 boundary 声明与函数签名，
// 所以在 declareFns 之后、检查函数体之前跑。
func (c *checker) checkRouting() {
	if c.bnd == nil {
		return
	}
	seen := map[string]token.Pos{}
	// 服务形态的判定与「路由是否合法」分开：路由写错时不该再叠一条「env 无人消费」，
	// 否则一条声明错误会多报一条无关的级联错误。
	for _, m := range c.bnd.Methods {
		if m.Impl != "" || m.Verb != "" {
			c.serviceForm = true
			break
		}
	}
	for _, m := range c.bnd.Methods {
		if m.Impl == "" {
			if m.Verb != "" {
				c.errorf(m.Pos, "E1",
					"方法 %s 声明了路由 @%s(%q) 却没有实现：补 `impl <函数名>`，或删掉路由（只有契约的方法不服务请求）",
					m.Name, m.Verb, m.Path)
			}
			continue
		}

		// ---- 路由本身 ----
		verb, ok := httpVerbs[m.Verb]
		if m.Verb == "" {
			c.errorf(m.Pos, "E1",
				"方法 %s 有实现 %s 却没有路由：写 `@get(\"/path\")` —— 路由是声明出来的，不从方法名推",
				m.Name, m.Impl)
			continue
		}
		if !ok {
			c.errorf(m.Pos, "E1",
				"路由动词 %q 不认识：只接受 get / put / post / delete", m.Verb)
			continue
		}
		if !strings.HasPrefix(m.Path, "/") {
			c.errorf(m.Pos, "E1", "路由路径 %q 必须以 / 开头", m.Path)
		}
		key := verb + " " + m.Path
		if prev, dup := seen[key]; dup {
			c.errorf(m.Pos, "E1", "路由 %s 重复声明（另一处在 %s）：一条路由只能有一个实现", key, prev)
		}
		seen[key] = m.Pos

		// ---- 预条件门禁：写路由必须显式面对「并发丢更新」 ----
		if m.Precondition && m.NoPrecondition {
			c.errorf(m.Pos, "E1",
				"方法 %s 同时写了 with precondition 与 without precondition：二选一", m.Name)
		}
		if isWriteVerb(m.Verb) && !m.Precondition && !m.NoPrecondition {
			c.errorf(m.Pos, "E1",
				"写路由 %s 必须显式表态：`with precondition`（进 handler 前比对 If-Match），或 `without precondition: \"理由\"` —— 沉默等于默认不做并发保护",
				key)
		}
		if m.Precondition && !isWriteVerb(m.Verb) {
			c.errorf(m.Pos, "E1",
				"只有写路由能声明 precondition：%s 不改变状态，预条件没有意义", key)
		}

		// ---- 成功类型（R1）：只有 bytes 与 Unit 能过 HTTP ----
		rt := c.resolve(m.Ret)
		if rt.Kind != KBytes && rt.Kind != KUnit && rt.Kind != KInvalid {
			hint := "：状态码不是能构造的值，是标签表的投影（SPEC §5.1.2）"
			if rt.Kind == KRecord && rt.FieldsByName["status"] != nil {
				hint = "；你写的 `Response{status: 200, body: X}` 在本语言里就是 `X`：状态码不是能构造的值，是标签表的投影"
			}
			c.errorf(m.Pos, "E1",
				"路由 %s 的成功类型必须是 bytes 或 Unit（得到 %s）%s", key, rt, hint)
			continue
		}

		// ---- 实现函数：参数前缀与返回类型都要对上 ----
		sig := c.fns[m.Impl]
		if sig == nil {
			c.errorf(m.Pos, "E1", "方法 %s 的实现 %s 不存在：先声明 `fn %s(...)`", m.Name, m.Impl, m.Impl)
			continue
		}
		if sig.IsEntry {
			c.errorf(m.Pos, "E1",
				"方法 %s 的实现 %s 不能同时是 entry：entry 是命令行入口，服务形态的入口就是这张路由表",
				m.Name, m.Impl)
			continue
		}
		if len(sig.Params) < len(m.Params) {
			c.errorf(c.fnPos(sig), "E1",
				"实现 %s 的参数比方法 %s 少：前 %d 个必须与 boundary 参数同名同类型，再接能力参数",
				m.Impl, m.Name, len(m.Params))
			continue
		}
		slots := make([]string, 0, len(m.Params))
		bad := false
		for i, p := range m.Params {
			want := c.resolve(p.Type)
			if sig.ParamNames[i] != p.Name || sig.Params[i] != want {
				c.errorf(c.fnPos(sig), "E1",
					"实现 %s 的第 %d 个参数必须是 %s: %s（与 boundary 方法 %s 一致），得到 %s: %s",
					m.Impl, i+1, p.Name, want, m.Name, sig.ParamNames[i], sig.Params[i])
				bad = true
				continue
			}
			if !requestSlots[p.Name] {
				c.errorf(m.Pos, "E1",
					"boundary 参数 %s 无法从 HTTP 请求绑定：目前只接受 key 与 body（其余进不了 handler）", p.Name)
				bad = true
			}
			slots = append(slots, p.Name)
		}
		for i := len(m.Params); i < len(sig.Params); i++ {
			p := sig.Params[i]
			if p.Kind == KHost || p.IsEnv {
				continue
			}
			c.errorf(c.fnPos(sig), "E1",
				"实现 %s 的参数 %s: %s 无法绑定：boundary 参数之后只能是 host 能力或 env 类型",
				m.Impl, sig.ParamNames[i], p)
			bad = true
		}
		if sig.Ret == nil || sig.Ret.Kind != KUnion {
			c.errorf(c.fnPos(sig), "E1",
				"实现 %s 必须返回 `%s | 错误`：错误是值，HTTP 状态码由它映射", m.Impl, rt)
			continue
		}
		if got := sig.Ret.Items[0]; got != rt && got.Kind != KInvalid && rt.Kind != KInvalid {
			c.errorf(c.fnPos(sig), "E1",
				"实现 %s 的成功类型是 %s，与方法 %s 声明的 %s 不一致", m.Impl, got, m.Name, rt)
		}
		if bad {
			continue
		}
		c.routes = append(c.routes, Route{
			Verb: verb, Path: m.Path, Fn: m.Impl, Slots: slots,
			Precondition: m.Precondition, Ret: rt, Method: m.Name, Pos: m.Pos,
		})
	}
	if c.routes == nil {
		c.routes = []Route{}
	}
}

// routesPos 给出路由表第一处声明位置，用于「服务形态缺配置」这类整体性报错。
func (c *checker) routesPos() token.Pos {
	if len(c.routes) > 0 {
		return c.routes[0].Pos
	}
	return token.Pos{}
}

// fnPos 给出函数的声明位置（签名里的 Pos 可能只是名字位置）。
func (c *checker) fnPos(sig *FnSig) token.Pos {
	if sig.Decl != nil {
		return sig.Decl.Pos
	}
	return sig.Pos
}
