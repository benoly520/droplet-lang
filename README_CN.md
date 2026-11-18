# droplet-lang

**语言就是 `drop/`。它的全部定义是 `drop/spec.txt` —— 135 行、1,497 token。**
仓库里别的东西，要么是这一页的门，要么是量它产物的尺。

项目目标是过程本身：学一门语言怎么从 0 被造出来。四条需求 —— API 极简、AI 读完文档和源码就会用、
编译成什么必须想清、运行时 CPU·GPU·内存越小越好。

## 东西都在哪

```
drop/spec.txt                  定义 —— 只有这一页是规范
drop/hosts/python/             第一宿主，参考实现（1,192 行）
drop/hosts/csharp/             第二宿主，另一个人只读那一页从零写的（1,692 行）
drop/lib/http.dr               用 drop 自己写的 170 行 HTTP 服务器（语言本身不认识 HTTP）
drop/examples/  drop/tests/    程序与语料（期望输出是逐字节的）
drop/check.sh                  门：五段 + 十八条破坏，一个退出码
drop/conform.sh                两份宿主对账：语料、报错样本、真 socket 实录
drop/api.json  drop/api_check.py   机器可读的 API 表（14 码 / 8 内置带 arity / 7 宿主命令 /
                               22 保留名 / 退出码 0·1·2），三向对账
drop/codegen/dropc.c           编译器（741 行 C）：吐裸 x86-64 ELF，没有 libc
docs/DROP.md                   这门语言的历史与它挨过的判据
docs/BUILD.md                  从 0 建一门语言的路线：十阶段，每段都有能跑的判据
docs/NEXT.md                   交接：现在在哪、下一步做什么
tools/                         footprint.py（尺）、rssshim.c（诚实的 RSS 尺）、codegen_check.py
```

`drop/` 不读外面的任何文件。这不是文里的一句承诺：第一段门就是那条 grep，破坏 Z11 证明它真的会红。

## 五分钟上手

```sh
sh drop/check.sh                                                  # 一条命令，一个退出码
python3 drop/hosts/python/interpreter.py drop/examples/fact.dr    # 1 / 3628800 / 大 —— 没有编译这一步
sh drop/conform.sh                                                # 两份宿主一份定义（要 dotnet）

cc -O2 -o dropc drop/codegen/dropc.c                              # 另一条路：先编出编译器本体，
./dropc drop/examples/fact.dr -o fact && ./fact                   # 再编成裸 ELF，同样的输出
python3 tools/codegen_check.py                                    # 编译器对账 + 量尺
```

需要 `cc`、`sh`、`python3`、`git`、`curl`。不需要 Go，也不需要 `as`、`ld`、任何字节码 VM。

## 真正被查的是什么

判据不是编译器自己说好。`tools/codegen_check.py` 编 4 份程序（含 `examples/fact.dr` 原样），
要求机器码的输出与参考实现**逐字节相同**；3 份子集外程序必须各自拒绝在第 2 行、退出码 2、理由对得上。
`readelf -lW` 里必须没有 INTERP 段，`ldd` 必须说 not a dynamic executable。

用 `tools/rssshim.c` 量（fork + execvp + wait4，它自己的地板是 1,324 KB）：

| | 磁盘 | 常驻 | 启动→退出 |
|---|---|---|---|
| `fact.dr` 编译产物 | 643 B | 332–336 KB | 0.46 ms |
| `fact.dr` 参考实现 | — | 14,184 KB | 25.9 ms |
| 25 万次尾递归 编译产物 | 545 B | 332–336 KB | 2.30 ms |
| 25 万次尾递归 参考实现 | — | 14,248 KB | 1,035 ms |

编译器自己排字节、自己写 ELF 头：入口 `_start`，之后只有 `write`(1) 和 `exit`(60)，没有别的。
这两个系统调用号从 Linux 3.x 到 7.0 没变过，所以产物只依赖那张冻住的表 —— 不依赖 glibc，也不依赖宿主语言。

## 历史

更早还有一条不相干的线（一套 C 服务 + 一门需求登记格式），**2026-11-18 整条删除**：
它不是这门语言，两份东西摆在一起，让人分不清哪个目录才是源码。它还留在 `git log` 里（删除那次提交之前）。
`docs/DROP.md` 与 `docs/BUILD.md` 保留了带日期的记录，提到它时会说明已经删掉。
