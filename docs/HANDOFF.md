# mimostats — session handoff

Paste the block below into a fresh session.

---

Continue **mimostats** — a continuous latency monitor for Xiaomi MiMo that separates
time-to-reach-the-endpoint from server-side time.

## State

Working dir `/Users/jan/localgit/llmstats` (directory name is stale — the project is
**mimostats**). Repo `git@github.com:trick77/mimostats.git`, public, default branch `master`,
module `github.com/trick77/mimostats`. Site will be `mimostats.trick77.com`.

All six planned phases are built. PRs #1–#6 merged; **PR #7 (compose, container healthcheck,
DEPLOY.md) is open and CI-green**. Backend coverage 87.6%, UI 82.3%, floor 75%.

Read first: `docs/plan.html` (open in Safari — plan of record, carries inline amendments),
then `AGENTS.md` (the invariants).

## Do this next, in order

1. **`/code-review medium 7 --fix`** — I cannot invoke it (`disable-model-invocation`); the
   user must. It has found a real defect on every PR so far. Apply findings, then merge #7.
2. Merging #7 cuts the next patch release. The image already builds: six release runs have
   succeeded and `0.0.6` — the first build carrying the UI stage — is on GHCR as `latest`.
   It has never been RUN, only built, so `docker compose up` is still unexercised.
3. **Confirm the two reference ping hosts from the probe box.** Every RTT so far is from
   Zurich. `DEPLOY.md` has a copy-paste script. This is the longest-standing open item.
4. Deploy per `DEPLOY.md`, then let it run 24h and confirm the network/server-side split looks
   sane and consumption tracks the cost model.

## Workflow (non-negotiable)

Feature branch per unit of work, never commit to master. Conventional commits. `gh pr create`
against **this** repo. Ask the user to run `/code-review medium <PR#> --fix` on every PR, apply
findings, merge when CI is green. Commit as `trick77@users.noreply.github.com`. Show anything
visual in **Safari**, never Chrome (Chrome is for automated verification only).

## Secrets

`BACKEND_MIMO_API_KEY` is a live billable `tp-…` key. The working one lives in
`/Users/jan/localgit/loom/.env` as `BACKEND_CHAT_API_KEY` (the copy in opencode's `auth.json`
is dead — 401). Never commit it; `.gitignore` covers `.env` and `.env.*`. Scan
`git diff --cached` for it before every commit.

## Findings that contradicted the plan — do not undo these

- **Always send a system message.** With none, MiMo injects its own: 250 prompt tokens, ~192
  cache-served. That inflates the token budget ~6.5× *and* turns measured prefill into a cache
  lookup. With one: 34 tokens, `cached_tokens: 0`. Pinned by a config test; empty is a boot error.
- **`itl_p50_ms` is a gap between CHUNKS, not tokens.** MiMo batches and bursts — a healthy
  `wide` run measured **0.0075 ms** against 70 tok/s. The plan said lead the throughput chart
  with ITL p50; the dashboard leads with `output_tps` instead. Both are stored and served, and
  the caveat is on `/api/methodology`. Reversible in one panel if the user disagrees.
- **`ttfat_ms` is structurally ~equal to `ttft_ms`** (measured gap 0.008 ms) — MiMo emits the
  role chunk and first content chunk in one batch. The real alarm for reasoning returning is
  `reasoning_tokens != 0`, not the delta.
- **The plan's OVH Singapore reference does not exist.** `ap-southeast-sgp` is NXDOMAIN and
  `sgp.ovh` is Cloudflare anycast in *Europe*. Using `sgp1.digitaloceanspaces.com`, at the cost
  of the same-carrier argument (disclosed on the methodology page).
- **`ref_eu` is `cloudflare.com`, not `1.1.1.1`.** Bare resolver IPs are filtered on port 443 on
  many networks (`1.1.1.1:53` connects, `:443` times out).
  *Superseded:* the European reference probe was removed — there is one reference host
  (`ref_sgp`) now, and `route` is no longer produced. The resolver-IP caveat still applies to
  whatever `BACKEND_PING_REF_SGP_HOST` is set to.
- **The `wide` corpus is ~3222 words ≈ 3844 prompt tokens**, sized against the plan's 4k target.

## Invariants that fail silently — the reason most of the tests exist

Failed runs store NULL timings, never 0, and are excluded from percentiles but counted in
availability. `probe` is always a filter, never an aggregation. `error_detail` is operator-only
and never served (asserted). `cached_tokens` and `reasoning_tokens` must stay 0. The
network/inference subtraction is a JOIN on `cycle_id`. Percentiles suppress below 20 samples.
Never call the residual "model time".

## Things that bit, and would bite again

- **Unit tests cannot catch bundling bugs.** The dashboard died with React error #130 on every
  load (`echarts-for-react/lib/core` is CJS; its default export bundles as an object) while all
  52 tests passed, because the tests mock that module. **Always load the built bundle in a real
  browser before declaring UI work done.**
- `hack/patch-coverage.sh` excludes `cmd/` to match `coverage-gate.sh`. Don't "fix" that.
- The `.gitignore` force-includes `backend/web/dist/index.html`; a UI build overwrites it, so
  `git checkout -- backend/web/dist/index.html` after building or the real bundle gets committed.
- Peeq's dependabot ignore on `ncruces/go-sqlite3` is deliberately **not** copied (that pin
  exists for sqlite-vec, which this project does not use).
