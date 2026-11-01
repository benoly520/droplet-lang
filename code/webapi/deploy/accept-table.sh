#!/bin/sh
# 一张表跑两种介质（v0.33 / P18 第 3 条）：24 条断言，没有一条是打印。
#
# 为什么要合并：`.dlt` 那支 accept.sh 原来是 **17 行打印** —— 打印没有期望值，"红不红"要人眼判；
# 超集那支 accept-sup.sh 是 **21 条断言**。两种介质各维护一张验收表不可持续，这正是 P18 的起点。
# 合并之后：这张表只有一支脚本，两种介质都调它；各自剩下的只是"怎么把二进制弄出来"的薄壳。
#
# 三条口径，合并时一条都不许丢：
#   1) 每条断言带期望值，退出码就是结论（最后 [ "$FAIL" -eq 0 ]）；
#   2) 原来只是打印的检查全部转成断言：快照、提交、清单条目数 -> #22/#23/#24。
#      #22 顺手把 DP_GOALS 里一直挂"未证"的 snapshot_before_write 变成有断言撑着的那条；
#   3) 必须证明观测的是**这一版刚起起来的进程**：端口被上一轮残留实例占着时请求照样有响应，
#      但测的不是这份代码（v0.13 实踩过）。所以先 kill -0，再谈结果。
#
# 用法：sh code/webapi/deploy/accept-table.sh <二进制绝对路径> <端口> <标签>
# 两种介质都跑这一支；不要在这里加分支判断 —— 需要分支说明表里出现了介质差异，那是设计问题。
set -eu

# 注意 ../../../ —— 这支脚本在 code/webapi/deploy/ 下，比 code/sup/ 深一层。
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
BIN=$1
PORT=$2
LABEL=$3

B=$ROOT/.build/accept-$LABEL
export TMPDIR=$ROOT/.build
rm -rf "$B"
mkdir -p "$B/data"

cd "$B/data"
git init -q .
# 只提交本次写的那个 key：扫全树会撞上正在 rename 的 .tmp.<pid>.<seq>。
# **并发**：运行时每次成功写入都拉起一个子进程（DP_COMMIT_DEBOUNCE=0 不合并），所以这条命令会被
# 同时跑好几份。直接跑 `git commit` 会抢 HEAD 的 ref 锁 —— CI 第一次跑 v0.33 就挂在这一条上
# （`fatal: cannot lock ref 'HEAD'`，4 次写入只落地 3 条提交）。用 flock 把命令串起来执行；
# 这不是放宽 #23，它仍然是"一次成功写入 = 一条提交"，只是让子进程排队而不是互相踩。
cat > "$B/commit.sh" <<'EOS'
#!/bin/sh
git add -A -- "$DP_COMMIT_KEY" &&
git -c user.name=droplet -c user.email=dp@example.com commit -q -m "snapshot $DP_COMMIT_KEY"
EOS
DP_COMMIT_CMD="flock \"$B/commit.lock\" sh \"$B/commit.sh\""
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

# ---- 从 accept.sh 的打印转成断言的三条（v0.33 合并时新增）----
# 提交子进程是异步的：轮询等它落盘（最多 10 秒；CI 的 runner 比本机慢，3 秒实测不够），
# 否则 #23 数到的是写入过程中的中间态。
# 等待不等于放宽：等到之后仍然断言**恰好** 4 条，多一条少一条都算挂。
COMMON_WAIT=0
COMMITS=0
while [ "$COMMON_WAIT" -lt 20 ]; do
  COMMITS=$(git -C "$B/data" rev-list --count HEAD 2>/dev/null || echo 0)
  [ "$COMMITS" -eq 4 ] && break
  sleep 0.5
  COMMON_WAIT=$((COMMON_WAIT + 1))
done

SNAPS=$(ls "$B/data/.snapshots" 2>/dev/null | grep -c '^demo\.' || true)
ck 22 3 "$SNAPS" "覆盖写之前留有上一版本快照（DP_GOALS 里的 snapshot_before_write 靠这条）"

ck 23 4 "$COMMITS" "4 次成功写入 == 4 条提交：DP_COMMIT_CMD 那个子进程真的在跑"

LISTBODY=$(body -H "$A" "$KEYS")
KEYN=$(printf '%s' "$LISTBODY" | tr ',' '\n' | grep -c '"' || true)
ck 24 3 "$KEYN" "清单来自目录本身（demo + 两次 POST = 3 个 key），不来自任何注册表文件"

echo "$LABEL: $PASS 过 / $FAIL 挂"
if [ "$FAIL" -ne 0 ]; then
  # 只在挂的时候贴 srv.log —— 绿的时候贴日志是把噪声当证据。
  echo "--- srv.log ---" >&2
  cat "$B/srv.log" >&2
fi
[ "$FAIL" -eq 0 ]
