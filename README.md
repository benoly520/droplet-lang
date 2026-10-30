# Droplet

<p align="left"><strong>English</strong> · <a href="README_CN.md">简体中文</a></p>

A server-side programming language **designed for AI-generated, human-maintained services**.

More precisely: **the enabling layer for a generate-and-verify paradigm** — where a program is not written but *selected* as the proven solution among many candidates, and the human's job shifts from writing implementations to stating intent.

Droplet optimizes for one thing above all: **making AI mistakes impossible to keep silent, and making verification cheap enough that an agent will actually run it.** Not "a safer, more abstract, more elegant language" — a language where errors cannot stay hidden, checks finish in milliseconds, and the runtime is light enough to fit on the smallest production instance.

> Status: **D0 implemented, D1–D3 complete** — D1 landed sum variant values (`T.V` / `T.V { … }`), flattened `entry`, runtime-derived ETags, and typed `env` configuration (an `env App { … }` block becomes a record the handler receives as `cfg`; a missing required key fails at startup and source never spells an environment-variable name). D2 removed the `Response` record entirely: **success is a value** — an entry returns `bytes` (with a body) or `Unit` (without), a bare `return` means the return type's unit value, and HTTP status codes live only in the error-variant → status table (`NotFound`→404, `NotAllowed`→405, `PreconditionFailed`→412, …). Writing `Response{status: 200, body: X}` is now a `check`-time `E1` telling you it *is* `X`. D3 made `boundary` the single source of the HTTP face: methods carry `@<verb>("/path") impl <fn>`, the route table *is* the service entry (a bare `entry` next to routes is an error), and every write route must state its precondition — `with precondition` or `without precondition: "reason"`, silence is a compile error. Generated guards call the runtime's single `dp_precondition_check`, so the 22-line If-Match block that used to live in the HTTP layer is gone. Success codes are projected from declarations too — `@created` → 201, `@no_content` → 204, default 200, and the number never appears in source (a 204 must be declared on a `Unit` success type, and the runtime omits `Content-Length` for it per RFC 7230). A 95-line example service (contract + handler) compiles to a C11 binary that boots in under 2 MB RSS and speaks HTTP/1.1; 126 Go tests cover lexer, parser, checker, generator, host-capability signatures, runtime dead-code audit, error-tag/runtime agreement, AST dump, contract export, the expression printer (a canonical form that re-parses to the same AST, now used to export `requires` / `example` expressions into the contract), and the static half of `E10` — intent expressions are type-checked, constant-decided (a tautology is an empty declaration, a contradiction is unreachable), and `requires` is checked at call sites whose arguments are constants. The implemented language is still a deliberate subset of the design — every gap (opaque secrets, feature-flag lifetimes, …) carries a reason and a fill-in milestone in [docs/D0-DEVIATIONS.md](docs/D0-DEVIATIONS.md). Full proposal: [docs/SPEC.md](docs/SPEC.md) (Chinese). Where things stand and what is next: [docs/NEXT.md](docs/NEXT.md).

## Quick start

```sh
mkdir -p .build
(cd code/lang && go build -o ../../.build/droplet ./cmd/droplet)

# smallest closed loop: source → C → binary → stdout
.build/droplet build code/lang/examples/hello.dlt -o .build/hello && .build/hello   # world

# the example service: contract + handler → binary → HTTP
.build/droplet contract code/webapi/src/contract.dlt code/webapi/src/main.dlt      # JSON contract + variant tags
.build/droplet build    code/webapi/src/contract.dlt code/webapi/src/main.dlt -o .build/blobd
mkdir -p .build/data    # APP_ROOT is required (no default) and must already exist; missing either → the server exits at startup
APP_ROOT=.build/data APP_TOKEN=t .build/blobd &
curl -s -X PUT -H 'Authorization: Bearer t' --data 'hello' localhost:8787/v1/blob/demo
curl -s        -H 'Authorization: Bearer t' localhost:8787/v1/blob/demo              # hello
```

`sh code/webapi/deploy/accept.sh` runs the 17 boundary checks (412 with an empty body / 204 without Content-Length / 201 / 405 / 413 / 501 / derived ETag …); `sh code/webapi/deploy/soak.sh` is the long-running leak observation.

## Core idea

LLMs fail on server code mostly because correctness hides in places the model cannot see: effect-free-looking functions that touch the database, contracts encoded in string tags, errors that may be dropped, state that outlives a request, configuration that only fails at deploy time.

Droplet's answer is to make those facts **visible in the signature and checkable at compile time** — so an agent can find and fix its own mistakes instead of relying on a human reviewer to catch them.

## Hard constraints

| # | Constraint | Target |
| --- | --- | --- |
| H1 | Teaching surface (spec + all rules + core API, machine-readable) | ≤ 2000 tokens — **so the language needs no retraining** |
| H2 | Core keywords | ≤ 14 |
| H3 | Core primitives | ≤ 24 |
| H4 | Idle-service RSS in production | ≤ 3 MB |
| H5 | Idle CPU / per-request overhead | ≤ 0.5% of one core / ≤ 2 µs |
| H6 | **Marginal** memory cost per added route / task | reported by `droplet mem`, budgeted in CI |
| H7 | **Repeated-writing budget**: the same field of a record literal written with the same constant ≥3 times, or the same keyword-and-brackets-only line ≥3 times | 0 occurrences — a gap in *syntax*, not in docs |
| H8 | **Implementation-size budget**: lines per source file; compiler (excl. tests) + embedded runtime | ≤ 800 lines per file; ≤ 8000 lines total |

