#!/usr/bin/env python3
"""方案 B 的最小可行原型：从手写 C 里抽外部面，和一份清单（manifest）对账。

抽三类：路由（method+path）、成功状态码（reply(out, N) / out->status = N）、
环境变量名（getenv / cfg_get / cfg_num 的字面量首参）。
用法：surface-c.py <service.c> <manifest.json>
退出码：0 = 对得上；1 = 有出入（打印出来）。
"""
import json
import re
import sys

src = open(sys.argv[1], encoding="utf-8").read()
manifest = json.load(open(sys.argv[2], encoding="utf-8"))

routes = re.findall(r'IS\(rq->method,\s*"([A-Z]+)"\)\s*&&\s*IS\(rq->path,\s*"([^"]+)"\)', src)
statuses = [int(x) for x in re.findall(r'reply(?:_body)?\(out,\s*(\d+)', src)]
statuses += [int(x) for x in re.findall(r'out->status\s*=\s*(\d+)', src)]
envs = re.findall(r'(?:getenv|cfg_get|cfg_num)\(\s*"([A-Za-z_][A-Za-z0-9_]*)"', src)

actual = {
    "routes": sorted(set("%s %s" % r for r in routes)),
    "statuses": sorted(set(statuses)),
    "envs": sorted(set(envs)),
}

want = {
    "routes": sorted(manifest.get("routes", [])),
    "statuses": sorted(manifest.get("statuses", [])),
    "envs": sorted(manifest.get("envs", [])),
}

bad = 0
for k in ("routes", "statuses", "envs"):
    only_actual = [x for x in actual[k] if x not in want[k]]
    only_want = [x for x in want[k] if x not in actual[k]]
    if only_actual or only_want:
        bad += 1
        print("对不上 [%s]：代码里有、清单没写 %s；清单写了、代码里没有 %s"
              % (k, only_actual or "（无）", only_want or "（无）"))
    else:
        print("对得上 [%s]：%d 项" % (k, len(actual[k])))

print(json.dumps(actual, ensure_ascii=False))
sys.exit(1 if bad else 0)
