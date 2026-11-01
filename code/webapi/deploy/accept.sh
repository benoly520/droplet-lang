#!/bin/sh
# 语言介质这一侧的入口（v0.33 起只剩"把二进制弄出来"）。
#
# 为什么这么薄：这张仓库原本有两套验收 —— 这一支打 17 行结果（没有期望值，红不红要人眼判），
# `code/sup/accept-sup.sh` 打 21 条断言。两种介质各维护一张表不可持续（P18 第 3 条），
# 现在断言表只有一份：`accept-table.sh`（24 条），**两种介质跑同一张**。
# 需要分支判断的地方就是问题所在：如果这张表对某一侧要改期望，那改的是设计，不是脚本。
#
# 数据目录：每次在 .build/accept-dlt 重建一个临时 git 仓库，跑完留在那里供翻看。
# 用法：sh code/webapi/deploy/accept.sh [端口]（CI 不带参数 = 18799）
set -eu

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$HERE/../../.." && pwd)
PORT=${1:-18799}

B=$ROOT/.build/m4
export TMPDIR=$ROOT/.build
export GOCACHE=$TMPDIR/gocache
rm -rf "$B"
mkdir -p "$B"

cd "$ROOT/code/lang"
go build -o "$B/droplet" ./cmd/droplet
"$B/droplet" build ../webapi/src/contract.dlt ../webapi/src/main.dlt -o "$B/blobd"

exec sh "$HERE/accept-table.sh" "$B/blobd" "$PORT" dlt
