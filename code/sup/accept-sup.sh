#!/bin/sh
# 超集介质这一侧的入口（v0.33 起只剩薄壳）。
#
# 断言表搬到 `code/webapi/deploy/accept-table.sh`（24 条），两种介质跑同一张 ——
# 原先这里是 21 条、`code/webapi/deploy/accept.sh` 是 17 行打印，两介质各维护一张表
# 正是 P18 第 3 条要结束的状态。合并时一条都没丢：见 accept-table.sh 头部记的三条口径。
#
# CLI 与调用方（CI 的 sup job、sabotage S6、手跑）保持不变。
# 用法：sh code/sup/accept-sup.sh <二进制绝对路径> <端口> <标签>
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)

if [ "$#" -ne 3 ]; then
  echo "用法：sh code/sup/accept-sup.sh <二进制绝对路径> <端口> <标签>" >&2
  exit 2
fi

exec sh "$ROOT/code/webapi/deploy/accept-table.sh" "$1" "$2" "$3"
