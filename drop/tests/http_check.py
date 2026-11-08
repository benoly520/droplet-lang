#!/usr/bin/env python3
# 服务器这一段不用 curl，自己开 socket：只有这样才能把「只回头部不回正文」「同一条连接读第二个
# 请求」「只认 CRLF」「超过上限直接断连接且不报错」这几条钉死。
# 打的是一份文字实录，check.sh 拿它和 tests/http.expected 逐字比。Date 那一行换成 *，
# 否则每次都不同。被起的两份文件都在命令行上给：lib/http.dr（库）+ 程序。
# 用法：python3 tests/http_check.py <端口>
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import re
import time

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 8123
PY = sys.executable
LIB = os.path.join(ROOT, "lib", "http.dr")
POST_BODY = "你好 body".encode("utf-8")   # 11 个字节：两个汉字各 3 个
CRLF = b"\r\n"
END = CRLF * 2
_PEER = re.compile(rb"127\.0\.0\.1:\d+")


def boot(app, port, tmp):
    """起服务器：宿主除了报错什么都不往 stderr 写，所以只能自己试连，等端口肯接。
       等不到就抛 —— 不许门自己卡住。"""
    p = subprocess.Popen([PY, os.path.join(ROOT, "drop.py"), "--port", str(port), LIB, app],
                         cwd=tmp, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    deadline = time.time() + 8
    while time.time() < deadline:
        if p.poll() is not None:
            raise RuntimeError("服务器起不来：rc=%s %s" % (p.returncode, p.stderr.read()[-200:]))
        try:
            s = socket.create_connection(("127.0.0.1", port), timeout=0.3)
            s.close()
            return p
        except OSError:
            time.sleep(0.05)
    raise RuntimeError("等不到端口起来（8 秒）")


def _recv(s):
    try:
        return s.recv(4096)
    except socket.timeout:
        return b""      # 超时不是响应的一部分：当成「对端不再发了」
    except ConnectionResetError:
        return b""      # 服务器断这条连接时客户端还有没读走的字节 = RST：和「断了」同一件事


def ask(port, req, read_more=False, fin=False):
    """发一个请求，按 Content-Length 精确读完正文；read_more 时再看对端有没有接着发。"""
    s = socket.create_connection(("127.0.0.1", port), timeout=12)
    s.settimeout(12)
    s.sendall(req)
    if fin:
        s.shutdown(socket.SHUT_WR)       # 发完就半关：让服务器拿到 EOF，而不是干等
    buf = b""
    while END not in buf:
        ch = _recv(s)
        if not ch:
            s.close()
            return buf                          # 头部都没读全：对端已经断了
        buf += ch
    head, rest = buf.split(END, 1)
    cl = 0
    for ln in head.split(CRLF)[1:]:
        if ln.startswith(b"Content-Length:"):
            cl = int(ln.split(b":", 1)[1])
    while len(rest) < cl:
        ch = _recv(s)
        if not ch:
            break
        rest += ch
    out = head + END + rest
    extra = b""
    if read_more:
        try:
            extra = s.recv(4096)                # 只认 CRLF 那条要看是不是真的断了
        except socket.timeout:
            extra = b"<still open>"
    s.close()
    if extra:
        out += b"[[extra %d bytes]]" % len(extra)   # 标记用 ASCII：这是协议实录，别混进非 ASCII
    return out


def show(label, data):
    print("— %s" % label)
    data = _PEER.sub(b"127.0.0.1:*", data)   # 对端的临时端口号每次都不同，折成 *
    try:
        txt = data.decode("utf-8")        # 正文是 UTF-8 就读得懂；换了字节就换一种读法
    except UnicodeDecodeError:
        txt = data.decode("latin-1")
    lines = []
    for ln in txt.split("\r\n"):
        lines.append("Date: *" if ln.startswith("Date: ") else ln)
    print("\r\n".join(lines).rstrip("\r\n"))


def main():
    tmp = tempfile.mkdtemp(prefix="drop-http-")
    srv = None
    try:
        srv = boot(os.path.join(ROOT, "examples", "http.dr"), PORT, tmp)
        show("GET / —— 一个 200，正文四个字节",
             ask(PORT, b"GET / HTTP/1.1" + CRLF + b"Host: x" + END))
        show("GET 一条没有的路 —— 404",
             ask(PORT, b"GET /nope HTTP/1.1" + CRLF + b"Host: x" + END))
        show("HEAD / —— 只回头，Content-Length 写 0，正文一个字节都不发",
             ask(PORT, b"HEAD / HTTP/1.1" + CRLF + b"Host: x" + END))
        show("DELETE / —— 程序自己不认的方法：405",
             ask(PORT, b"DELETE / HTTP/1.1" + CRLF + b"Host: x" + END))
        post = ask(PORT, b"POST / HTTP/1.1" + CRLF + b"Host: x" + CRLF +
                   b"Content-Length: 11" + END + POST_BODY)
        show("POST —— 正文走 bytes 那条路，回来的还是那些字节", post)
        print("  [回来的最后 %d 个字节和发出去的一模一样吗] %s"
              % (len(POST_BODY), post[-len(POST_BODY):] == POST_BODY))
        s = socket.create_connection(("127.0.0.1", PORT), timeout=12)
        s.settimeout(12)
        s.sendall(b"GET / HTTP/1.1" + CRLF + b"Host: x" + CRLF + b"Connection: keep-alive" + END)
        a = _recv(s)
        s.sendall(b"GET /ver HTTP/1.1" + CRLF + b"Host: x" + END)
        b = _recv(s)
        s.close()
        show("同一条连接读完第一个请求，接着读第二个（keep-alive）", a)
        show("第二个响应：还在同一条连接上，没再建过一次连接", b)
        show("对端写了 Connection: close（一模一样）—— 多回一行，并且真的关",
             ask(PORT, b"GET / HTTP/1.1" + CRLF + b"Host: x" + CRLF + b"Connection: close" + END))
        show("行分隔只认 CRLF：LF-only 的请求不认，直接断这条连接",
             ask(PORT, b"GET / HTTP/1.1\n\n", read_more=True, fin=True))
        show("首部超过 64 KiB：断这条连接，不是错误码",
             ask(PORT, b"GET / HTTP/1.1" + CRLF + b"X: " + b"a" * 70000 + END))
        show("正文声明超过 1 MiB：断这条连接，不是错误码",
             ask(PORT, b"POST / HTTP/1.1" + CRLF + b"Host: x" + CRLF +
                 b"Content-Length: 2000000" + END + b"x" * 100))
        show("GET /len —— 程序自己数请求的字节数",
             ask(PORT, b"GET /len HTTP/1.1" + CRLF + b"Host: x" + END))
        show("GET /peer —— 对端地址是个字符串",
             ask(PORT, b"GET /peer HTTP/1.1" + CRLF + b"Host: x" + END))

        # 处理函数里报错 = 整个服务器一红就停；库不兜程序的错，红的那一行落在程序里
        bad = os.path.join(tmp, "bad.dr")
        open(bad, "w", encoding="utf-8").write(
            'fn handle(req, peer) { split(req, "\\x0D\\x0A")[9] }\nhttp_serve()\n')
        q = boot(bad, PORT + 1, tmp)
        show("处理函数里报错：整个服务器一红就停（第一个响应发不出去）",
             ask(PORT + 1, b"GET / HTTP/1.1" + CRLF + b"Host: x" + END))
        print("  [服务器的 stderr] %s" % q.stderr.read().strip())
        print("  [退出码] %d" % q.wait(timeout=12))

        # 程序绕过库自己去 host：句柄不是宿主给过的，红在 E14 上，库什么都没做
        rogue = os.path.join(tmp, "rogue.dr")
        open(rogue, "w", encoding="utf-8").write(
            'fn handle(req, peer) { host("send", "这个句柄是我自己编的\\nx") http_reply(200, "ok") }\n'
            'http_serve()\n')
        r = boot(rogue, PORT + 2, tmp)
        show("程序拿自己编的句柄去 send = E14（响应一个字节都没发出去）",
             ask(PORT + 2, b"GET / HTTP/1.1" + CRLF + b"Host: x" + END))
        print("  [服务器的 stderr] %s" % r.stderr.read().strip())
        print("  [退出码] %d" % r.wait(timeout=12))

        # 程序交回来的不是 [状态, 正文]：红的那一行落在库里 —— 分层分对了的证据
        loose = os.path.join(tmp, "loose.dr")
        open(loose, "w", encoding="utf-8").write(
            'fn handle(req, peer) { 200 }\nhttp_serve()\n')
        u = boot(loose, PORT + 3, tmp)
        show("程序交回的不是 [状态, 正文]：红在库的那一句上（行号落在 lib/http.dr）",
             ask(PORT + 3, b"GET / HTTP/1.1" + CRLF + b"Host: x" + END))
        print("  [服务器的 stderr] %s" % u.stderr.read().strip())
        print("  [退出码] %d" % u.wait(timeout=12))
    finally:
        if srv:
            srv.kill()
        shutil.rmtree(tmp, ignore_errors=True)
    return 0


sys.exit(main())
