# Deploying mimostats

The site is ismimodown.com; the repo, module, image and database file are still
called `mimostats`. Both names appear below and neither is a typo.

One container behind an existing Traefik. The whole thing is a single Go binary
with the dashboard embedded, plus one SQLite file.

## Prerequisites

- Docker with Compose, and a Traefik instance already terminating TLS.
- An external network shared with Traefik:

  ```sh
  docker network create traefik    # if it does not already exist
  ```

- **Two** DNS A records pointing at the host: `ismimodown.com` and
  `www.ismimodown.com`. www exists only to 301 to the apex, but it needs its own
  record and its own certificate — see below.
- An ACME resolver named `letsencrypt` in Traefik's static config. The router
  labels name it explicitly:

  ```toml
  [certificatesResolvers.letsencrypt.acme]
    email = "…"
    tlsChallenge = true
    storage = "/letsencrypt/acme.json"
  ```

  `tlsChallenge` is TLS-ALPN-01, answered on 443 through the `websecure`
  entrypoint. Nothing needs port 80 and there is no challenge path to route.

  **Let the DNS resolve before the first `docker compose up`.** The ACME order
  fires on router creation; against a name that does not resolve it fails, and
  Let's Encrypt rate-limits failed authorisations. The visible symptom is a
  browser interstitial — Traefik falls back to its own self-signed certificate
  rather than erroring.
- A MiMo token-plan key (`tp-…`).

## Install

```sh
git clone git@github.com:trick77/mimostats.git
cd mimostats
cp .env.example .env
$EDITOR .env                      # BACKEND_MIMO_API_KEY is the only required value

# The data directory must exist AND be owned by the container's UID before the
# first start. It is gitignored, so a fresh clone has no ./data — and Docker
# creates a missing bind-mount source owned by root, which the non-root
# container then cannot write. SQLite fails to create the database and the
# container crash-loops with "unable to open database file".
mkdir -p data
sudo chown 1000:1000 data         # matches `user:` in compose.yaml

docker compose up -d
```

The image comes from `ghcr.io/trick77/mimostats:latest`, published by the
release workflow on every push to `master` that touches `backend/` or `ui/`.

**The key is billable.** `.env` is gitignored, along with every `.env.*`
variant, because this repo is public. Never put a real key in `.env.example`.

## Verifying

```sh
docker compose ps                       # healthy within ~30s
docker compose logs -f mimostats        # "cycle complete" every ~5 minutes
curl -s https://ismimodown.com/healthz
```

The certificate is the first thing to check on a new host — a self-signed
fallback still answers, so `curl -k` and a browser click-through both hide it:

```sh
echo | openssl s_client -connect ismimodown.com:443 -servername ismimodown.com 2>/dev/null | \
  openssl x509 -noout -issuer -dates
```

The issuer must be Let's Encrypt. `TRAEFIK DEFAULT CERT` means the ACME order
never completed — check DNS first, then `docker compose logs traefik`.

www must redirect permanently rather than serve:

```sh
curl -sSI https://www.ismimodown.com/ | head -3   # 301, location: https://ismimodown.com/
```

Response headers, after a Traefik reload picks up the new labels:

```sh
curl -sSI https://ismimodown.com/ | \
  grep -Ei 'content-security-policy|x-content-type|x-frame|referrer|strict-transport'
```

CSP, `nosniff`, `X-Frame-Options` and `Referrer-Policy` come from the binary and
are present with or without Traefik. `Strict-Transport-Security` comes from the
`mimostats-hsts` middleware in `compose.yaml` and is the one that disappears if
those labels are dropped. Each header must appear exactly ONCE: a duplicate
means Traefik is setting its own copy too, and two conflicting CSPs are enforced
as their intersection, which breaks the page rather than hardening it.

The first cycle runs immediately at startup rather than after a full interval,
so there is data within seconds of a deploy. Percentiles stay suppressed until
20 successful samples exist — roughly 100 minutes — and the dashboard says
"insufficient data" rather than showing a number until then. That is working as
intended, not a fault.

### The one check worth doing by hand

`reasoning_tokens` must be 0 on every sample. A non-zero value means thinking
came back on and every latency figure is measuring something else:

```sh
docker compose exec mimostats /usr/local/bin/mimostats -healthcheck && echo healthy
sqlite3 data/mimostats.db \
  'SELECT model_id, probe, ttft_ms, reasoning_tokens, cached_tokens, answer_ok
   FROM infer_probes ORDER BY id DESC LIMIT 8'
```

`cached_tokens` should also be at or near 0. On `wide` a rise means the
cache-defeat nonce stopped working and the prefill numbers have quietly become
cache lookups; on `short` it means the system message went missing and MiMo is
serving its own injected prompt from cache.

The `probe` column reads `short` from migration 0003 onward; databases last
written by an older build hold `infer` there instead. That CHECK is why the
rename is one-way.

