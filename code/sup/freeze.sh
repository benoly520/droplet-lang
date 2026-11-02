#!/bin/sh
# 题面冻结：把“AI 要实现的题”钉成哈希，改了就红。
#
# 为什么需要：只验结果的前提是**题不能动**。如果提交者（或维护者顺手）改了验收表、
# 改了声明头、改了契约里的目标，那么“全绿”可能只是把题改简单了。这里把题面的
# 十一份文件（题面 6 + 译稿 5）+ 编译产物里的四张表（--contract）钉进 req/FROZEN：
#
#   * 提交口（submit-sup.sh）每次先核对 FROZEN，不一致 -> 以“题被改过”拒绝；
#   * check.sh 的快速回路也核对，防止题在作者不知情时漂移；
#   * 只有人类显式跑 --update 才能换题 —— 换题是一个动作，不是一次顺手的编辑。
#
# 口径：契约哈希取 `blobd --contract` 的原始输出。它只由声明表决定，与实现写法无关，
# 所以“只改 dp_decide.c 的函数体”不会撞上这堵墙；改表才会。
#
# 用法：
#   sh code/sup/freeze.sh --check  [二进制]   默认动作
#   sh code/sup/freeze.sh --update [二进制]
#   不给二进制时依次找 .build/check/blobd、.build/sup/blobd，都没有就先构建。
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$ROOT"

MODE=check
BIN=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --check) MODE=check; shift ;;
    --update) MODE=update; shift ;;
    *) BIN=$1; shift ;;
  esac
done

FILES='code/webapi/deploy/accept-table.sh
code/sup/dp_decide.h
code/sup/dp_env.h
code/sup/dp_sup.h
code/sup/dp_conf.h
code/runtime/rt.h
req/SRC.md
req/REQ.tsv
req/TRANS.tsv
req/AMBIG.tsv
req/GAP.tsv'
FROZEN=req/FROZEN

if [ -z "$BIN" ]; then
  for C in .build/check/blobd .build/sup/blobd; do
    if [ -x "$C" ]; then BIN=$C; break; fi
  done
fi
if [ -z "$BIN" ]; then
  sh code/sup/build.sh freeze >/dev/null
  BIN=.build/freeze/blobd
fi
if [ ! -x "$BIN" ]; then
  echo "冻结：找不到二进制 $BIN（先跑 sh code/sup/build.sh）" >&2
  exit 2
fi

hash_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

hash_stdin() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | awk '{print $1}'
  else
    shasum -a 256 | awk '{print $1}'
  fi
}

contract_hash() {
  "$BIN" --contract | hash_stdin
}

if [ "$MODE" = "update" ]; then
  mkdir -p req
  TMP=$FROZEN.tmp.$$
  {
    echo "# Droplet 题面冻结清单（由 code/sup/freeze.sh --update 生成）"
    echo "# 一行一项：路径 <TAB> sha256。contract.json 是 \`blobd --contract\` 的原始输出哈希。"
    echo "# 改动其中任意一项，submit 口都会以“题被改过”拒绝，直到人类重跑 --update。"
    for F in $FILES; do
      printf '%s\t%s\n' "$F" "$(hash_file "$F")"
    done
    printf '%s\t%s\n' contract.json "$(contract_hash)"
  } >"$TMP"
  mv "$TMP" "$FROZEN"
  echo "题面冻结已更新：$(printf '%s\n' "$FILES" | grep -c .) 份文件 + 契约 -> $FROZEN"
  exit 0
fi

if [ ! -f "$FROZEN" ]; then
  echo "冻结红：没有 $FROZEN —— 题面没有被钉住，任何人都能改题再交" >&2
  echo "  人类确认过题面后跑：sh code/sup/freeze.sh --update" >&2
  exit 1
fi

BAD=0
N=0
for F in $FILES; do
  N=$((N + 1))
  WANT=$(awk -F '\t' -v p="$F" '$1 == p {print $2}' "$FROZEN")
  if [ -z "$WANT" ]; then
    echo "冻结红：清单里缺 $F" >&2
    BAD=1
    continue
  fi
  GOT=$(hash_file "$F")
  if [ "$WANT" != "$GOT" ]; then
    echo "冻结红：$F 被改过（题被改过，不是实现漂移）" >&2
    echo "  冻结时 $WANT" >&2
    echo "  现在   $GOT" >&2
    BAD=1
  fi
done
N=$((N + 1))
WANT=$(awk -F '\t' '$1 == "contract.json" {print $2}' "$FROZEN")
if [ -z "$WANT" ]; then
  echo "冻结红：清单里缺 contract.json（四张声明表）" >&2
  BAD=1
else
  GOT=$(contract_hash)
  if [ "$WANT" != "$GOT" ]; then
    echo "冻结红：contract.json 被改过 —— 路由/配置/目标/样例哪张表动了" >&2
    echo "  冻结时 $WANT" >&2
    echo "  现在   $GOT" >&2
    BAD=1
  fi
fi

# 清单里多出来的未知项也算红：冻结清单本身不能成为藏东西的地方。
EXTRA=""
for P in $(awk -F '\t' '!/^#/ && NF >= 2 {print $1}' "$FROZEN"); do
  case "$P" in
    contract.json) ;;
    *)
      FOUND=0
      for F in $FILES; do
        if [ "$P" = "$F" ]; then FOUND=1; fi
      done
      if [ "$FOUND" -eq 0 ]; then EXTRA="$EXTRA $P"; fi
      ;;
  esac
done
if [ -n "$EXTRA" ]; then
  echo "冻结红：清单里有未知项：$EXTRA" >&2
  BAD=1
fi

if [ "$BAD" -ne 0 ]; then
  echo "冻结：题面与 FROZEN 不一致 —— 提交口会拒绝；人类确认后跑 --update" >&2
  exit 1
fi
echo "题面冻结：$N 项一致（验收表 / 声明头 5 份 / 需求译稿 5 份 / 契约）"
