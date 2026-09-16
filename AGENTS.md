# AGENTS.md

Latency monitor for MiMo. Go backend + React dashboard, public read-only. Rationale is in code comments; this is the rule list.

## Secrets

`BACKEND_MIMO_API_KEY` is live, billable, repo public: `.env` only, never `.env.example`, fixtures, commits, logs. `BACKEND_MIMO_BASE_URL` takes no userinfo/query (`config.Load` refuses); same for any new value a public endpoint echoes. `error_detail` is operator-only, never served (tested).

## Measurement invariants

- Always send `config.DefaultSystemPrompt`. Without it MiMo injects its own: 250 prompt tokens, ~192 `cached_tokens`, prefill becomes a cache lookup.
- `cached_tokens` near 0, `reasoning_tokens` exactly 0 (send `thinking.type=disabled` AND `enable_thinking:false`). Reasoning is the primary alarm.
- Failed rows out of percentiles, into availability; residual is "server-side time", never "model time". Timeout = recorded sample (`ok=0`, `error_class`).
- Publish `censored` beside every percentile. Classes only in `probe.CensoringErrorClasses`. Connection failures are not censoring. Never fold censored back in.
- API always publishes counts; UI may gate PROSE below `MIN_FAILURES_FOR_STATE`, never data.
- ONE inference call in flight process-wide, `DispatchGap` apart (one key; concurrent models 429'd, published as a MiMo outage). Slot BLOCKS, never skips; overrun logged by `logMissedTicks`, never fixed by shortening later deadlines.
- `itl_p50_ms` is a chunk gap (0.0075 ms at 70 tok/s). Lead with `output_tps`.
- Ping TCP-only, IPv4-only at resolve and dial. Amsterdam charted, never attributed (`AttributeFault`, `Summary.Net` stay SGP; `Save` requires the SGP pair). Subtraction JOINs on `cycle_id`.

## Banner (`ui/src/trend.ts`, `verdict.ts`)

- Floors MEASURED: `+70%` first token (`+40%` both models), `-20%` throughput; a round 10% fires on 70% of readings. Rank moves by SECONDS added, never per cent.
- Relative alone never takes the banner: lead move must also cross `SLOW_TTFT_MS`/`SLOW_TPS` (chosen), tested per MOVE. Else `minor`: no headline, chip, plot, sentence.
- One claim per page. Chip `slower` (never `elevated`, spent on faults); "normal" and "slower" never together. Fault outranks speed, includes window availability/correctness (`scoreModel`).
- Present tense everywhere: `stillHappening`, per TRACK, dated off the whole served block. `unknown` never softened.
- Quiet banner: NO figures. "As usual" needs `steady` + `everyModelRead` + empty `verdict.detail`.
- Sentences body-size serif `ink-dim`, never `text-label text-muted`. Faster is measured, never said.

## Backend

`slog`, `err` key. Eight `BACKEND_*` env vars, that is the surface; probe shape is constants in `config.go`, no env var. SQLite pure-Go `ncruces/go-sqlite3`, `CGO_ENABLED=0`, WAL, STRICT, no dependabot ignore.

Cycle builds the dashboard, never a request: `OnCycle` runs `Server.Warm` (all windows, in place) BEFORE `broker.Publish`. Invalidate-then-notify measured 19 s per request at five concurrent cold misses. Misses single-flighted, one build slot (`cpus: "1.0"`), TTL a two-cycle valve. Never shorten it, never add a slot.

Limiters: request limiter guards `/api/*`. 404 limiter charges ONLY a 404 with a non-image extension (`isUncounted404`): never 4xx at large, 200s, image misses, `/.well-known`, extensionless. `banGate` is not a limiter: exploit path (`exploitpaths.go`) = 48 h in-memory block, matched on `r.URL.Path` before the mux, any depth (safe only while no dynamic path segments). Only a REPEAT exploit path renews (NAT pool + own tab would self-renew forever). New path: check `TestRealTrafficIsNotAnExploitPath`.

Tests: `openTestDB(t)` real file in `t.TempDir()`, never `:memory:`. No real API calls, ever. `make test` / `make backend-coverage` (75% floor + patch) / `make run` / `make dev`.

## Deploy & serving

Distroless, no shell: healthcheck is `ismimodown -healthcheck`. Keep every `compose.yaml` hardening line.

`spaHandler`: `/` serves the shell, everything else 404s (`TestUnknownPathsAreNotFound`). No third-party origin in CSP (`TestNoThirdPartyOriginsInThePolicy`).

## Naming & copy

"Is Xiaomi MiMo down?" in `<h1>`, `<title>`, static `index.html`, og card: reword together. Stranger-facing always `Xiaomi MiMo`; model IDs verbatim. Code `ismimodown` lowercase; DB file stays `/data/mimostats.db` (`config_test` pins it). Footer denies Xiaomi affiliation (tested). Cadence: "periodically" / "every few minutes", never "every five minutes".

English, 24 h, reader's zone: never pin `timeZone` in `format.ts`; ECharts axes/tooltips via `format.ts`.

Preview card carries no measurement. `ui/public/og.png` from `ui/assets/og/card.html` via `ui/scripts/gen-og.sh`: re-run, commit, bump `?v=` together. WhatsApp crops to the middle 630 px. Heading band 527 px real / 481 px fallback, re-measure both before touching; heading `<br>` is load bearing. Host and SEO strings move together: `Host()`, og URLs, canonical, `robots.txt`, `sitemap.xml`, JSON-LD, `.host` in `card.html`.

Comments in `ui/index.html` and `ui/public/` never ship (`ui/build/strip-comments.ts`); a trailing `# why` on a `robots.txt` directive survives, never write one. No path or script name outside a comment there.

## Reference repos, read only

`../peeq` backend patterns · `../music` UI stack, `@theme` tokens · `../loom` MiMo client.
