# 只做测试客户端：裸 socket。连不上就重试（等宿主把监听起来）。
import socket, sys, time
port = int(sys.argv[1])
payload = b"GET /path \xc3\xa9 x\r\n"          # 16 个字节，其中两个 >127
s = None
for _ in range(80):
    try:
        s = socket.create_connection(("127.0.0.1", port), timeout=15)
        break
    except OSError:
        time.sleep(0.1)
if s is None:
    print("client: 连不上"); sys.exit(2)
s.sendall(payload)
s.shutdown(socket.SHUT_WR)                      # 写完半关，让宿主第二次 recv 看到「断」
data = b""
while True:
    b = s.recv(4096)
    if not b:
        break
    data += b
s.close()
print("client got   :", repr(data))
print("client sent  :", len(payload), repr(payload))
print("byte-exact   :", data == b"DROP/1 " + payload)
