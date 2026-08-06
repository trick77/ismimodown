# AGENTS.md

Latency monitor for MiMo. Go backend + React dashboard, public read-only.

## Never commit secrets

`BACKEND_MIMO_API_KEY` (`tp-…`) is live and billable; repo is public. Key lives in `.env`
only — never in `.env.example`, a fixture, a commit message or a log line.

`BACKEND_MIMO_BASE_URL` takes no userinfo, no query string; `config.Load` refuses both, so a
key pasted there cannot travel. Same treatment for any NEW config value a public endpoint echoes.

## Git

Feature branch per phase, never commit to master. Conventional commits. PRs against THIS
repo, never a fork/upstream. Commit as `trick77@users.noreply.github.com`. Default branch
`master`, never `main`.

## Measurement invariants — break these and the numbers lie silently

**Always send a system message** (`config.DefaultSystemPrompt`). With none MiMo injects its
own: 250 prompt tokens, ~192 back as `cached_tokens` — 6.5x the token budget AND measured
prefill becomes a cache lookup. Any non-empty message suppresses it (prompt_tokens → 20).

**Call the residual "server-side time", never "model time".** The ping terminates at the TLS
edge; backhaul, queueing, prefill and scheduling all sit inside the residual, inseparable.

**Failed rows: out of latency percentiles, into availability.** Else a 240 000 ms timeout
lands in the P50 and an outage reads as catastrophic latency.

**A timeout is a recorded sample** — `ok=0`, an `error_class`, and how far it got.

**That exclusion truncates the tail, so publish `censored` beside every percentile.** The
excluded runs are the SLOWEST, so percentiles improve as truncation worsens. Classes in
`probe.CensoringErrorClasses` — add there, never to a literal in a query. Connection failures
are not censoring: nothing was measured. Never fold censored runs back INTO the percentiles.

**The API always publishes the count; a UI surface may gate its PROSE below
`MIN_FAILURES_FOR_STATE`.** One cut-off run in 288 daily cycles is a rounding error, and an
amber box about it sits on the card forever. Chart bands still draw it. Gate prose, never data.

**ONE inference call in flight, process-wide, `DispatchGap` apart.** Not per model, not per
probe: MiMo throttles the API key and there is one key. Concurrent models returned 429s that
publish as a MiMo outage (`rate_limited` is neither censoring nor availability-exempt). The gap
covers a short-window limiter, which serialising alone does not. Never race the models back.

**The slot BLOCKS, never skips.** A row's bucket is its cycle's `started_at`, so a probe that
waits still lands in its own bucket however late; a skipped probe leaves that bucket empty and
reads as a probe that was never running. Cost: a cycle costs the SUM and can overrun —
recorded by `logMissedTicks`, not prevented. Do NOT cap it by shortening later probes'
deadlines; that moves `censored` and the percentiles for scheduling reasons.

**`itl_p50_ms` is a chunk gap, not inter-token latency.** MiMo batches into bursts — a real run
measured 0.0075 ms against 70 tok/s. Never lead a chart with it; `output_tps` is the robust one.

**`probe` is a filter, never an aggregation.** Mixing `short` (40 tok) and `wide` (4k tok)
destroys the prefill signal, which IS the gap between them.

**`error_detail` is operator-only** — no public endpoint serves it; a provider error body can
echo request fragments. A test asserts this.

**`cached_tokens` must stay near zero.** A rise on `wide` means the cache-defeat nonce broke;
on `short`, the system prompt went missing.

**`reasoning_tokens` must be 0.** Send both `{"thinking":{"type":"disabled"}}` and
`enable_thinking:false`. This, not the `ttft_ms`/`ttfat_ms` delta, is the primary alarm —
those two columns are near-identical when healthy (role and first content chunk arrive in one
batch, ~0.008 ms).

**Ping is TCP-only, never ICMP.** ICMP is dropped as routine policy and needs `CAP_NET_RAW`.
**IPv4-only too**, at resolve and dial: the four targets are compared against each other, and a
v6 route timed against a v4 one publishes the difference as edge latency.

**Amsterdam is charted, never attributed.** `mimo_ams`/`ref_ams` stay out of `AttributeFault`
and `Summary.Net`. Adding them restores route-vs-uplink but MOVES published availability —
`netSummary` excludes uplink/route cycles from `mimo_sgp`'s denominator. Own change, own
reasoning. `Save` requires the SGP pair so a cycle can never be attributed without it.

**The network/inference subtraction JOINs on `cycle_id`**, never a nearest-timestamp guess.

## Backend

`backend/`, module `github.com/trick77/ismimodown`. stdlib `net/http` with Go 1.22+ method
routing, `Deps` struct DI, `slog` with `err` as the error key, `config.Load()` from `BACKEND_*`
env only. SQLite via pure-Go `ncruces/go-sqlite3`, WAL, `CGO_ENABLED=0`, `STRICT` tables.

Eight env vars, and that is the whole surface: API key, addr, log level, DB path, base URL,
SGP reference host, AMS reference host, probe user agent. Probe shape — models, prices, system
prompt, retention, timeout ladder — is constants in `config.go`. Do NOT add an env var for any
of them; they say what the page measures, not where it runs. The two REFERENCE hosts are the
exception and stay settable; both MiMo edges are fixed.

