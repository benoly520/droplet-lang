# Droplet

**A checked C workspace for AI-written services: valid C11 source, declarations as tables,
and checks that fail when the implementation drifts from what was declared.**

A "superset" here means exactly one thing: valid C is never rejected by this layer. There was a
bespoke language in this repository; it was removed, and no further extensions to **C** will be
built (docs/SPEC.md §2.1) — no new syntax, no compiler branches, no annotation checkers. What
remains is the part that measurably worked. One caveat, stated rather than buried: v0.38 added
`lang/`, a tiny *separate* medium for registering claims and their judges (see docs/LANG2.md). It
touches no C, is excluded from the frozen question surface, replaces nothing, and is marked
experimental with kill criteria.

"AI" here means the author, not a design target: nothing in the syntax or the toolchain is specific
to AI. What is specific is the workflow — declarations a machine can reconcile, checks that fail,
and results a human can accept without reading the implementation. Whether that workflow actually
helps AI write more reliable C: there is now **one small measurement** — three independent generations
of the decision layer, all passing on the first submission (pass@1 = 3/3) — plus one maintenance trial
(405 -> 501 in 5 files / 18 +/- lines, full check green). The whole service and real human maintainers
are still unmeasured; see [docs/EXPERIMENT.md](docs/EXPERIMENT.md).

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
sh code/sup/check.sh                            # one command, one exit code (build/samples/contract/registry/freeze/budget/language)
sh code/sup/report.sh .build/check/blobd --accept 18881   # human page: requirement -> assertions/samples -> red/green
```

The authoring loop is three commands: `brief.sh` emits a task packet (declarations, contract, commands —
no implementation), `check.sh` gives the first failing gate, `submit-sup.sh candidate.c` runs the whole
surface in a scratch tree (including the question-freeze check) and accepts or rejects it. `check.sh --full` adds acceptance, mutation and
sabotage for maintainers.

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
| mutation (`mutate-dec.sh`) | a mutant survives and is not a hand-judged equivalent (13 mutants: 10 caught / 2 equivalent / 1 compile-time) |
| wire assertions (`accept-table.sh`) | any of 30 external promises is broken (status, headers, body, snapshot, commit, auth, list envelope, content-type, boot-time refusal) |
| contract reconciliation (`contract-sup.py`) | a goal cites an assertion number that does not exist, or a goal is left unproven (`""` is an explicit admission) |
| surface budget (`surface-budget.sh`) | the must-read tables, types and reference service grow past 9,000 tokens / 32 KB |
| candidate comparison (`compare-sup.sh`) | 0 or more than 1 candidate survives the examples (no solution / ambiguity) |
| first-pass ruler (`firstpass.sh`) | given a candidate sequence, it computes first-pass and pass@k; **no model, no number** |
| maintenance cost (`maint-cost.sh`) | a scripted change touches more files or +/- lines than its budget |
| translation layer (`req/SRC.md` + `req/REQ.tsv` + `req/TRANS.tsv` + `req/AMBIG.tsv` + `req/GAP.tsv` + `req/BASELINE`) | a clause has no recorded reading, its counterexample column is empty or claims a catcher (`S<n>`/`E<n>`) that doesn't exist or didn't actually redden this clause's assertions, an ambiguity is undecided, a gap is `未闭`, or a `新增` clause was already green on the baseline (old implementation + current question) — i.e. the status quo was transcribed as a requirement. The baseline check now runs automatically |
| requirement registry (`req-check.py`) | a requirement has no assertion/example, or a source sentence/assertion/example/goal is unclaimed, or a requirement disagrees with its goals' declared proof |
| registration language (`lang-check.sh`, experimental) | the `.dpt` definition and its checker disagree (a documented error code that can never fire, or a code with no live sample), `lang/blobd.dpt` stops being field-for-field equivalent to `req/`, or the complete definition grows past 1,500 tokens |
| question freeze (`freeze.sh`) | the acceptance table, declaration headers, the five translation files or the contract differ from `req/FROZEN` (the question was edited) |
| mutation score (`mutation-score.py`) | changing assertion N's expectation does not turn it red, a mutant reddens an undeclared assertion, or coverage falls below the floor |
| sabotage (`sabotage-sup.sh`) | one of 23 injected defects is *not* caught by a named tool with a named diagnostic (6 of them edit the question, the translation or the baseline instead of the code; 3 edit the registration language's own definition) |

The last row is the important one: it checks the checkers. A "caught" that is really a compile error
from a broken injection is a false green, and this suite exists because that happened once.

## Layout

```
code/sup/            the checked layer: four declaration tables, the flagship service, every check
  blobd.c            DP_ROUTES + DP_GOALS — 229 lines, user-owned
  dp_env.h           DP_ENVS — 18 lines, user-owned
  dp_decide.h/.c     five pure decisions + 19 DP_EXAMPLES — 114 lines, user-owned, linked by the service
  check.sh report.sh brief.sh submit-sup.sh   the three loops: author, human acceptance, submission
  req-check.py freeze.sh mutation-score.py   registry + translation / question freeze / assertion sensitivity
  dp_sup.h dp_cfg.c dp_conf.h dp_oracle.c   mechanism, 344 lines
code/runtime/        the service runtime, 1468 lines of C (rt_core / rt_http / rt_blob / rt_ver, incl. rt.h)
code/sup/lab/        the experiment packet: two decisions that do not exist in this repo
code/webapi/deploy/  30-assertion acceptance table, soak, systemd unit, env example
req/                 16 source sentences, 19 clauses, translation ledgers (TRANS/AMBIG/GAP), baseline pin, frozen hashes, README
lang/                experimental registration language: droplet.dpt (the whole definition, 1,321 tokens) + blobd.dpt (mechanical rewrite of req/, proven equivalent)
docs/                SPEC, VALIDATION (numbers), EXPERIMENT (two trials), LANG2 (the registration language and its measurement), NEXT (handoff)
```

## Known limits

This is a checking surface, not a sandbox. Bypassing `build.sh` bypasses the audit (E7);
`dlopen` + `dlsym` resolves at runtime where `nm -u` cannot see it (E8); the oracle only reaches
*pure* decisions (If-Match, auth and the body limit are now among them), so the reads inside the
runtime — loading the old version, headers, ETag computation, snapshot rotation — are covered by
wire assertions alone. First-pass rate has a ruler (`firstpass.sh`) but no measured number, because
no model runs in this repository. All of it is written down in
[docs/SPEC.md](docs/SPEC.md) §5 and [docs/VALIDATION.md](docs/VALIDATION.md) §4, including what the
green numbers do **not** prove.

Chinese version: [README_CN.md](README_CN.md).
