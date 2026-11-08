import socket, sys, time
port = int(sys.argv[1])
for _ in range(80):
    try:
        s = socket.create_connection(("127.0.0.1", port), timeout=10); s.close()
        print("client: 连上就关"); break
    except OSError:
        time.sleep(0.1)
else:
    print("client: 连不上"); sys.exit(2)
