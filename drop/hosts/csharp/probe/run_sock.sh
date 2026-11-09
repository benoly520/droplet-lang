#!/bin/sh
# 七个 host 命令的真 TCP 往返。每个用例：起服务端（后台）→ 起 python 裸 socket 客户端 → 收两侧输出。
HERE=$(cd "$(dirname "$0")" && pwd)
DLL="$HERE/../bin/Release/net8.0/droplet.dll"
P="$HERE"
PORT=${PORT:-45731}

echo "### 1) bind/accept/recv/send/peer/close 一趟往返  (--port $PORT)"
dotnet "$DLL" --port "$PORT" "$P/sock_echo.dr" >"$P/.sock.srv" 2>"$P/.sock.srv.err" &
srvp=$!
python3 "$P/sock_client.py" "$PORT" >"$P/.sock.cli" 2>&1
wait $srvp; rc=$?
echo "服务端 rc=$rc，stdout："
sed 's/^/  | /' "$P/.sock.srv"
[ -s "$P/.sock.srv.err" ] && { echo "  服务端 stderr："; sed 's/^/  ! /' "$P/.sock.srv.err"; }
sed 's/^/  /' "$P/.sock.cli"

echo "### 2) 同一端口第二次 bind"
dotnet "$DLL" --port "$PORT" "$P/sock_bindtwice.dr" >"$P/.bt.out" 2>&1; echo "  rc=$? $(sed -n 1p "$P/.bt.out")"

echo "### 3) recv 拿到的是监听句柄（不是连接句柄）"
dotnet "$DLL" --port "$PORT" "$P/sock_cmd.dr" >"$P/.cm.out" 2>&1; echo "  rc=$? $(sed -n 1p "$P/.cm.out")"

echo "### 4) close 之后再 send"
dotnet "$DLL" --port "$PORT" "$P/sock_closed.dr" >"$P/.cl.out" 2>&1 &
srv2=$!
python3 "$P/sock_poke_client.py" "$PORT" >/dev/null 2>&1
wait $srv2; echo "  rc=$? $(sed -n 1p "$P/.cl.out")"

echo "### 5) 连上但 10 秒没动静 = 断 = 空串（这一条要等十几秒）"
dotnet "$DLL" --port "$PORT" "$P/sock_timeout.dr" >"$P/.to.out" 2>"$P/.to.err" &
srv3=$!
python3 "$P/sock_quiet_client.py" "$PORT" >"$P/.to.cli" 2>&1
wait $srv3; rc=$?
echo "  服务端 rc=$rc"; sed 's/^/  | /' "$P/.to.out"
[ -s "$P/.to.err" ] && sed 's/^/  ! /' "$P/.to.err"
sed 's/^/  /' "$P/.to.cli"
