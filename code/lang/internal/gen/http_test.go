package gen

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"droplet/internal/check"
)

// startBlobd 编译 webapi 契约并拉起服务，返回 base URL 与清理函数。
func startBlobd(t *testing.T) (string, func()) {
	t.Helper()
	return startBlobdIn(t, filepath.Join(t.TempDir(), "data"))
}

// startBlobdIn 用指定的 root 起服务，env 形如 "K=V" 追加到环境。
func startBlobdIn(t *testing.T, root string, env ...string) (string, func()) {
	t.Helper()

	bin := build(t,
		source{"contract.dlt", genSource(t, "../../../webapi/src/contract.dlt")},
		source{"main.dlt", genSource(t, "../../../webapi/src/main.dlt")},
	)

	// 先占一个空闲端口再释放：窗口极小，够测试用
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	// 服务端日志只在失败时有用，平时不看
	logPath := filepath.Join(t.TempDir(), "blobd.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			if b, readErr := os.ReadFile(logPath); readErr == nil && len(b) > 0 {
				t.Logf("服务端日志：\n%s", b)
			}
		}
		logFile.Close()
	})

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("APP_PORT=%d", port),
		"APP_ROOT="+root,
	)
	cmd.Env = append(cmd.Env, env...)
	cmd.Stdout = io.Discard
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/health")
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	stop := func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	return base, stop
}

func do(t *testing.T, method, url string, body string, headers map[string]string) (int, string, http.Header) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

// TestServiceEndToEnd 跑完 M3 的判据：五接口 + 412 / 404 / 400 / 413。
func TestServiceEndToEnd(t *testing.T) {
	base, stop := startBlobd(t)
	defer stop()

	t.Run("health", func(t *testing.T) {
		code, body, hdr := do(t, "GET", base+"/health", "", nil)
		if code != 200 || body != `{"ok":true}` {
			t.Fatalf("code=%d body=%q", code, body)
		}
		// 推导规则只看 body：handler 一个字也没写 ETag，非空表示就有版本
		if hdr.Get("ETag") == "" {
			t.Fatal("非空 body 应有运行时推导的 ETag")
		}
	})

	var etag string
	t.Run("put", func(t *testing.T) {
		code, _, hdr := do(t, "PUT", base+"/v1/blob/demo", "hello", nil)
		if code != 200 {
			t.Fatalf("PUT 返回 %d", code)
		}
		// ETag 由运行时从 body 推导：PUT 的响应体是空的，没有表示就没有版本（v0.10）
		if got := hdr.Get("ETag"); got != "" {
			t.Fatalf("PUT 空 body 不该带 ETag，得到 %q", got)
		}
	})

	t.Run("get", func(t *testing.T) {
		code, body, hdr := do(t, "GET", base+"/v1/blob/demo", "", nil)
		if code != 200 || body != "hello" {
			t.Fatalf("code=%d body=%q", code, body)
		}
		// 新版本号从 GET 拿：这是 v0.10 之后客户端唯一的取 tag 路径
		etag = hdr.Get("ETag")
		if etag == "" {
			t.Fatal("GET 响应缺少 ETag：它应由运行时从 body 推导，handler 不写")
		}
	})

	t.Run("list", func(t *testing.T) {
		code, body, _ := do(t, "GET", base+"/v1/keys", "", nil)
		if code != 200 || body != `["demo"]` {
			t.Fatalf("code=%d body=%q", code, body)
		}
	})

	t.Run("create", func(t *testing.T) {
		code, _, _ := do(t, "POST", base+"/v1/keys/fresh", "", nil)
		if code != 200 {
			t.Fatalf("POST 返回 %d", code)
		}
		code, body, _ := do(t, "GET", base+"/v1/keys", "", nil)
		if code != 200 || body != `["demo","fresh"]` {
			t.Fatalf("列表 code=%d body=%q", code, body)
		}
	})

	t.Run("if-match-ok", func(t *testing.T) {
		code, _, _ := do(t, "PUT", base+"/v1/blob/demo", "hello2", map[string]string{"If-Match": etag})
		if code != 200 {
			t.Fatalf("正确 If-Match 应 200，得到 %d", code)
		}
	})

	t.Run("etag-follows-body", func(t *testing.T) {
		code, body, hdr := do(t, "GET", base+"/v1/blob/demo", "", nil)
		if code != 200 || body != "hello2" {
			t.Fatalf("code=%d body=%q", code, body)
		}
		if hdr.Get("ETag") == etag {
			t.Fatalf("body 变了而 ETag 没变：%q", hdr.Get("ETag"))
		}
	})

	t.Run("if-match-412", func(t *testing.T) {
		code, _, _ := do(t, "PUT", base+"/v1/blob/demo", "nope", map[string]string{"If-Match": `"deadbeef"`})
		if code != 412 {
			t.Fatalf("错误 If-Match 应 412，得到 %d", code)
		}
	})

	t.Run("404", func(t *testing.T) {
		code, _, _ := do(t, "GET", base+"/v1/blob/missing", "", nil)
		if code != 404 {
			t.Fatalf("不存在应 404，得到 %d", code)
		}
	})

	t.Run("400-bad-key", func(t *testing.T) {
		code, _, _ := do(t, "PUT", base+"/v1/blob/Bad%2fKey", "x", nil)
		if code != 400 {
			t.Fatalf("非法 key 应 400，得到 %d", code)
		}
	})

	t.Run("405", func(t *testing.T) {
		code, _, hdr := do(t, "DELETE", base+"/v1/blob/demo", "", nil)
		if code != 405 {
			t.Fatalf("DELETE 应 405，得到 %d", code)
		}
		if got := hdr.Get("ETag"); got != "" {
			t.Fatalf("空 body 不该有 ETag，得到 %q", got)
		}
	})

	t.Run("413", func(t *testing.T) {
		// 服务端在读 body 前就拒绝，声明 Expect 让客户端先等回应（RFC 9110 §10.1.1），
		// 否则发送途中会收到 RST，得到的是连接错误而不是 413。
		big := strings.Repeat("x", 8<<20+1)
		code, _, _ := do(t, "PUT", base+"/v1/blob/demo", big, map[string]string{"Expect": "100-continue"})
		if code != 413 {
			t.Fatalf("超限 body 应 413，得到 %d", code)
		}
	})
}

