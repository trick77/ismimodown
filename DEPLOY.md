# Deploying ismimodown

Everything is called `ismimodown` except the database file, which is still
`mimostats.db` — the project's earlier name, kept so an existing deployment's
history is not orphaned. Both names appear below and neither is a typo.

One container behind an existing Traefik. The whole thing is a single Go binary
with the dashboard embedded, plus one SQLite file.

## Prerequisites

- Docker with Compose, and a Traefik instance already terminating TLS.
- An external network shared with Traefik:

  ```sh
  docker network create traefik    # if it does not already exist
  ```

- **Two** DNS A records pointing at the host: `ismimodown.com` and
  `www.ismimodown.com`. www exists only to redirect permanently (308) to the
  apex, but it needs its own record and its own certificate — see below.
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
git clone git@github.com:trick77/ismimodown.git
cd ismimodown
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

The image comes from `ghcr.io/trick77/ismimodown:latest`, published by the
release workflow on every push to `master` that touches `backend/` or `ui/`.

**The key is billable.** `.env` is gitignored, along with every `.env.*`
variant, because this repo is public. Never put a real key in `.env.example`.

## Verifying

```sh
docker compose ps                        # healthy within ~30s
docker compose logs -f ismimodown        # "cycle complete" every ~5 minutes
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
curl -sSI https://www.ismimodown.com/ | head -3   # 308, location: https://ismimodown.com/
```

**308, not 301.** Traefik answers a permanent `redirectregex` with 308; both are
permanent and both consolidate the two hostnames the same way for Google and
Bing. The path is preserved, so `https://www.…/sitemap.xml` lands on the apex
copy rather than on the apex root — that is the middleware working, and it is
the thing to check if a redirect ever looks suspect.

Response headers, after a Traefik reload picks up the new labels:

```sh
curl -sSI https://ismimodown.com/ | \
  grep -Ei 'content-security-policy|x-content-type|x-frame|referrer|strict-transport'
```

One header is path-dependent: `Cross-Origin-Resource-Policy` is `same-origin`
everywhere except `/og.png`, which is `cross-origin` because the link-preview
card is the one response another origin is meant to embed.

```sh
curl -sSI https://ismimodown.com/og.png | grep -i cross-origin-resource
# cross-origin-resource-policy: cross-origin
```

CSP, `nosniff`, `X-Frame-Options` and `Referrer-Policy` come from the binary and
are present with or without Traefik. `Strict-Transport-Security` comes from the
`ismimodown-hsts` middleware in `compose.yaml` and is the one that disappears if
those labels are dropped. Each header must appear exactly ONCE: a duplicate
means Traefik is setting its own copy too, and two conflicting CSPs are enforced
as their intersection, which breaks the page rather than hardening it.

## Exploit-path bans

A request for a path only an exploit scan asks for blocks that caller for **48
hours** with a bare `403`. Not a rate limit: there is no budget and no refill,
because this binary serves no PHP, no admin panel and no dotfiles, so there is
no honest request to protect. Four rules, in
`backend/internal/httpapi/exploitpaths.go`:

- **any dotfile** — `/.env.prod`, `/.htaccess`, `/.git/config`, `/.DS_Store`.
  One rule rather than a list, because a list never finishes: the first version
  named `/.env` and `/.env.local` and let `/.env.prod` straight through.
  `/.well-known` is the sole carve-out, so a future `security.txt` still works.
- **an extension this binary cannot serve** — `.php`, `.asp(x)`, `.jsp`, `.cgi`,
  `.sql`, `.bak`, `.pem`.
- **a known segment at ANY depth** — `wp-admin`, `wp-includes`, `wp-content`,
  `wp-json`, `vendor`, `cgi-bin`, `phpmyadmin`, `actuator`, `solr` and friends.
  Depth matters: the WordPress scanners prepend a guess at the install root, so
  a single pass is `/blog/wp-includes/…`, `/wordpress/wp-includes/…`,
  `/2018/wp-includes/…`. Matching only at the front missed eleven of fourteen.
