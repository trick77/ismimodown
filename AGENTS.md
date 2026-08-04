# AGENTS.md

Continuous latency monitor for Xiaomi MiMo. Go backend + React dashboard, public read-only.

## Never commit secrets

`BACKEND_MIMO_API_KEY` (a `tp-…` token-plan key) is live and billable; repo is public.
Key lives in `.env` only. `.gitignore` covers `.env` and `.env.*`. Never put a real key in
`.env.example`, a test fixture, a commit message or a log line.

`BACKEND_MIMO_BASE_URL` is published verbatim on `/api/methodology`. No userinfo, no query
string — `config.Load` refuses both. Any NEW config value echoed by a public endpoint gets
the same treatment.

## Git

Feature branch per phase, never commit to master. Conventional commits.
PRs against THIS repo, never a fork/upstream. Commit as `trick77@users.noreply.github.com`.
Default branch is `master`, never `main`.

## Measurement invariants — break these and the numbers lie silently

**Always send a system message** on every probe request (`BACKEND_PROBE_SYSTEM_PROMPT`).
With none, MiMo injects its own: 250 prompt tokens, ~192 returned as `cached_tokens`.
Inflates the token budget ~6.5x AND turns measured prefill into a cache lookup. Any
non-empty system message suppresses it (prompt_tokens drops to 20).

**Call the residual "server-side time", never "model time".** The TCP ping terminates at the
TLS edge; Xiaomi runs no European GPUs, so edge-to-compute backhaul sits inside the residual.

**Failed rows are excluded from latency percentiles, counted in availability.** Otherwise a
240 000 ms timeout lands in the P50 and an outage reads as catastrophic latency.

**A timeout is a recorded sample, never a dropped one** — `ok=0`, an `error_class`, and
however far it got.

**That exclusion truncates the tail, so always publish the `censored` count beside the
percentiles.** The excluded runs are the SLOWEST ones, so every percentile is over the
survivors and improves as truncation worsens. Classes in `probe.CensoringErrorClasses`;
add to that list, never to a literal in a query. Connection failures are not censoring —
nothing was measured. Never fold censored runs back INTO the percentiles.

**Cycles run models concurrently, probes within a model sequentially.** Sequential models
make a cycle cost the sum, so the cadence breaks at ~`CycleInterval / len(Models)` and the
series thins out during the incident it exists to record. Two probes at once against one
model contend for the same upstream node. A wide cycle still costs infer+wide per model
and can still overrun — that is recorded, not prevented.

**A dropped tick is recorded, never just absorbed.** A cycle overrunning its slot is the
only thing that writes `skipped_runs`; the `inFlight` guard cannot fire while cycles run
one at a time.

**`itl_p50_ms` is a chunk-level gap, not inter-token latency.** MiMo batches tokens into
chunks and delivers them in bursts — a real run measured itl_p50=0.0075ms against 70 tok/s.
Do not lead a chart with it; `output_tps` over the decode window is the robust one. Both are
stored.

**`probe` is always a filter, never an aggregation.** Mixing `infer` (40 tok) and `wide`
(4 k tok) into one series destroys the prefill signal, which IS the gap between them.

**`error_detail` is operator-only** — never served by any public endpoint. A provider error
body can echo request fragments. A test asserts this.

**`cached_tokens` must stay near zero.** A rise on `wide` means the cache-defeat nonce
stopped working; on `infer` it means the system prompt went missing.

**`reasoning_tokens` must be 0.** Send both `{"thinking":{"type":"disabled"}}` and
`enable_thinking:false`. This — not the `ttft_ms`/`ttfat_ms` delta — is the primary alarm for
reasoning creeping back on. MiMo emits the role chunk and first content chunk in the same
batch (measured gap ~0.008 ms), so those two columns are near-identical when healthy.

**Ping is TCP-only, never ICMP.** ICMP is dropped as routine policy and needs `CAP_NET_RAW`.

**The network/inference subtraction is a JOIN on `cycle_id`** — never a nearest-timestamp
guess. Every `infer_probes` row needs a `net_probes` row in the same cycle.

## Backend

`backend/`, module `github.com/trick77/mimostats`. stdlib `net/http` with Go 1.22+ method
routing, `Deps` struct DI, `slog` with `err` as the error attr key, `config.Load()` from
`BACKEND_*` env only. SQLite via pure-Go `ncruces/go-sqlite3`, WAL, `CGO_ENABLED=0`.
Tables are `STRICT`.

Do NOT add a dependabot ignore for `ncruces/go-sqlite3`. peeq pins it for sqlite-vec;
mimostats has no sqlite-vec.

Tests: `openTestDB(t)` against a real SQLite file in `t.TempDir()`, never `:memory:`.
Probe tests run against `httptest.NewServer` fakes and a local `net.Listen` — no real API
calls in tests, ever.

## Commands

```
make test              # backend tests
make backend-coverage  # tests + 75% line floor via hack/coverage-gate.sh
make run               # run the daemon
make dev               # local dev against a throwaway /tmp DB
```

Both coverage gates must pass: absolute floor (75%) and patch coverage (75%).

## Deploy

Container is distroless — no shell, no curl. Healthcheck is the binary probing
itself: `mimostats -healthcheck`. Do NOT add a shell to the image to run one.

`compose.yaml`: external traefik network, `read_only`, `cap_drop: ALL`,
non-root, resource limits. Keep all of them.

See `DEPLOY.md`. Confirm the two reference ping hosts FROM the probe box before
trusting attribution — a dead europe reference destroys the route-vs-uplink
distinction without causing a visible failure.

## Reference repos (read, never modify)

`../peeq` — backend patterns, Makefile, hack/, workflows, compose shape, filter-pill CSS
`../music` — UI stack (React 19 + Vite + TS + Tailwind v4), `@theme` tokens, `ui.tsx`
`../loom` — MiMo client: `internal/llm/{client,types,session,stream}.go`

## Conventions

`.yaml` never `.yml`. English UI, Europe/Zurich, 24-hour clock.
Chart axes too — ECharts has no per-axis timezone, so stamp `axisLabel`/tooltip
via `format.ts`, never leave them on the viewer's clock.
MiMo off-peak (`ui/src/offpeak.ts`) is 16:00–24:00 **UTC**. Derive from UTC —
Beijing has no DST, Zurich does. It is a price; never label it as demand.
Swiss orthography in any German text: `ss` never `ß`.
Anything visual → show in Safari (`open -a Safari …`), never Chrome.
