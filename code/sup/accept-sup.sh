#!/bin/sh
# 超集介质这一侧的入口（v0.33 起只剩薄壳）。
#
# 断言表住在 `code/webapi/deploy/accept-table.sh`（24 条）；这里只做参数检查后 exec。
# CLI：<二进制绝对路径> <端口> <标签>（CI 的 sup job、sabotage S6、手跑都这样调）。
# 用法：sh code/sup/accept-sup.sh <二进制绝对路径> <端口> <标签>
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)

if [ "$#" -ne 3 ]; then
  echo "用法：sh code/sup/accept-sup.sh <二进制绝对路径> <端口> <标签>" >&2
  exit 2
fi

exec sh "$ROOT/code/webapi/deploy/accept-table.sh" "$1" "$2" "$3"