// svcEnv 是服务形态必需的 env 声明：checker 要求补齐运行时五键（SPEC §5.7），
// 内联探针都要带上它才能走到生成期。值本身不重要，这些探针不跑进程。
const svcEnv = `env App {
  port: u64 = 18999,
  bind: str = "127.0.0.1",
  root: str = "/tmp/dp-probe",
  token: str = "",
  max_body: u64 = 1024
}

`

// genErrText 走完 parse → check → gen，返回生成期的报错（没有错误返回 ""）。
func genErrText(t *testing.T, text string) string {
	t.Helper()
	prog := parseAll(t, source{"svc.dlt", text})
	res := check.Program(prog)
	if len(res.Errors) > 0 {
		t.Fatalf("源码有 %d 个检查错误：%v", len(res.Errors), res.Errors)
	}
	_, err := C(prog, res)
	if err == nil {
		return ""
	}
	return err.Error()
}

// TestResponseEtagFieldRejected 锁住 v0.10 的第四条语法减法：ETag 由运行时从 body
// 推导，所以 Response 里再写 etag 不是「可以忽略」，而是在生成期就报错并给改法。
func TestResponseEtagFieldRejected(t *testing.T) {
	got := genErrText(t, svcEnv+`type ApiError = NotFound

type Request { method: str }

type Response {
  status: u64,
  body: bytes,
  etag: str
}

entry handle(req: Request) -> Response | ApiError
  invariant "一律 405"
{
  return Response{status: 405, body: b"", etag: "x"}
}`)
	if !strings.Contains(got, "Response.etag 不用写") {
		t.Fatalf("期望「Response.etag 不用写」，得到：%q", got)
	}
}

// TestUnknownResponseFieldRejected：不在响应面上的字段一律拒绝，不能静默丢弃。
func TestUnknownResponseFieldRejected(t *testing.T) {
	got := genErrText(t, svcEnv+`type ApiError = NotFound

type Request { method: str }

type Response {
  status: u64,
  body: bytes,
  retry_after: u64
}

entry handle(req: Request) -> Response | ApiError
  invariant "一律 405"
{
  return Response{status: 405, body: b"", retry_after: 1}
}`)
	if !strings.Contains(got, "Response.retry_after 无法映射到响应") {
		t.Fatalf("期望「Response.retry_after 无法映射到响应」，得到：%q", got)
	}
}

// TestNotFoundMapsTo404 单独确认 404 不是 handler 里写死的。
func TestNotFoundMapsTo404(t *testing.T) {
	base, stop := startBlobd(t)
	defer stop()

	code, body, _ := do(t, "GET", base+"/v1/blob/absent", "", nil)
	if code != 404 {
		t.Fatalf("期望 404，得到 %d（body=%q）", code, body)
	}
}