H1 and H4–H8 squeeze the language from both sides (see SPEC §9 for the full mechanism-level reduction list and CI gates): one limits **how much you must teach the model**, the others limit **how much you may hide in the runtime — and how much each new feature adds to it**. A 3 MB idle service that balloons to 80 MB after 20 routes has not met the budget; the real bill is `routes × marginal bytes × instances`. Both disciplines point the same way — an invisible mechanism is invisible precisely because it decides something for you at runtime, and every decision costs memory. **What AI cannot verify is usually also what costs the most.**

H7 and H8 close the remaining two loopholes: H7 makes verbosity a *compiler* error (fix the syntax, don't add a doc section), and H8 keeps the verifier itself small enough to be read and repaired by an agent — a checker nobody can read only certifies trust.

## Paradigm (SPEC §0)

Past paradigms all optimise one scarce resource: **human comprehension bandwidth**. Abstraction, encapsulation, design patterns, type systems — all of them are "save human brain" designs. The cost structure has flipped: **candidate code is nearly free, verification is the bottleneck.** Droplet's role is to make the new loop affordable:

```
intent (properties + contracts + boundary + examples)
   → candidates (AI, many, random)        ← randomness lives ONLY here
   → verifier (types / effects / resources / properties / replay / mutation)
   → survivor(s) → deterministic, human-readable, diffable source code
```

Three non-negotiables: the **runtime stays deterministic** (probabilistic semantics would destroy substitutability, the foundation of five decades of programming), the **deliverable stays human-readable**, and **verification stays sub-second**. New composition rule under investigation: property-union + effect-subset + additive resource budget — this, not syntax, is where any originality would live.

In one sentence: **old paradigms were designed to save human brain; the new one is designed for verifiability; and the new one cannot run *unless* a language makes intent a first-class construct, makes verification millisecond-cheap, and makes the runtime small enough for the smallest instance. Droplet is that "unless."**

**Convergence rule (decided, SPEC §7.8.3):** if the surviving candidates differ in externally observable behaviour, the build **fails** (`E12`) and a human adds a constraint. The language deliberately provides **no** way to auto-pick a candidate — no `strategy: minimal`, no default, no last-known-good fallback — because automatic convergence would turn required intent into optional documentation, and the whole enabling layer with it. Exploration is served by `droplet explore`, whose output is explicitly not a releasable artifact. The honest cost of this choice — under-specification becomes *the human's* new bottleneck — is measured, not argued away (`E12` frequency and average time-to-constrain are §11.3 metrics, and a falsification path for claim P-D).

**Honest caveat (SPEC appendix C):** the draft audited itself for inherited assumptions and found three structural ones it had never questioned — *programs are text*, *functions are the unit of thought*, *deployment is not a language concern*. Two of them are open experiments (V1 state-machine-first, V2 AST-identity diffs) that must run before the core is frozen.

## Design pillars

- **No escape hatches** — no `any`/`as`/`unsafe`/`#[allow]` equivalents. An agent optimizing for "make the build pass" will systematically abuse any exit that exists.
- **Effects in the signature** — what a function can touch (which database, which external host, the clock) is a type-level fact; no ambient authority, no global `fetch`, no implicit environment reads.
- **Affine types + explicit regions** — resource leaks and transactions that span network calls become compile errors; the same model gives static memory bounds for bare-metal targets.
- **Boundaries are declared, not assumed** — RPC/queue/HTTP payloads, versions, and idempotency requirements live in one `boundary` construct that generates stubs, handlers, serializers and machine-readable contracts.
- **Typed configuration and opaque secrets** — a missing key in an environment fails the build, not the pager. Secrets cannot be printed or logged.
- **Resource budgets are types** — per-request memory caps and cache sizes are checkable facts, which kills unbounded queries and unbounded caches at compile time.
- **Errors are values that cannot be dropped** — propagate, handle, or record an explicit `ignore(reason)`.
- **Intent is the input surface, not a comment** — `requires` / `effects` / `cap` / `idempotent` / `invariant` / `example` are checked, mutated against, and used to converge (SPEC §5.10). An `entry` or `boundary` method with no verifiable intent does not compile (`E11`); an intent that catches no mutation is a hard error (`E10`).
- **Single static binary, no interpreter, no VM, no JIT, no daemon.**

## Roadmap

| Stage | Deliverable | Gate |
| --- | --- | --- |
| S0 | Spec freeze | hard constraints satisfied on paper |
| S1 | **Semantics validation without a compiler** (checker prototype in a host language + ablation experiments) | first-pass rate beats baseline; false positives < 5% |
| S2 | Bootstrap: real syntax + C backend + `droplet check`/`mem`/`bench` | real service runs; idle RSS ≤ 3 MB measured |
| S3 | Minimum usable server stack | non-author users running in production |
| S4 | Bare-metal backend | "one language from MCU to server" validated |

**S1 is the point of no return**: it tests the central claim — that a small enough language lets AI write correct code without being retrained — before we spend years on a compiler.

## License

MIT © benoly520
