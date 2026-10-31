#!/bin/sh
# exp1：守卫落点。把 If-Match 那 7 行整块删掉，看谁会抱怨。
# 结论（实测）：-Wall -Wextra -Werror 全过，19 条断言只挂 #6，而 #7「412 空 body」空过。
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
S=$ROOT/docs/validation/c-control
A=$ROOT/code/lang/internal/runtime/assets
B=$ROOT/.build/c-control/exp1
mkdir -p "$B"
export TMPDIR=$ROOT/.build

echo "=== 1) C 侧：删掉守卫 ==="
python3 - "$S/service2.c" "$B/service2_noguard.c" <<'PY'
import pathlib, sys
src = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
guard = """  if (rq->if_match.n > 0u) {
    int pc = dp_precondition_check(a, dp_str_from_cstr(env_root), rq->key, rq->if_match);
    if (pc != 0) {
      reply(out, status_of(pc));
      return 0;
    }
  }
"""
assert src.count(guard) == 1
pathlib.Path(sys.argv[2]).write_text(src.replace(guard, ""), encoding="utf-8")
print("已生成 service2_noguard.c（删掉 7 行守卫）")
PY

cc -std=c11 -O2 -Wall -Wextra -Werror -I"$S" -I"$A" \
   "$B/service2_noguard.c" "$A/rt_core.c" "$A/rt_http.c" "$A/rt_blob.c" "$A/rt_ver.c" "$S/decisions.c" \
   -o "$ROOT/.build/blobd-noguard"
echo "编译：-Wall -Wextra -Werror 全部通过（编译器不认识「这条路由需要守卫」这件事）"
sh "$S/accept-c.sh" "$ROOT/.build/blobd-noguard" 18821 noguard

echo
echo "=== 2) droplet 侧：同形变异（从路由声明里删掉 `, precondition`）==="
D=$ROOT/.build/c-control/exp1d
mkdir -p "$D"
cp "$ROOT/code/webapi/src/contract.dlt" "$ROOT/code/webapi/src/main.dlt" "$D/"
python3 - "$D" <<'PY'
import pathlib, sys
p = pathlib.Path(sys.argv[1]) / "contract.dlt"
s = p.read_text(encoding="utf-8")
old = '@put("/v1/blob") @no_content impl blob_put with idempotency, precondition'
assert s.count(old) == 1
p.write_text(s.replace(old, '@put("/v1/blob") @no_content impl blob_put with idempotency'), encoding="utf-8")
print("已从路由删掉 `, precondition`")
PY
set +e
"$ROOT/.build/droplet" check "$D/contract.dlt" "$D/main.dlt"
echo "check 退出码 = $?（非 0 = 编译期就拦住）"
set -e

echo
echo "=== 3) 补测：显式写 without precondition + 理由 ==="
python3 - "$D" <<'PY'
import pathlib, sys
p = pathlib.Path(sys.argv[1]) / "contract.dlt"
s = p.read_text(encoding="utf-8")
s = s.replace('@put("/v1/blob") @no_content impl blob_put with idempotency',
              '@put("/v1/blob") @no_content impl blob_put with idempotency\n    without precondition: "这个服务不做并发保护"', 1)
p.write_text(s, encoding="utf-8")
PY
"$ROOT/.build/droplet" check "$D/contract.dlt" "$D/main.dlt" >/dev/null && echo "check：通过（表态了）"
export TMPDIR=$ROOT/.build
"$ROOT/.build/droplet" build "$D/contract.dlt" "$D/main.dlt" -o "$ROOT/.build/blobd-nopc" >/dev/null
sh "$S/accept-c.sh" "$ROOT/.build/blobd-nopc" 18822 nopc
echo "与 C 无守卫版对照：同样只挂 #6，#7 同样空过 —— 语言的增益不在运行期，在「沉默不能是一个决定」。"
