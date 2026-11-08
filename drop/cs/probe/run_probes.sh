#!/bin/sh
# 每条样本跑一遍：记下 stderr 实际报的码、行号、退出码、stdout 字节数。
# 这是干净房间里那份自证清单，搬进仓库后路径改成从脚本自己所在的位置算：
# 它跑的是 cs/bin 里那份构建产物，跟 conform.sh 用的是同一个 dll。
HERE=$(cd "$(dirname "$0")" && pwd)
DLL="$HERE/../bin/Release/net8.0/droplet.dll"
P="$HERE"
check() { # 名字 期望码 期望rc
  b=$1; want=$2; wrc=$3
  so="$P/.$b.stdout"; se="$P/.$b.stderr"
  shift 3
  dotnet "$DLL" "$@" >"$so" 2>"$se"; rc=$?
  line=$(sed -n '1p' "$se")
  got=$(printf '%s' "$line" | sed -n 's/^挂：第 \([0-9]*\) 行 \(E[0-9]*\) —— .*/\2/p')
  gln=$(printf '%s' "$line" | sed -n 's/^挂：第 \([0-9]*\) 行 \(E[0-9]*\) —— .*/\1/p')
  if [ "$want" = "NONE" ]; then mark="ok  "; [ "$rc" = "0" ] || mark="BAD "
  else mark="ok  "; { [ "$got" = "$want" ] && [ "$rc" = "$wrc" ]; } || mark="BAD "
  fi
  printf "%s %-9s 期望%-5s 实报%-5s 行%-4s rc=%s stdout=%sB  %s\n" "$mark" "$b" "$want" \
    "${got:-无}" "${gln:--}" "$rc" "$(wc -c <"$so" | tr -d ' ')" \
    "$(printf '%s' "$line" | sed 's/^挂：[^—]*—— //')"
  [ -s "$so" ] && printf "        stdout: %s\n" "$(sed -n '1,3p' "$so" | tr '\n' '~')"
}
check e01 E1 1 "$P/e01.dr"
check e02 E2 1 "$P/e02.dr"
check e03 E3 1 "$P/e03.dr"
check e03b E3 1 "$P/e03b.dr"
check e03c E3 1 "$P/e03c.dr"
check e03d E3 1 "$P/e03d.dr"
check e03e E3 1 "$P/e03e.dr"
check e03f E3 1 "$P/e03f.dr"
check e03g E3 1 "$P/e03g.dr"
check e03h E3 1 "$P/e03h.dr"
check e04 E4 1 "$P/e04.dr"
check e05 E5 1 "$P/e05.dr"
check e05b E5 1 "$P/e05b.dr"
check e05c E5 1 "$P/e05c.dr"
check e06 E6 1 "$P/e06.dr"
check e06b E6 1 "$P/e06b.dr"
check e07 E7 1 "$P/e07.dr"
check e07b E7 1 "$P/e07b.dr"
check e08 E8 1 "$P/e08.dr"
check e09 E9 1 "$P/e09.dr"
check e10 E10 1 "$P/e10.dr"
check e10b E10 1 "$P/e10b.dr"
check e11 E11 1 "$P/e11.dr"
check e11b E11 1 "$P/e11b.dr"
check e12 E12 1 "$P/e12.dr"
check e13 E13 1 "$P/e13.dr"
check e13ok NONE 0 "$P/e13ok.dr"
check e13tail NONE 0 "$P/e13tail.dr"
check e14 E14 1 "$P/e14.dr"
check e14b E14 1 "$P/e14b.dr"