// snapshotFiles 列出某个 key 的快照文件名（不算目录）。
func snapshotFiles(t *testing.T, root, key string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, ".snapshots"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("读快照目录失败：%v", err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), key+".") {
			out = append(out, e.Name())
		}
	}
	return out
}

// TestSnapshotOnPut 锁定 PUT 的顺序：先备份旧版本，再原子写。
func TestSnapshotOnPut(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	base, stop := startBlobdIn(t, root)
	defer stop()

	if code, _, _ := do(t, "PUT", base+"/v1/blob/demo", "v1", nil); code != 200 {
		t.Fatalf("首次 PUT 返回 %d", code)
	}
	// 没有旧版本可备份，就不该凭空造快照
	if got := snapshotFiles(t, root, "demo"); len(got) != 0 {
		t.Fatalf("首次 PUT 不该产生快照，得到 %v", got)
	}

	if code, _, _ := do(t, "PUT", base+"/v1/blob/demo", "v2", nil); code != 200 {
		t.Fatalf("第二次 PUT 返回 %d", code)
	}
	snaps := snapshotFiles(t, root, "demo")
	if len(snaps) != 1 {
		t.Fatalf("第二次 PUT 应产生 1 份快照，得到 %v", snaps)
	}
	old, err := os.ReadFile(filepath.Join(root, ".snapshots", snaps[0]))
	if err != nil {
		t.Fatal(err)
	}
	if string(old) != "v1" {
		t.Fatalf("快照内容应为 v1，得到 %q", old)
	}
	if code, body, _ := do(t, "GET", base+"/v1/blob/demo", "", nil); code != 200 || body != "v2" {
		t.Fatalf("当前版本应该已经变成 v2，code=%d body=%q", code, body)
	}
}

// TestSnapshotNoOverwrite 同一秒内的多次写不能互相覆盖：文件名带了 seq。
func TestSnapshotNoOverwrite(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	base, stop := startBlobdIn(t, root, "DP_SNAPSHOT_KEEP=0")
	defer stop()

	for i := 1; i <= 6; i++ {
		body := fmt.Sprintf("v%d", i)
		if code, _, _ := do(t, "PUT", base+"/v1/blob/demo", body, nil); code != 200 {
			t.Fatalf("第 %d 次 PUT 返回 %d", i, code)
		}
	}
	// 6 次写 = 5 份快照（第一次没有旧版本可备）；keep=0 表示不轮转。
	if got := snapshotFiles(t, root, "demo"); len(got) != 5 {
		t.Fatalf("应留下 5 份快照，得到 %d：%v", len(got), got)
	}
}

// TestSnapshotRotation 锁定轮转：每 key 只留最近 DP_SNAPSHOT_KEEP 份。
func TestSnapshotRotation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	base, stop := startBlobdIn(t, root, "DP_SNAPSHOT_KEEP=3")
	defer stop()

	for i := 1; i <= 6; i++ {
		body := fmt.Sprintf("v%d", i)
		if code, _, _ := do(t, "PUT", base+"/v1/blob/demo", body, nil); code != 200 {
			t.Fatalf("第 %d 次 PUT 返回 %d", i, code)
		}
	}

	snaps := snapshotFiles(t, root, "demo")
	if len(snaps) != 3 {
		t.Fatalf("应只剩 3 份快照，得到 %d：%v", len(snaps), snaps)
	}
	var got []string
	for _, s := range snaps {
		b, err := os.ReadFile(filepath.Join(root, ".snapshots", s))
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, string(b))
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "v3,v4,v5" {
		t.Fatalf("应保留 v3,v4,v5，实际 %v（文件 %v）", got, snaps)
	}
}

// TestVersioningCommit 验收版本化钩子：响应不等 git，但事后果真有提交。
func TestVersioningCommit(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	git := gitRepo(t, root)
	base, stop := startBlobdIn(t, root,
		"DP_COMMIT_CMD="+commitCmd(git),
		"DP_COMMIT_DEBOUNCE=0", // 不防抖，确保这次调用真的会 fork
	)
	defer stop()

	if code, _, _ := do(t, "PUT", base+"/v1/blob/demo", "v1", nil); code != 200 {
		t.Fatalf("PUT 返回 %d", code)
	}
	waitCommit(t, git, root)

	files, err := exec.Command(git, "-C", root, "log", "-1", "--name-only", "--pretty=format:").Output()
	if err != nil {
		t.Fatalf("读提交内容失败：%v", err)
	}
	if !strings.Contains(string(files), "demo") {
		t.Fatalf("提交里没有 demo：\n%s", files)
	}
}

