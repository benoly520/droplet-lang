#!/bin/sh
# 把「定义没写清、我这台宿主实际怎么定的」那些形状逐个跑一遍，输出留档。
D=$(cd "$(dirname "$0")/.." && pwd)/bin/Release/net8.0/droplet.dll
for f in "$(dirname "$0")/shapes/"*.dr; do
  printf "%-22s" "$(basename "$f" .dr)"
  timeout 20 dotnet "$D" "$f" 2>&1 | tr '\n' '~'
  echo
done