Do NOT add a dependabot ignore for `ncruces/go-sqlite3` — peeq pins it for sqlite-vec, this
repo has none.

Two limiters, different questions. The request one guards `/api/*`. The 404 one gates EVERY
route but charges only for a 404 — never 4xx at large, never a served response. Charging a 429
or a 400 compounds the two into a limit neither was sized for, and charging a 200 puts a
budget on the page load itself: one visit is a dozen asset requests.

Tests: `openTestDB(t)` against a real SQLite file in `t.TempDir()`, never `:memory:`. Probes
run against `httptest` fakes and a local `net.Listen` — no real API calls in tests, ever.

## Commands

```
make test              # backend tests
make backend-coverage  # tests + 75% line floor via hack/coverage-gate.sh
make run               # run the daemon
make dev               # local dev against a throwaway /tmp DB
```

Both coverage gates must pass: absolute floor (75%) and patch coverage (75%).

## Deploy

Container is distroless — no shell, no curl. Healthcheck is the binary probing itself:
`ismimodown -healthcheck`. Do NOT add a shell to the image to run one.

`compose.yaml`: external traefik network, `read_only`, `cap_drop: ALL`, non-root, resource
limits. Keep all of them.

See `DEPLOY.md`. Confirm all four ping targets FROM the probe box before trusting attribution —
a dead SGP reference kills the edge-vs-uplink distinction silently.

## Reference repos (read, never modify)

`../peeq` — backend patterns, Makefile, hack/, workflows, compose shape, filter-pill CSS
`../music` — UI stack (React 19 + Vite + TS + Tailwind v4), `@theme` tokens, `ui.tsx`
`../loom` — MiMo client: `internal/llm/{client,types,session,stream}.go`

## Naming

The SITE is "Is Xiaomi MiMo down?" at `ismimodown.com`. That exact question is the `<h1>`, the
`<title>`, the static body of `index.html` and the og card — four copies, reword together.

Always `Xiaomi MiMo`, never bare `MiMo`, in anything a stranger reads: "mimo" is also an antenna
technique and a learn-to-code app, and that disambiguation is the whole point of the name. In
comments, tests and log lines, bare `MiMo` is fine. Model IDs verbatim (`mimo-v2.5`,
`mimo-v2.5-pro`).

The CODE is `ismimodown` too, lowercase everywhere: Go module, `cmd/ismimodown`, GHCR image,
compose service, npm package. Never `IsMimoDown`, `isMimoDown` or `Is Mimo Down` as an identifier.

One exception: the database file stays `/data/mimostats.db`. A deployment's whole history lives in
that file on a bind mount; renaming the default orphans it on the next restart. `config_test` pins
the string. Do NOT "finish" that last one.

The footer denies affiliation with Xiaomi. Keep it; `Footer.test.tsx` asserts it.

**Never publish the exact cadence.** Stranger-facing copy says "periodically", or "every few
minutes" where a phrase is needed, or "within a few minutes" for the empty states — never "every
five minutes". The interval is a deployment detail that can change; code, comments and
`DEPLOY.md` still name the real number.

## Conventions

`.yaml` never `.yml`. English UI, 24-hour clock, times in the READER's zone — never pin
`timeZone` in `format.ts`; the footer names the resolved zone. Chart axes too: ECharts has no
per-axis timezone, so stamp `axisLabel`/tooltip via `format.ts`, never leave them on ECharts'
own formatting. Swiss orthography in German text: `ss` never `ß`. Anything visual → Safari
(`open -a Safari …`), never Chrome.

**The link-preview card never carries a measurement.** `ui/public/og.png` is committed, drawn
from `ui/assets/og/card.html` by `ui/scripts/gen-og.sh` — re-run and commit after editing the
source. A preview is scraped once and cached by the messenger (Slack ~30 min, WhatsApp and
Telegram effectively forever), so any number baked in freezes and is then shown as current.
Bump `?v=` on the og:/twitter: image URLs in the same commit, or a messenger that already
scraped the page never re-fetches and the redraw reaches nobody. WhatsApp crops it SQUARE to
the middle 630px; keep the heading and every lede line inside that band. The script asserts
both, and the og: URLs hardcode the `Host()` from `compose.yaml` — change them together, plus
the `.host` line card.html prints into the picture.

The card's heading width band in `gen-og.sh` is calibrated to the real render (527px) against
the font-fallback render (481px) — only 46px apart. Re-measure BOTH (break the `@font-face`
URLs on purpose) before touching it; never just widen it. The `<br>` in that heading is load
bearing: left to wrap, the fallback broke a word later and measured WIDER than the real face.

**Host and SEO strings move together:** `Host()` in `compose.yaml` (two routers — apex and the
www 301), og:/twitter: URLs, `rel=canonical`, `robots.txt`, `sitemap.xml`, JSON-LD `@id`s, and
`.host` in `card.html`.

**Comments in `ui/index.html` and `ui/public/` never ship.** `ui/build/strip-comments.ts` strips
them at build time — `<!-- -->` from html/svg/xml, whole `#` lines from `robots.txt` (a trailing
`# why` after a directive stays, so do not write one). So keep writing
them next to what they explain; do NOT thin them out for the reader's sake, and do NOT put a
path, a script name or a `compose.yaml` reference anywhere OUTSIDE a comment in those files.