// TestVersioningDebounce 锁定防抖：窗口内连写多次也只起一个提交进程。
func TestVersioningDebounce(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	git := gitRepo(t, root)
	base, stop := startBlobdIn(t, root,
		"DP_COMMIT_CMD="+commitCmd(git),
		"DP_COMMIT_DEBOUNCE=60", // 默认窗口：后两次写必须被挡住
	)
	defer stop()

	for _, v := range []string{"v1", "v2", "v3"} {
		if code, _, _ := do(t, "PUT", base+"/v1/blob/demo", v, nil); code != 200 {
			t.Fatalf("PUT %s 返回 %d", v, code)
		}
	}
	waitCommit(t, git, root)

	// 给可能迟到的第二个子进程留出暴露时间
	time.Sleep(300 * time.Millisecond)
	if n := commitCount(t, git, root); n != 1 {
		t.Fatalf("防抖窗口内应该只有 1 次提交，得到 %d", n)
	}
}

// gitRepo 在 root 建一个仓库并返回 git 路径；没有 git 就跳过。
func gitRepo(t *testing.T, root string) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("环境里没有 git，跳过版本化验收")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	init := exec.Command(git, "init", "-q", ".")
	init.Dir = root
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("git init 失败：%v\n%s", err, out)
	}
	return git
}

// commitCmd 给出一条只影响本次的提交命令行，不去写全局 git 配置。
// 只 add $DP_COMMIT_KEY 这一个路径：扫全树会撞上正在 rename 的临时文件。
func commitCmd(git string) string {
	return fmt.Sprintf(
		`%s add -A -- "$DP_COMMIT_KEY" && %s -c user.name=droplet -c user.email=dp@example.com commit -q -m "snapshot $DP_COMMIT_KEY"`,
		git, git,
	)
}

// commitCount 数 HEAD 上的提交数；还没有提交时返回 0。
func commitCount(t *testing.T, git, root string) int {
	t.Helper()
	out, err := exec.Command(git, "-C", root, "rev-list", "--count", "HEAD").Output()
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("提交数不是整数：%q", out)
	}
	return n
}

// waitCommit 等子进程把提交落下来；无提交时 git 会把 fatal 写到 stderr，所以不能看 log。
func waitCommit(t *testing.T, git, root string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if commitCount(t, git, root) > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("等了 10s 还是没有提交：防抖器或子进程没跑起来")
}

// TestMissingRootFailsFast 数据目录不存在时必须当场退出。
// 否则服务能起来、每个写请求都 500，那是「启动成功、运行全废」的静默降级。
func TestMissingRootFailsFast(t *testing.T) {
	bin := build(t,
		source{"contract.dlt", genSource(t, "../../../webapi/src/contract.dlt")},
		source{"main.dlt", genSource(t, "../../../webapi/src/main.dlt")},
	)

	missing := filepath.Join(t.TempDir(), "nope")
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "APP_PORT=18795", "APP_ROOT="+missing)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("数据目录缺失时不应启动成功，输出：%s", out)
	}
	if !strings.Contains(string(out), "数据目录不可用") {
		t.Fatalf("退出原因应指明数据目录，得到：%s", out)
	}
}

// TestMissingRequiredCfgFailsFast：无默认值的键缺失 → 不起服务，报错要说出
// 环境变量名与它来自哪个 env 块。以前这里会用 ./data 默默起来。
func TestMissingRequiredCfgFailsFast(t *testing.T) {
	bin := build(t,
		source{"contract.dlt", genSource(t, "../../../webapi/src/contract.dlt")},
		source{"main.dlt", genSource(t, "../../../webapi/src/main.dlt")},
	)

	cmd := exec.Command(bin)
	cmd.Env = []string{"APP_PORT=18796"} // 故意不给 APP_ROOT
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("必填配置缺失时不应启动成功，输出：%s", out)
	}
	if !strings.Contains(string(out), "缺少配置 APP_ROOT") {
		t.Fatalf("退出原因应指明缺哪个键，得到：%s", out)
	}
}

// TestBadCfgValueFailsFast：u64 键拿到非数字时报错退出，不静默回退到默认端口。
func TestBadCfgValueFailsFast(t *testing.T) {
	bin := build(t,
		source{"contract.dlt", genSource(t, "../../../webapi/src/contract.dlt")},
		source{"main.dlt", genSource(t, "../../../webapi/src/main.dlt")},
	)

	missing := filepath.Join(t.TempDir(), "nope")
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "APP_PORT=1879x", "APP_ROOT="+missing)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("非法端口不应启动成功，输出：%s", out)
	}
	if !strings.Contains(string(out), "APP_PORT=1879x 不是整数") {
		t.Fatalf("退出原因应指明非法值，得到：%s", out)
	}
}
