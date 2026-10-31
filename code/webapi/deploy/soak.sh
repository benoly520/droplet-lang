#!/bin/sh
# M4 长稳观测：每轮 PUT + GET 校验内容，定时采样 VmRSS / 线程数 / 子进程数，
# 退出时对 (时间, RSS) 做最小二乘，斜率超阈值即判失败。
#
#   用法：sh code/webapi/deploy/soak.sh [小时]        # 默认 72
#   环境：
#     SOAK_INTERVAL   每轮间隔秒，默认 30（判据口径）
#     SOAK_HOURS      覆盖小时数
#     SOAK_PORT       自建实例的端口，默认 18800
#     SOAK_URL        测已有实例时给 base URL（此时必须同时给 SOAK_PID）
#     SOAK_PID        要观测的进程 pid（自建模式下脚本自己填）
#     SOAK_MAX_GROWTH_KB_PER_H  RSS 斜率上限，默认 64 KB/h
#     SOAK_SECONDS    直接给秒数（压缩时间自测用，优先于小时数）
#   产物：.build/soak/sample.log（时间 pid rss_kb threads children）
#         .build/soak/fail.log（任何一次状态码或内容不符）
#
# 3 天 = 72 小时 = 8640 轮，日志约 90 行，够事后画图。
set -eu

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$HERE/../../.." && pwd)
OUT=$ROOT/.build/soak

HOURS=${SOAK_HOURS:-${1:-72}}
INTERVAL=${SOAK_INTERVAL:-30}
PORT=${SOAK_PORT:-18800}
MAX_GROWTH=${SOAK_MAX_GROWTH_KB_PER_H:-64}
# 每小时采一次样，换算成轮数（至少 1 轮一采，方便压缩时间自测）。
if [ "$INTERVAL" -gt 0 ]; then
  SAMPLE_EVERY=$(( 3600 / INTERVAL ))
else
  SAMPLE_EVERY=1
fi
if [ "$SAMPLE_EVERY" -lt 1 ]; then SAMPLE_EVERY=1; fi
# 压缩时间自测（SOAK_SECONDS）时按总轮数重新分摊采样：否则"每小时一采"会让一次
# 十分钟的跑动只留下 1 个样本 —— 斜率算不出来，等于没观测。目标 ~60 个样本。
if [ -n "${SOAK_SECONDS:-}" ]; then
  if [ "$INTERVAL" -gt 0 ]; then
    RUN_ROUNDS=$(( SOAK_SECONDS / INTERVAL ))
  else
    RUN_ROUNDS=$SOAK_SECONDS
  fi
  SAMPLE_EVERY=$(( RUN_ROUNDS / 60 ))
  if [ "$SAMPLE_EVERY" -lt 1 ]; then SAMPLE_EVERY=1; fi
fi

mkdir -p "$OUT"
: > "$OUT/sample.log"
: > "$OUT/fail.log"

if [ -n "${SOAK_URL:-}" ]; then
  U=$SOAK_URL
  PID=${SOAK_PID:?SOAK_URL 模式下必须给 SOAK_PID，否则采不到 VmRSS}
  T=${SOAK_TOKEN:-}
else
  export TMPDIR=$ROOT/.build
  export GOCACHE=$TMPDIR/gocache
  rm -rf "$OUT/data"
  mkdir -p "$OUT/data"
  # 长稳要盖到提交子进程这条路，否则只测了 blob 读写。
  git -C "$OUT/data" init -q .
  DP_COMMIT_CMD='git add -A -- "$DP_COMMIT_KEY" && git -c user.name=droplet -c user.email=dp@example.com commit -q -m "soak $DP_COMMIT_KEY"'
  export DP_COMMIT_CMD
  cd "$ROOT/code/lang"
  [ -x "$ROOT/.build/m4/droplet" ] || go build -o "$ROOT/.build/m4/droplet" ./cmd/droplet
  "$ROOT/.build/m4/droplet" build ../webapi/src/contract.dlt ../webapi/src/main.dlt \
    -o "$OUT/blobd" >/dev/null
  DP_COMMIT_DEBOUNCE=${SOAK_DEBOUNCE:-60}
  export DP_COMMIT_DEBOUNCE
  export APP_BIND=127.0.0.1 APP_PORT="$PORT" APP_ROOT="$OUT/data" APP_TOKEN=soak
  "$OUT/blobd" 2>"$OUT/srv.log" &
  PID=$!
  U=http://127.0.0.1:$PORT
  T=soak
  trap 'kill $PID 2>/dev/null || true' EXIT
  sleep 0.4
  # 端口被上一轮残留实例占着时，新实例 bind 失败立刻退出：不先确认它活着，
  # 观测到的是别人家的进程（甚至旧二进制），整轮数据都不可信（v0.10 实踩过）。
  if ! kill -0 "$PID" 2>/dev/null; then
    echo "soak: 自建实例启动即退出（多半是端口 $PORT 被占），srv.log：" >&2
    cat "$OUT/srv.log" >&2
    exit 1
  fi
fi

A="Authorization: Bearer $T"
if [ -n "${SOAK_SECONDS:-}" ]; then
  END=$(( $(date +%s) + SOAK_SECONDS ))
else
  END=$(( $(date +%s) + HOURS * 3600 ))
fi
N=0
FAILS=0

