# droplet-lang

**The language is `drop/`. Its entire definition is `drop/spec.txt` — 135 lines, 1,497 tokens.**
Everything else in this repository is either a gate for that page or a ruler for what it produces.

The project goal is the process itself: learning how a language gets built from zero, under four
requirements — a minimal API, legible enough that an AI can use it after reading the docs and the
source, a decided compilation target, and the smallest possible runtime (CPU / GPU / memory).

## Where things are

```
drop/spec.txt                  the definition — one page, nothing else is normative
drop/hosts/python/             host #1, the reference implementation (1,192 lines)
drop/hosts/csharp/             host #2, written from that page alone by another author (1,692 lines)
drop/lib/http.dr               a 170-line HTTP server written *in* drop (the language does not know HTTP)
drop/examples/  drop/tests/    programs and corpus (outputs are byte-for-byte expectations)
drop/check.sh                  gate: 5 segments + 18 sabotage probes, one exit code
drop/conform.sh                the two hosts reconciled: corpus, error samples, live socket transcript
drop/api.json  drop/api_check.py   the machine-readable API table (14 codes / 8 builtins with arity /
                               7 host commands / 22 reserved names / exit 0-1-2), reconciled three ways
drop/codegen/dropc.c           the compiler (741 lines of C): emits a bare x86-64 ELF, no libc
docs/DROP.md                   the language's history and its judges
docs/BUILD.md                  the from-zero route: ten stages, each with a runnable gate
docs/NEXT.md                   handoff: where we are, what is next
tools/                         footprint.py (the ruler), rssshim.c (the honest RSS ruler), codegen_check.py
```

`drop/` does not read anything outside itself. That is not a promise in prose: the first gate segment
greps for it, and sabotage probe Z11 proves the grep can go red.

## Five minutes

```sh
sh drop/check.sh                                                  # one command, one exit code
python3 drop/hosts/python/interpreter.py drop/examples/fact.dr    # 1 / 3628800 / 大  — no compile step
sh drop/conform.sh                                                # two hosts, one definition (needs dotnet)

cc -O2 -o dropc drop/codegen/dropc.c                              # the other road: build the compiler,
./dropc drop/examples/fact.dr -o fact && ./fact                   # then a bare ELF, same output
python3 tools/codegen_check.py                                    # compiler check + the ruler
```

Needs `cc`, `sh`, `python3`, `git`, `curl`. Go is not needed; neither is `as`, `ld`, or a bytecode VM.

## What is actually checked

The gate is not the compiler's own opinion. `tools/codegen_check.py` builds 4 programs (including
`examples/fact.dr` unmodified) and requires the machine code's output to equal the reference host's
**byte for byte**; 3 out-of-subset programs must each be refused at line 2 with exit code 2 and a
reason that matches. `readelf -lW` must show no INTERP segment and `ldd` must call it
"not a dynamic executable".

Measured with `tools/rssshim.c` (fork + execvp + wait4, its own floor is 1,324 KB):

| | disk | resident | start → exit |
|---|---|---|---|
| `fact.dr`, compiled | 643 B | 332–336 KB | 0.46 ms |
| `fact.dr`, reference host | — | 14,184 KB | 25.9 ms |
| 250k tail calls, compiled | 545 B | 332–336 KB | 2.30 ms |
| 250k tail calls, reference host | — | 14,248 KB | 1,035 ms |

The compiler lays out the bytes and writes the ELF header itself: `_start`, then `write`(1) and
`exit`(60) and nothing else. Two syscall numbers have been stable from Linux 3.x to 7.0, so the
product depends on that frozen table — not on glibc, and not on the host language.

## History

An earlier, unrelated line (a C service plus a requirement-registry format) lived in this repository
and was **deleted in full on 2026-11-18**: it was not this language, and having both side by side made
it hard to tell which directory held the source. It is still in `git log`, in the commit before the
deletion. `docs/DROP.md` and `docs/BUILD.md` keep their dated records and say so where they mention it.
