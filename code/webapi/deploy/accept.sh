#!/bin/sh
# M4 契约验收：五接口 + 412 + 快照 + 版本化提交。
# 不碰服务真实数据目录：每次在 .build/m4 重建一个临时 git 仓库，跑完留在那里供翻看。
set -eu

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$HERE/../../.." && pwd)

B=$ROOT/.build/m4
export TMPDIR=$ROOT/.build
export GOCACHE=$TMPDIR/gocache
rm -rf "$B"
mkdir -p "$B/data"

cd "$ROOT/code/lang"
go build -o "$B/droplet" ./cmd/droplet
"$B/droplet" build ../webapi/src/contract.dlt ../webapi/src/main.dlt -o "$B/blobd"

cd "$B/data"
git init -q .
# 只提交本次写的那个 key：扫全树会撞上正在 rename 的 .tmp.<pid>.<seq>。
DP_COMMIT_CMD='git add -A -- "$DP_COMMIT_KEY" && git -c user.name=droplet -c user.email=dp@example.com commit -q -m "snapshot $DP_COMMIT_KEY"'
export DP_COMMIT_CMD
export DP_COMMIT_DEBOUNCE=0
export DP_BIND=127.0.0.1 DP_PORT=18799 DP_ROOT="$B/data" DP_TOKEN=secret

"$B/blobd" 2>"$B/srv.log" &
SRV=$!
trap 'kill $SRV 2>/dev/null || true' EXIT
sleep 0.4

U=http://127.0.0.1:18799
A='Authorization: Bearer secret'

code() { curl -s -o /dev/null -w '%{http_code}' "$@"; }
# ETag 由运行时从 body 推导（v0.10）：handler 一个字也不写，所以这里只看响应头。
tag()  { curl -s -o /dev/null -D - "$@" | tr -d '\r' \
  | awk -F': ' 'tolower($1)=="etag"{t=$2} END{print (t==""?"(none)":t)}'; }
row() { printf '%-34s %s\n' "$1" "$2"; }

row 'GET  /health (无 token)'      "$(code "$U/health")"
row 'GET  /v1/keys (空)'           "$(code -H "$A" "$U/v1/keys")"
row 'PUT  /v1/blob/demo'           "$(code -X PUT -H "$A" --data-binary 'hello' "$U/v1/blob/demo")"

ETAG=$(tag -H "$A" "$U/v1/blob/demo")
row 'GET  /v1/blob/demo (ETag)'    "$ETAG"
row 'PUT  If-Match 命中'           "$(code -X PUT -H "$A" -H "If-Match: $ETAG" --data-binary 'hello2' "$U/v1/blob/demo")"
row 'PUT  If-Match 不符 -> 412'    "$(code -X PUT -H "$A" -H 'If-Match: deadbeef' --data-binary 'x' "$U/v1/blob/demo")"
row 'POST /v1/keys/second'         "$(code -X POST -H "$A" "$U/v1/keys/second")"
row 'GET  /v1/keys (两个)'         "$(curl -s -H "$A" "$U/v1/keys")"
row 'GET  /v1/blob/missing -> 404' "$(code -H "$A" "$U/v1/blob/missing")"
row 'GET  /v1/blob/../evil -> 400' "$(code -H "$A" --path-as-is "$U/v1/blob/../evil")"
row 'DELETE /v1/blob/demo -> 405'  "$(code -X DELETE -H "$A" "$U/v1/blob/demo")"
row 'DELETE 405 empty body'        "$(tag -X DELETE -H "$A" "$U/v1/blob/demo")"

dd if=/dev/zero of="$B/big" bs=1M count=9 2>/dev/null
row 'PUT  9 MiB -> 413'            "$(code -X PUT -H "$A" --data-binary @"$B/big" "$U/v1/blob/big")"

sleep 1
echo
echo '--- 快照 ---'
ls "$B/data/.snapshots" 2>/dev/null || echo '(无)'
echo '--- git log ---'
git -C "$B/data" log --oneline 2>/dev/null || echo '(无提交)'
echo '--- 服务端日志 ---'
cat "$B/srv.log"
