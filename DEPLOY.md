# Deploying mimostats

One container behind an existing Traefik. The whole thing is a single Go binary
with the dashboard embedded, plus one SQLite file.

## Prerequisites

- Docker with Compose, and a Traefik instance already terminating TLS.
- An external network shared with Traefik:

  ```sh
  docker network create traefik    # if it does not already exist
  ```

- A DNS record for `mimostats.trick77.com` pointing at the host.
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
curl -s https://mimostats.trick77.com/healthz
```

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
cache lookups; on `infer` it means the system message went missing and MiMo is
serving its own injected prompt from cache.

### If the container crash-loops on first start

```sh
docker compose logs mimostats | tail -20
```

`unable to open database file` means `./data` is not writable by UID 1000 — see
the `chown` in the install steps above. `BACKEND_MIMO_API_KEY is required`
means `.env` was not filled in; compose fails before the container starts, by
design, because a container running without a key would record an unbroken wall
of auth failures that renders on the dashboard as a MiMo outage.

## Confirming the reference hosts from this box

The two reference ping targets are what stop a route problem, or an outage of
our own, from being published as a MiMo outage — so they have to be reachable
**from the probe host**, not from wherever they were last checked.

```sh
python3 - <<'PY'
import socket, statistics, time
for h in ["token-plan-sgp.xiaomimimo.com", "sgp1.digitaloceanspaces.com", "cloudflare.com"]:
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

All three must answer. If the Europe reference does not, override it — a dead
reference does not create a false outage (attribution only consults it once
MiMo is already unreachable), but it destroys the route-vs-uplink distinction
exactly when that distinction matters:

```sh
echo 'BACKEND_PING_REF_EU_HOST=one.one.one.one' >> .env && docker compose up -d
```

Bare resolver IPs are a poor choice here: `1.1.1.1`, `9.9.9.9` and `8.8.8.8`
are provisioned for DNS and their port 443 is filtered on many networks.

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