- **a handful of exact paths** — `/config.json`, `/credentials`,
  `/server-status`.

The 404 budget (`notFoundPenalty`) runs underneath, and charges a narrower set
than "any wrong guess". Three kinds of 404 cost nothing:

- **Images** — `.ico`, `.png`, `.svg`. A browser asks for `/favicon.ico` and
  `/apple-touch-icon.png` on its own, without the page naming them.
- **`/.well-known/*`** — Chrome probes `/.well-known/traffic-advice` on
  navigations and its devtools asks for an `appspecific` JSON file.
- **Anything extensionless** — `/admin`, `/status`, `/about`. These returned
  200 until the soft-404 fix, so they have never been charged; keeping them free
  is what stops a search engine recrawling an old soft-404 URL from spending the
  budget and being 429'd off `/` and `/robots.txt` next.

What is left, and what the budget is really for, is a 404 with a non-image
extension: `.php`, `.env`, `.bak`, `.sql`, `.aspx` — the shape of a wordlist,
and nothing a browser asks for unprompted. Extensionless exploit names are not
a gap: `banGate` answers those with a `403` on the first request.

Going back to scanning while banned **resets the block to a fresh 48 hours** from
that moment, so a scanner has to actually stop for two days to get back in.
Ordinary requests from a banned caller are refused but do NOT renew the ban —
deliberately, since a key is an address and an address can be a NAT pool: one
device asking for `/wp-login.php` bans every other, and a bystander with the
dashboard open would otherwise have their own reconnecting tab renew the ban
forever.

Bans live in memory only. **Restarting the container clears every one of them**,
which is the only escape hatch there is:

```sh
docker compose restart ismimodown
```

**This bans you too.** `curl https://ismimodown.com/.env` against production
locks your own address out for 48 hours. Either wait it out or restart the
container.

The line to look for:

```sh
docker compose logs ismimodown | grep 'banned caller'
# {"level":"WARN","msg":"banned caller for exploit path","client":"165.245.182.166",
#  "path":"/wp-login.php","ban":"new","until":"2026-08-09T10:00:36Z"}
```

`ban` is `new` on the first offence and `extended` on a return visit. Repeat
lines for the same caller are throttled to one an hour — a banned scanner does
not stop knocking, and one line per refusal would bury the ban that started it.
The ordinary request log still records every `403`.

At most 10,000 addresses are held; past that the soonest-to-expire is evicted so
the newest ban always lands. Well beyond real volume — the logs run to a few
dozen such requests a day — but the callers this catches rotate addresses.

Note this is per address, so it does **not** stop a scanner arriving through
Cloudflare Workers: those rotate across Cloudflare's edge ranges and present a
different address every few minutes. It works against direct-to-origin scanners.

The first cycle runs immediately at startup rather than after a full interval,
so there is data within seconds of a deploy. Percentiles stay suppressed until
20 successful samples exist — roughly 100 minutes — and the dashboard says
"insufficient data" rather than showing a number until then. That is working as
intended, not a fault.

### The one check worth doing by hand

`reasoning_tokens` must be 0 on every sample. A non-zero value means thinking
came back on and every latency figure is measuring something else:

```sh
docker compose exec ismimodown /usr/local/bin/ismimodown -healthcheck && echo healthy
sqlite3 data/mimostats.db \
  'SELECT model_id, ttft_ms, reasoning_tokens, cached_tokens, answer_ok
   FROM infer_probes ORDER BY id DESC LIMIT 8'
```

`cached_tokens` should also be at or near 0. A rise means the system message
went missing and MiMo is serving its own injected prompt from cache.

The `probe` column is gone from migration 0006 onward, and so are the `wide`
rows it distinguished — see that migration for why the measurement they served
could not be supported. **0006 deletes data and there is no way back.** Take the
backup below before deploying a build that carries it.