sample() {
  now=$(date +%s)
  rss=$(awk '/^VmRSS:/{print $2}' "/proc/$PID/status" 2>/dev/null || echo 0)
  thr=$(ls "/proc/$PID/task" 2>/dev/null | wc -l | tr -d ' ')
  kid=$(pgrep -P "$PID" 2>/dev/null | wc -l | tr -d ' ')
  printf '%s %s %s %s %s\n' "$now" "$PID" "${rss:-0}" "${thr:-0}" "${kid:-0}" \
    >> "$OUT/sample.log"
}

fail() {
  printf '%s round=%s key=%s %s\n' "$(date +%s)" "$N" "$1" "$2" >> "$OUT/fail.log"
  FAILS=$(( FAILS + 1 ))
}

if [ -n "${SOAK_SECONDS:-}" ]; then
  echo "soak: ${SOAK_SECONDS}s（压缩自测）每 ${INTERVAL}s 一轮，端口 $PORT，pid $PID，样本每 $SAMPLE_EVERY 轮"
else
  echo "soak: ${HOURS}h 每 ${INTERVAL}s 一轮，端口 $PORT，pid $PID，样本每 $SAMPLE_EVERY 轮"
fi
sample

while [ "$(date +%s)" -lt "$END" ]; do
  N=$(( N + 1 ))
  KEY="soak$(( N % 8 ))"
  BODY="round-$N-$(date +%s)"

  code=$(curl -s -o /dev/null -w '%{http_code}' -X PUT -H "$A" \
    --data-binary "$BODY" "$U/v1/blob/$KEY" 2>/dev/null) || code=000
  # put 声明了 @no_content（B3）：每轮的成功码就是 204，不是 200。
  [ "$code" = 204 ] || fail "$KEY" "PUT=$code"

  got=$(curl -s -H "$A" "$U/v1/blob/$KEY" 2>/dev/null) || got=''
  [ "$got" = "$BODY" ] || fail "$KEY" "GET 内容不符"

  if [ $(( N % SAMPLE_EVERY )) -eq 0 ]; then
    sample
    printf 'round=%s rss=%sKB fail=%s\n' "$N" \
      "$(awk 'END{print $3}' "$OUT/sample.log")" "$FAILS"
  fi
  if [ "$INTERVAL" -gt 0 ]; then sleep "$INTERVAL"; fi
done

echo
echo "--- 判定 ---"
echo "轮次 $N，失败 $FAILS"
awk -v limit="$MAX_GROWTH" '
  { n++; t[n] = $1/3600; y[n] = $3 }
  END {
    if (n < 3) { printf "样本 %d 个，不足以算斜率（跑得越久越准）\n", n; exit 0 }

    # 全窗口斜率：只作参考，永远打出来，不许藏。
    for (i = 1; i <= n; i++) { st += t[i]; sy += y[i]; stt += t[i]*t[i]; sty += t[i]*y[i] }
    den = n*stt - st*st
    kall = (den == 0) ? 0 : (n*sty - st*sy) / den

    # 判据取"去预热后"的斜率：进程刚起时的常驻分配是一次性开销，不是泄漏。
    # 短窗口（压缩自测）里那段爬升会被全窗口线性拟合当成"增长趋势"，判据就只剩
    # 一个方向 —— 见 docs/NEXT.md P0。预热段 = 前 10% 的样本。
    skip = int(n/10); if (skip < 1) skip = 1
    m = 0
    for (i = skip+1; i <= n; i++) { m++; st2 += t[i]; sy2 += y[i]; stt2 += t[i]*t[i]; sty2 += t[i]*y[i] }
    printf "样本 %d 个（前 %d 个视为预热，不进判据），末次 RSS %d KB\n", n, skip, y[n]
    if (m < 3) { printf "结论：样本不足 —— 去预热后只剩 %d 个点，跑久一点\n", m; exit 0 }
    den2 = m*stt2 - st2*st2
    k = (den2 == 0) ? 0 : (m*sty2 - st2*sy2) / den2
    printf "斜率：全窗口 %.1f KB/h，去预热后 %.1f KB/h（判据上限 %s KB/h）\n", kall, k, limit

    # 平台检查与窗口长度无关：最后 10% 样本的极差就是"还在不在长"。
    lo = y[n]; hi = y[n]; cnt = 0
    for (i = n-int(n/10); i <= n; i++) { if (i < 1) continue; cnt++
      if (y[i] < lo) lo = y[i]; if (y[i] > hi) hi = y[i] }
    printf "尾部 %d 个样本：%d–%d KB（极差 %d KB）\n", cnt, lo, hi, hi-lo

    if (k > limit) printf "结论：FAIL —— 去预热后 RSS 仍有增长趋势\n"
    else printf "结论：PASS —— 去预热后斜率低于阈值，尾部稳定在 %d–%d KB\n", lo, hi
  }' "$OUT/sample.log"

# 单线程服务 + SIGCHLD=SIG_IGN：线程数应恒为 1，子进程不该堆积。
awk '($4 > 1) || ($5 > 0) { bad++ } END {
  if (bad) printf "注意：%d 个样本出现线程 >1 或残留子进程，看 sample.log\n", bad
  else printf "线程/子进程：全程 1 线程、无残留子进程\n"
}' "$OUT/sample.log"
