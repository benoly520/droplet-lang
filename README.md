# Droplet

**A C superset for AI-written services: the source is valid C11, the declarations are tables,
and the checks fail when the implementation drifts from what was declared.**

The division of labour this is built around:

| Actor | Does | Does not |
| --- | --- | --- |
| AI | writes the implementation and the declarations, runs the checks | explain itself, certify itself |
| Compiler / build script / checkers | fail the build or the acceptance run when something is wrong | judge whether the business logic is right |
| Human | accepts the result: does it run, is the table green, does breaking it turn something red | read the implementation line by line |

So the deliverable is not syntax — it is **a chain of failing checks from a declaration to the
running binary**. If you cannot make it fail, you have not checked anything.

## Status

One medium. The former bespoke language (its own lexer, parser, checker, generator — 4727 lines of
`.dlt` front-end) was removed: measured against a hand-written C control group it did not win the
one comparison that mattered (turning a declared goal into something that fails when the
implementation violates it), and its own flagship service violated its own declared invariant.
The superset form kept the part that did work. Everything under `code/` is C11 plus shell/Python.

## Quick start

Needs `cc` (C11), `sh`, `python3`, `git`, `flock`, `curl`. No Go, no code generator.

```sh
sh code/sup/build.sh sup                       # compile user TUs -> symbol audit -> wiring check -> link
sh code/sup/accept-sup.sh "$PWD/.build/sup/blobd" 18880 sup   # 24 wire assertions
```

Or talk to it directly:

```sh
mkdir -p /tmp/blobd-data && APP_ROOT=/tmp/blobd-data APP_TOKEN=secret \
  APP_BIND=127.0.0.1 APP_PORT=8787 .build/sup/blobd &
curl -s -X POST  -H 'Authorization: Bearer secret' http://127.0.0.1:8787/v1/keys/demo -i
curl -s -X PUT   -H 'Authorization: Bearer secret' --data-binary 'hello' http://127.0.0.1:8787/v1/blob/demo -i
curl -s           -H 'Authorization: Bearer secret' http://127.0.0.1:8787/v1/blob/demo
```

## What is actually checked

| Check | Fails when |
| --- | --- |
| table ↔ implementation reconciliation | the table names a missing function (`used but never defined`), or an implementation is not referenced by the table (`-Wunused-function` + `-Werror`) |
| symbol audit (`audit-sup.sh`) | a *user* translation unit references `getenv` / `environ` / `socket` / `open` … without declaring it |
| wiring check (`build.sh`, `nm -u`) | the service no longer references a decision that `DP_EXAMPLES` grades — i.e. the examples would grade a copy |
| sample oracle (`oracle-sup.sh`) | any `DP_EXAMPLES` row fails; zero runnable examples counts as failure; a crash is listed, never a pass |
| mutation (`mutate-dec.sh`) | a mutant survives and is not a hand-judged equivalent |
| wire assertions (`accept-table.sh`) | any of 24 external promises is broken (status, headers, body, snapshot, commit) |
| contract reconciliation (`contract-sup.py`) | a goal cites an assertion number that does not exist, or a goal is left unproven (`""` is an explicit admission) |
| sabotage (`sabotage-sup.sh`) | one of 11 injected defects is *not* caught by a named tool with a named diagnostic |

The last row is the important one: it checks the checkers. A "caught" that is really a compile error
from a broken injection is a false green, and this suite exists because that happened once.

## Layout

```
code/sup/            the superset: four declaration tables, the flagship service, every check
  blobd.c            DP_ROUTES + DP_GOALS — 125 lines, user-owned
  dp_env.h           DP_ENVS — 18 lines, user-owned
  dp_decide.h/.c     two pure decisions + DP_EXAMPLES — 62 lines, user-owned, linked by the service
  dp_sup.h dp_cfg.c dp_conf.h dp_oracle.c   mechanism, 344 lines
code/runtime/        the service runtime, 1472 lines of C (rt_core / rt_http / rt_blob / rt_ver)
code/webapi/deploy/  24-assertion acceptance table, soak, systemd unit, env example
docs/                SPEC (what this is), VALIDATION (measured numbers), NEXT (handoff)
```

## Known limits

This is a checking surface, not a sandbox. Bypassing `build.sh` bypasses the audit (E7);
`dlopen` + `dlsym` resolves at runtime where `nm -u` cannot see it (E8); the oracle only reaches
*pure* decisions, so auth and version preconditions living inside the runtime are covered by wire
assertions alone. All of it is written down in [docs/SPEC.md](docs/SPEC.md) §5 and
[docs/VALIDATION.md](docs/VALIDATION.md) §4, including what the green numbers do **not** prove.

Chinese version: [README_CN.md](README_CN.md).
