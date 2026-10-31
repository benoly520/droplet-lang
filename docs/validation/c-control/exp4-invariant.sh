#!/bin/sh
# 7.4：同一 key 连 POST 两次。契约里的 invariant 写的是"POST 只在 key 不存在时成功"。
# 实测：语言版 201 / 201（违反），手写 C 对照组 201 / 409（守住）。
# 三层都没有看见它：invariant 是散文不进 oracle；boundary 的 example 不被收集；
# 19 条 wire 断言里没有"同一个 key POST 两次"。
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
S=$ROOT/docs/validation/c-control
A=$ROOT/code/lang/internal/runtime/assets
B=$ROOT/.build/c-control/inv
export TMPDIR=$ROOT/.build

[ -x "$ROOT/.build/droplet" ] || (cd "$ROOT/code/lang" && go build -o "$ROOT/.build/droplet" ./cmd/droplet)
"$ROOT/.build/droplet" build "$ROOT/code/webapi/src/contract.dlt" "$ROOT/code/webapi/src/main.dlt" \
  -o "$ROOT/.build/blobd-lang" >/dev/null
cc -std=c11 -O2 -Wall -Wextra -Werror -I"$S" -I"$A" \
   "$S/service2.c" "$A/rt_core.c" "$A/rt_http.c" "$A/rt_blob.c" "$A/rt_ver.c" "$S/decisions.c" \
   -o "$ROOT/.build/blobd-c2" 2>/dev/null

probe() { # probe <标签> <端口> <二进制>
  L=$1; P=$2; BIN=$3
  D=$B/$L
  rm -rf "$D"; mkdir -p "$D/data"
  (cd "$D/data" && git init -q .)
  env TMPDIR=$ROOT/.build DP_COMMIT_DEBOUNCE=0 APP_BIND=127.0.0.1 \
      APP_ROOT="$D/data" APP_TOKEN=secret APP_PORT=$P \
      nohup "$BIN" 2>"$D/log" >/dev/null &
  Srv=$!
  sleep 0.6
  U=http://127.0.0.1:$P
  A2='Authorization: Bearer secret'
  C=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "$A2" "$U/v1/keys/alice")
  D2=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "$A2" "$U/v1/keys/alice")
  printf '%-10s 第一次=%s  第二次（key 已存在）=%s   期望：409\n' "$L" "$C" "$D2"
  kill $Srv 2>/dev/null || true
  wait $Srv 2>/dev/null || true
}

probe lang 18825 "$ROOT/.build/blobd-lang"
probe c2   18826 "$ROOT/.build/blobd-c2"