An older binary on a migrated database boots clean — the migration is already
recorded — and then stores nothing at all, because its INSERT names a column
that no longer exists. A cycle is written in ONE transaction, so that failure
takes the whole cycle with it: the cycle row and both network readings. The
daemon logs `persist cycle failed` every five minutes while everything already
collected stays on the dashboard, so it reads as a page that quietly stopped
updating rather than as an outage. Roll forward, not back.

```sh
docker compose logs ismimodown | grep 'persist cycle failed'
```

### If the container crash-loops on first start

```sh
docker compose logs ismimodown | tail -20
```

`unable to open database file` means `./data` is not writable by UID 1000 — see
the `chown` in the install steps above. `BACKEND_MIMO_API_KEY is required`
means `.env` was not filled in; compose fails before the container starts, by
design, because a container running without a key would record an unbroken wall
of auth failures that renders on the dashboard as a MiMo outage.

## Confirming the reference hosts from this box

The reference ping targets are what stop a route problem, or an outage of our
own, from being published as a MiMo outage — so they have to be reachable **from
the probe host**, not from wherever they were last checked.

Four targets, in two pairs: an edge and its independent reference per region.
The script forces `AF_INET` because the probe itself is IPv4-only.

```sh
python3 - <<'PY'
import socket, statistics, time
for h in ["token-plan-sgp.xiaomimimo.com", "sgp.proof.ovh.net",
          "token-plan-ams.xiaomimimo.com", "speedtest.amsterdam.linode.com"]:
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

All four must answer. The two that matter differently:

**Singapore** is the region every verdict on the page is about. If its reference
does not answer, override it — a dead reference does not create a false outage
(attribution only consults it once MiMo is already unreachable), but it costs
the edge-vs-everything-else distinction exactly when that distinction matters,
and every unreachable cycle then lands in the excluded bucket instead of being
attributed:

```sh
echo 'BACKEND_PING_REF_SGP_HOST=<a real Singapore host>' >> .env && docker compose up -d
```

**Amsterdam** feeds no verdict at all. It is charted in "The wire itself" and
never summarised, never attributed — so a dead Amsterdam reference costs a chart
line and nothing else. It is still the likelier of the two to need overriding,
because `speedtest.amsterdam.linode.com` resolves to a SINGLE address rather
than a rotation, so one host down takes the whole series with it:

```sh
echo 'BACKEND_PING_REF_AMS_HOST=ams.speedtest.clouvider.net' >> .env && docker compose up -d
```

That fallback is tested: `194.127.172.176`, answering on 443 from Amsterdam.

These two are the only probe targets with a setting. Both of MiMo's own ping
hosts are constants — `token-plan-sgp.xiaomimimo.com` and
`token-plan-ams.xiaomimimo.com` (a CNAME to `mimo-pri-azams.alb.xiaomi.com`) —
and neither can be pointed elsewhere.

The Singapore edge used to be derived from `BACKEND_MIMO_BASE_URL`'s hostname.
It is not any more: with two regions that made one edge target follow an
operator setting while the other could not, so pointing the base URL at
Amsterdam produced two identical lines labelled as a cross-region comparison.
The consequence to know about is that **repointing `BACKEND_MIMO_BASE_URL` no
longer moves the ping targets** — inference and the wire chart would then be
measuring different hosts. If you repoint it, the ping constants in
`backend/internal/config/config.go` need changing to match.

Both references must be a hostname or an **IPv4** address. The probe resolves
and dials A records only, so that all four numbers measure the same kind of
path; an IPv6 literal is refused at boot rather than becoming a ping that fails
forever.

The Singapore default is `sgp.proof.ovh.net`, OVH's Singapore speedtest node — same
carrier as the probe box, which is what makes "the route is fine" a claim about
MiMo's own transit rather than about some other network's. Replace it if you
deploy somewhere OVH is not the relevant path, or if it stops answering.

Pick a genuine endpoint in the region you are replacing, and verify it with the script above rather
than by name: several plausible-looking hostnames answer from anycast PoPs in
Europe, which would put a European host in the Singapore slot — the precise
failure this reference exists to detect. `sgp.ovh` is exactly this trap; it is
Cloudflare anycast and answers from Europe in ~18 ms. Bare resolver IPs are a
poor choice too: `1.1.1.1`, `9.9.9.9` and `8.8.8.8` are provisioned for DNS and
their port 443 is filtered on many networks.

## Upgrading

```sh
docker compose pull && docker compose up -d
```

Migrations are embedded and run at boot, each in its own transaction. A
migration that fails rolls back and is not recorded, so a failed upgrade leaves
the database untouched rather than half-applied.

### One-time: check your reference hosts before upgrading to the Amsterdam build

The release that added the Amsterdam probes also pinned the TCP handshake to
IPv4, and with it made `validateRefHost` **reject a bare IPv6 literal**. That
value was accepted before, and `.env.example` used to advertise it.

This fails at BOOT, not at probe time — the container will not start:

```
BACKEND_PING_REF_SGP_HOST must be a bare hostname or IPv4 address without
scheme or port (the TCP probe is IPv4-only, so an IPv6 literal can never
be reached)
```

Before pulling, check:

```sh
grep -E '^BACKEND_PING_REF_(SGP|AMS)_HOST=' .env
```

If either carries an IPv6 literal, replace it with a hostname or an IPv4
address. A hostname is the better answer — the probe resolves A records and
walks the whole rotation.

The hard failure is deliberate. Accepting a v6 literal after the IPv4 pin would
mean a reference that can never be reached, which reads as a permanent outage of
the instrument rather than as the misconfiguration it is.

### Moving a host off the old `mimostats` checkout

A host that was deployed before the rename needs one manual cutover, because
compose derives its project name from the **directory**: cloning into
`ismimodown/` creates a second project, and `docker compose down` in the new
directory will not stop the container the old one started.

```sh
cd mimostats && docker compose down     # from the OLD directory, first
cd .. && git clone git@github.com:trick77/ismimodown.git
mv mimostats/data mimostats/.env ismimodown/
cd ismimodown && docker compose up -d
```

`./data` is a bind mount and the file inside it is still `mimostats.db`, so the
history carries over untouched. Renaming the compose service from `mimostats`
to `ismimodown` also recreates the container — expected, and harmless for the
same reason.

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
- Do the same in **Bing Webmaster Tools**. Bing does not read Google's index and
  will not find a site of this size on its own in any useful time; it was
  missing from this list for a while and the site was absent from Bing while
  ranking on Google, which is exactly the shape that symptom takes: indexed by
  the engine that was told about the site, invisible to the one that was not.
- Then check `bingbot` is not being throttled, because the two limiters in front
  of the site can do that silently:

  ```sh
  docker compose logs ismimodown --since 168h | grep -E '"status":(403|429)' | head -50
  ```

  Match on the JSON field, not on the bare number: `grep 429` also hits a
  `"dur":"429ms"` and any client address containing `4.29`.

  The request log carries `method`, `path`, `status`, `dur` and `client` — and
  deliberately no user-agent, so nothing in the line says "bingbot". Identify
  the caller from `client`: reverse-DNS it (`dig -x`, which for a genuine
  bingbot resolves under `search.msn.com`) or check it against Microsoft's
  published bingbot ranges. A `429` or `403` against an address that resolves
  that way means `notFoundPenalty` or `banGate` cut the crawler off — see the
  two sections above. The 404 budget is five chargeable misses refilling at one
  per 30s — images and `/.well-known` are free — and it gates *every* subsequent
  request from that caller, `/` and `/robots.txt` included. Nothing in the app
  knows what a crawler is, deliberately, so this is a log question rather than a
  setting.
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
