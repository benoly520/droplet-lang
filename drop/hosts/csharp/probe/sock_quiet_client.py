import socket, sys, time
port = int(sys.argv[1])
s = None
for _ in range(80):
    try:
        s = socket.create_connection(("127.0.0.1", port), timeout=20); break
    except OSError:
        time.sleep(0.1)
if s is None:
    print("client: 连不上"); sys.exit(2)
t0 = time.time()
time.sleep(12)                                  # 一个字节都不发
s.close()
print("client: 连上后沉默 %.1fs 再关" % (time.time() - t0))