A pre-0003 binary on a migrated database boots clean — 0003 is already recorded
— and then stores nothing at all. A cycle is written in ONE transaction, so the
short probe's row failing the constraint takes the whole cycle down with it: the
cycle row, both network readings and the wide run. The daemon logs `persist
cycle failed` every five minutes while everything already collected stays on the
dashboard, so it reads as a page that quietly stopped updating rather than as an
outage. Roll forward, not back.

```sh
docker compose logs mimostats | grep 'persist cycle failed'
```

### If the container crash-loops on first start

```sh
docker compose logs mimostats | tail -20
```

`unable to open database file` means `./data` is not writable by UID 1000 — see
the `chown` in the install steps above. `BACKEND_MIMO_API_KEY is required`
means `.env` was not filled in; compose fails before the container starts, by
design, because a container running without a key would record an unbroken wall
of auth failures that renders on the dashboard as a MiMo outage.

## Confirming the reference host from this box

The reference ping target is what stops a route problem, or an outage of our
own, from being published as a MiMo outage — so it has to be reachable **from
the probe host**, not from wherever it was last checked.

```sh
python3 - <<'PY'
import socket, statistics, time
for h in ["token-plan-sgp.xiaomimimo.com", "sgp1.digitaloceanspaces.com"]:
    try:
        ip = socket.getaddrinfo(h, 443, socket.AF_INET, socket.SOCK_STREAM)[0][4][0]
    except Exception as e:
        print(f"{h:34} DNS FAIL {e}"); continue
    r = []
    for _ in range(4):
        s = socket.socket(); s.settimeout(5); t = time.perf_counter()
        try:
            s.connect((ip, 443)); r.append((time.perf_counter() - t) * 1000)
        except Exception:
            pass
        finally:
            s.close()
    print(f"{h:34} {ip:16} " + (f"med={statistics.median(r):6.1f}ms n={len(r)}" if r else "UNREACHABLE"))
PY
```

Both must answer. If the Singapore reference does not, override it — a dead
reference does not create a false outage (attribution only consults it once
MiMo is already unreachable), but it costs the edge-vs-everything-else
distinction exactly when that distinction matters, and every unreachable cycle
then lands in the excluded bucket instead of being attributed:

```sh
echo 'BACKEND_PING_REF_SGP_HOST=<a real Singapore host>' >> .env && docker compose up -d
```

Pick a genuine Singapore endpoint, and verify it with the script above rather
than by name: several plausible-looking hostnames answer from anycast PoPs in
Europe, which would put a European host in the Singapore slot — the precise
failure this reference exists to detect. Bare resolver IPs are a poor choice
too: `1.1.1.1`, `9.9.9.9` and `8.8.8.8` are provisioned for DNS and their port
443 is filtered on many networks.

## Upgrading

```sh
docker compose pull && docker compose up -d
```

Migrations are embedded and run at boot, each in its own transaction. A
migration that fails rolls back and is not recorded, so a failed upgrade leaves
the database untouched rather than half-applied.

## Backup

The SQLite file is the only state:

```sh
sqlite3 data/mimostats.db ".backup '/tmp/mimostats-$(date +%F).db'"
```

Run this as a user that can write to `data/` — the database is in WAL mode, and
even a read needs to touch the `-shm` file. `sudo -u '#1000'` if in doubt.

Retention deletes cycles older than three months, so the history is bounded at
roughly 110k rows regardless of uptime.

## After a domain change

The site ships `robots.txt` and `sitemap.xml`, and neither does anything until
something is told to read them. Once the certificate is good:

- Add `https://ismimodown.com/` as a property in Google Search Console and
  submit `/sitemap.xml`.
- Re-scrape the link preview by pasting the URL into Slack or WhatsApp.
  WhatsApp and Telegram cache a card effectively forever, which is what the
  `?v=` on the og:image URL exists to defeat — bump it in the same commit as any
  regenerated `ui/public/og.png`, or the redraw reaches nobody who ever shared
  the old link.

## Hardening notes

The compose file runs the container non-root with `cap_drop: ALL`,
`no-new-privileges`, and a read-only root filesystem. The process opens TCP
sockets, speaks HTTPS and writes one SQLite file — it needs nothing else, so
nothing else is granted. `/tmp` is a small tmpfs because SQLite wants a
writable `TMPDIR`.

The healthcheck is the binary probing itself (`-healthcheck`), because the
runtime image is distroless and carries no shell or curl. Adding one so the
healthcheck could run would undo the reason the image is distroless.

If you edit that healthcheck, keep the **exec** form `["CMD", ...]`. The string
form — and `docker run --health-cmd` — are `CMD-SHELL`, which Docker runs
through `/bin/sh`; this image has no shell, so the check fails forever while the
service answers every request normally. A container that is working but reported
unhealthy is worse than one that is plainly down, because an orchestrator will
restart it in a loop.
