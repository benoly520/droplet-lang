#!/bin/sh
# 超集形态的验收表：对照组的 19 条断言 + 本轮新增 2 条 = 21 条，全部是断言不是打印。
#
#   #20 同一 key 连 POST 两次 = 409  —— P16 要的那条。语言版实测 201/201（SPEC 附录 D.10），
#       这一条进表之后，"POST 只在 key 不存在时成功"不再是散文。
#   #21 GET 回来的字节 == 最近一次成功 PUT 的字节 —— 上一轮的 19 条里**没有**这条，
#       而契约里写着这条 invariant，所以它在两种介质下都一直是未证的。补上。
#
# 用法：sh code/sup/accept-sup.sh <二进制绝对路径> <端口> <标签>
set -eu

BIN=$1
PORT=$2
LABEL=$3

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
B=$ROOT/.build/sup-$LABEL
export TMPDIR=$ROOT/.build
rm -rf "$B"
mkdir -p "$B/data"

cd "$B/data"
git init -q .
DP_COMMIT_CMD='git add -A -- "$DP_COMMIT_KEY" && git -c user.name=droplet -c user.email=dp@example.com commit -q -m "snapshot $DP_COMMIT_KEY"'
export DP_COMMIT_CMD
export DP_COMMIT_DEBOUNCE=0
export APP_BIND=127.0.0.1
export APP_PORT=$PORT
export APP_ROOT="$B/data"
export APP_TOKEN=secret

"$BIN" 2>"$B/srv.log" &
SRV=$!
trap 'kill $SRV 2>/dev/null || true' EXIT
sleep 0.4
if ! kill -0 "$SRV" 2>/dev/null; then
  echo "$LABEL: 启动即退出（端口 $PORT 被占？），srv.log：" >&2
  cat "$B/srv.log" >&2
  exit 1
fi

U=http://127.0.0.1:$PORT
A='Authorization: Bearer secret'
HEALTH=$U/health
KEYS=$U/v1/keys
BLOB=$U/v1/blob/demo
MISS=$U/v1/blob/missing
SEC=$U/v1/keys/second
THIRD=$U/v1/keys/third
EVIL=$U/v1/blob/../evil
BIG=$U/v1/blob/big
PASS=0
FAIL=0

code()   { curl -s -o /dev/null -w '%{http_code}' "$@"; }
nbytes() { curl -s -o /dev/null -w '%{size_download}' "$@"; }
body()   { curl -s "$@"; }
hd()     { k=$1; shift; curl -s -o /dev/null -D - "$@" | tr -d '\r' \
         | awk -F': ' -v k="$k" 'tolower($1)==k{v=$2} END{if(v=="")v="(none)"; print v}'; }

ck() { # ck <编号> <期望> <实际> <说明>
  if [ "$2" = "$3" ]; then
    PASS=$((PASS+1))
    echo "  ok   #$1 $4 -> $3"
  else
    FAIL=$((FAIL+1))
    echo "  FAIL #$1 $4 -> 期望 $2，实际 $3"
  fi
}

CK1=$(code "$HEALTH")
ck 1 200 "$CK1" "GET /health 无 token 也放行"

CK2=$(code -H "$A" "$KEYS")
ck 2 200 "$CK2" "GET /v1/keys 空清单"

CK3=$(code -X PUT -H "$A" --data-binary hello "$BLOB")
ck 3 204 "$CK3" "PUT 不带 If-Match"

ETAG=$(hd etag -H "$A" "$BLOB")
if [ "$ETAG" = "(none)" ]; then CK4=missing; else CK4=present; fi
ck 4 present "$CK4" "GET 发 ETag（运行时从 body 推导）"

CK5=$(code -X PUT -H "$A" -H "If-Match: $ETAG" --data-binary hello2 "$BLOB")
ck 5 204 "$CK5" "PUT If-Match 命中"

CK6=$(code -X PUT -H "$A" -H 'If-Match: deadbeef' --data-binary x "$BLOB")
ck 6 412 "$CK6" "PUT If-Match 不符 -> 412"

CK7=$(nbytes -X PUT -H "$A" -H 'If-Match: deadbeef' --data-binary x "$BLOB")
ck 7 0 "$CK7" "412 空 body"

CK8=$(hd content-length -X PUT -H "$A" --data-binary hello3 "$BLOB")
ck 8 '(none)' "$CK8" "204 不得带 Content-Length"

CK9=$(nbytes -X PUT -H "$A" --data-binary hello4 "$BLOB")
ck 9 0 "$CK9" "204 空 body"

CK10=$(code -X POST -H "$A" "$SEC")
ck 10 201 "$CK10" "POST 新 key -> 201"

CK11=$(code -H "$A" "$KEYS")
ck 11 200 "$CK11" "GET 清单（两个 key）"

CK12=$(hd content-length -X POST -H "$A" "$THIRD")
ck 12 0 "$CK12" "201 带 Content-Length: 0"

CK13=$(code -H "$A" "$MISS")
ck 13 404 "$CK13" "GET 不存在的 key -> 404"

CK14=$(nbytes -H "$A" "$MISS")
ck 14 0 "$CK14" "404 空 body（错误不带表示）"

CK15=$(code -H "$A" --path-as-is "$EVIL")
ck 15 400 "$CK15" "非法 key -> 400，不进 handler"

CK16=$(code -X DELETE -H "$A" "$BLOB")
ck 16 405 "$CK16" "未声明的动词 -> 405"

CK17=$(nbytes -X DELETE -H "$A" "$BLOB")
ck 17 0 "$CK17" "405 空 body"

CK18=$(hd etag -X DELETE -H "$A" "$BLOB")
ck 18 '(none)' "$CK18" "405 无 ETag"

dd if=/dev/zero of="$B/big.bin" bs=1M count=9 2>/dev/null
CK19=$(code -X PUT -H "$A" --data-binary "@$B/big.bin" "$BIG")
ck 19 413 "$CK19" "PUT 9 MiB -> 413"

# ---- 本轮新增的两条 ----

CK20=$(code -X POST -H "$A" "$SEC")
ck 20 409 "$CK20" "同一 key 连 POST 两次 -> 409（P16：语言版这里是 201）"

CK21=$(body -H "$A" "$BLOB")
ck 21 hello4 "$CK21" "GET 回来的字节 == 最近一次成功 PUT 的字节"

echo "$LABEL: $PASS 过 / $FAIL 挂"
[ "$FAIL" -eq 0 ]
