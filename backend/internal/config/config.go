// Package config loads ismimodown's runtime configuration from environment
// variables. Secrets come from the environment only — never from a file the
// repo could accidentally carry, since this repo is public and the MiMo
// token-plan key is a live billable credential.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

// Default probe endpoints and targets. These are the values validated by the
// pre-implementation curls; see docs/plan.html.
const (
	// DefaultBaseURL is the token-plan host the tp- key is issued against.
	DefaultBaseURL = "https://token-plan-sgp.xiaomimimo.com/v1"

	// DefaultRefSGPHost answers "is *any* Europe->Singapore path healthy".
	//
	// OVH, because the probe box is on OVH and a same-carrier path makes "the
	// route is fine" a stronger claim: an operator-specific backbone fault on
	// MiMo's own transit shows up here rather than hiding behind a different
	// carrier's healthy path.
	//
	// Two OVH names do NOT work and are recorded so they are not re-tested:
	// `ap-southeast-sgp` (and `s3.ap-southeast-sgp.io.cloud.ovh.net`) does not
	// resolve at all, and `sgp.ovh` answers from Cloudflare anycast in Europe
	// (~18 ms) — a Europe reference mislabelled as Singapore, which is exactly
	// the failure this reference exists to prevent. `sgp.proof.ovh.net` is the
	// real thing: 15.235.182.181, AS16276 OVH SAS, geolocated Singapore, and
	// ~271 ms TCP from Zurich against DigitalOcean sgp1's ~333 ms.
	//
	// The tradeoff: proof.ovh.net is OVH's public per-datacentre speedtest node.
	// A bare TCP handshake with no transfer is negligible load, but a speedtest
	// node is marginally more renumber-prone than a storage endpoint — which is
	// why this one host stays configurable while the rest of the probe shape
	// does not.
	DefaultRefSGPHost = "sgp.proof.ovh.net"

	// DefaultMimoSGPHost is the Singapore edge, the one the inference base URL
	// points at.
	//
	// A CONSTANT, and no longer BaseURL's hostname. It was derived, on the
	// reasoning that pinging one host while inferring against another would
	// report a path nobody is using — true, and it stopped being the whole
	// picture once a second region was probed. Derivation made one of the two
	// edge targets follow an operator setting while the other could not, so
	// pointing BACKEND_MIMO_BASE_URL at Amsterdam silently produced two
	// identical series labelled as a cross-region comparison.
	//
	// The tradeoff is explicit: a deployment that repoints the base URL now
	// keeps probing these two hosts, so the ping layer and the inference layer
	// can disagree about where MiMo is. That is the better failure — it is
	// visible in this file, where both targets are named, rather than emergent
	// from a URL three settings away. Both edges say where Xiaomi is; neither
	// says where we run.
	DefaultMimoSGPHost = "token-plan-sgp.xiaomimimo.com"

	// DefaultMimoAMSHost is Xiaomi's other front for the same service.
	//
	// Not configurable, for the same reason the Singapore edge above is not: it
	// says where Xiaomi is, not where we run. It resolves to
	// mimo-pri-azams.alb.xiaomi.com — `azams`, Azure Amsterdam — which is what
	// makes it a genuinely different edge rather than the Singapore one under
	// another name. Two A records, no AAAA, same as the Singapore edge.
	//
	// Nothing infers against it. It is charted beside Singapore so a reader can
	// see whether a slowdown is Xiaomi-wide or specific to one region, and it
	// never feeds a verdict — see probe.AttributeFault.
	DefaultMimoAMSHost = "token-plan-ams.xiaomimimo.com"

	// DefaultRefAMSHost answers "is *any* Europe->Amsterdam path healthy", the
	// Amsterdam counterpart to DefaultRefSGPHost.
	//
	// Akamai/Linode's Amsterdam speedtest node, a CNAME to
	// speedtest-1.ams2.nl.prod.linode.com. Not OVH this time: OVH publishes no
	// proof.ovh.net node in Amsterdam (`nl.proof.ovh.net` does not resolve), so
	// the same-carrier argument that picked the Singapore reference has nothing
	// to select here, and a different carrier is the honest second choice.
	//
	// Its one weakness, and the reason it stays configurable: a SINGLE A record,
	// against the Singapore reference's rotation. One host down reads as an
	// Amsterdam route problem. `ams.speedtest.clouvider.net` (194.127.172.176)
	// is the documented fallback; DEPLOY.md tells an operator how to switch.
	DefaultRefAMSHost = "speedtest.amsterdam.linode.com"
)

// DefaultUserAgent impersonates opencode, because MiMo's token-plan endpoint is
// an opencode-facing product and a neutral UA is not what production traffic
// looks like. Overridable via BACKEND_PROBE_USER_AGENT.
const DefaultUserAgent = "opencode/1.18.11 ai-sdk/openai-compatible/3.0.20 ai-sdk/provider-utils/5.0.18 runtime/bun/1.3.14"

// DefaultSystemPrompt is sent on every probe request, and is NOT cosmetic.
//
// When a request carries no system message, MiMo injects its own — measured at
// 250 prompt tokens, of which 192 came back as `cached_tokens`. That breaks the
// probe twice over: a "~40 token" short request actually costs ~263 tokens
// (blowing the cost model by ~6.5x), and most of the prefill it is supposed to
// be timing is served from cache instead of computed. Supplying any system
// message suppresses the injection entirely: prompt_tokens drops to 20 and
// cached_tokens comes back null.
//
// config_test.go pins this, because losing it re-introduces the fault silently —
// the probe keeps working and only the numbers become wrong.
const DefaultSystemPrompt = "You are a helpful assistant."

// DefaultModels are the two probed models, confirmed served on the tp- key by
// the pre-implementation curl against /v1/models.
//
// mimo-v2.5 is the omnimodal model; mimo-v2.5-pro is the 1T/42B-active text
// flagship. Different weight classes, so latency between them is comparable and
// quality is not — which the UI must say. The -asr and -tts variants the
// endpoint also serves are not chat models and are deliberately absent.
//
// Not configurable. The registry still takes the list as a parameter, so adding
// a model or a second vendor stays a small change here rather than an
// architectural one — but it is a change to this line, reviewed with the price
// table and the UI copy that name these two models, not a deployment-time knob
// that can silently disagree with any of them.
var DefaultModels = []string{"mimo-v2.5", "mimo-v2.5-pro"}

// ModelPrice is one model's list price, in USD per MILLION tokens.
//
// Three rates, because the bill has three parts and two of them are nested
// inside a third: cached_tokens is a SUBSET of prompt_tokens, so the input side
// is (prompt - cached) at In plus cached at Cached. reasoning_tokens gets no
// rate at all — it is already inside output_tokens, and giving it one would bill
// it twice.
type ModelPrice struct {
	In     float64 `json:"in_per_mtok"`
	Out    float64 `json:"out_per_mtok"`
	Cached float64 `json:"cached_per_mtok"`
}

// DefaultPrices is what a million tokens costs for each probed model, in USD.
//
// Source: LiteLLM's model_prices_and_context_window.json, read 2026-08-05, for
// openrouter/xiaomi/mimo-v2.5 and openrouter/xiaomi/mimo-v2.5-pro. Vendored
// rather than fetched: the container is distroless and offline by design, and a
// third party editing a number should not silently change a figure this
// dashboard publishes as its own cost. Edit this table when the rates move.
//
// These are MiMo's published per-token rates, and what the panel reports is the
// tokens this dashboard actually spent priced against them. It is the right
// order of magnitude and the wrong document to argue with an accountant about:
// nothing here sees a real invoice, so a rate that has moved since the date
// above is wrong everywhere at once and silently.
//
// Every model in DefaultModels MUST have an entry here. Nothing downstream
// tolerates a missing one any more: /api/cost prices every row it finds, so a
// gap would quietly drop those runs out of a total that still presents itself
// as complete. config_test.go pins the two lists against each other.
var DefaultPrices = map[string]ModelPrice{
	"mimo-v2.5":     {In: 0.40, Out: 2.00, Cached: 0.08},
	"mimo-v2.5-pro": {In: 1.00, Out: 3.00, Cached: 0.20},
}

// OffPeakCoefficient is MiMo's reduced-rate multiplier, applied to tokens spent
// between OffPeakStartUTCHour and midnight UTC.
//
// Applied per phase and never to a window total: price each phase's tokens at
// the full rate, discount the off-peak share, then add. Discounting a total
// that already mixes both phases charges the reduction against runs that never
// earned it.
const OffPeakCoefficient = 0.8

// OffPeakStartUTCHour opens the reduced-rate window. It closes at 24:00 UTC.
//
// 00:00-08:00 Beijing, and Beijing is UTC+8 with no DST, so the window is
// exactly 16:00-24:00 UTC every day. Held in UTC and never in local time: the
// probe host reads Europe/Zurich, which does observe DST, so the same window
// lands at 18:00-02:00 in summer and 17:00-01:00 in winter.
//
// This is a BILLING window and nothing else. MiMo publishes no load or demand
// figures, so nothing derived from it may be presented as a claim about when the
// platform is busy.
const OffPeakStartUTCHour = 16

// Retention bounds how far back raw samples are kept: 3 months, swept nightly.
// No rollups — a sample outside this window is deleted, not aggregated.
//
// This is the only thing bounding database growth. At one cycle every 5 minutes
// against two models the window holds ~110k rows, which is small; the number is
// a decision about how much history the charts can show, not a disk budget.
const Retention = 2160 * time.Hour

// The timeout ladder.
//
// A single outer deadline records "slow" and "dead" identically, so each layer
// gets its own bound and its own error class:
//
//	PingTimeout   ->  connect_timeout   TCP handshake to the ping targets
//	DialTimeout   ->  connect_timeout   dial + TLS for inference
//	HeaderTimeout ->  header_timeout    response headers
//	TTFTTimeout   ->  ttft_timeout      no first token at all
//	IdleTimeout   ->  stalled           silence between chunks mid-stream
//	ProbeTimeout  ->  timeout           overall deadline
//
// TTFTTimeout and IdleTimeout MUST stay below ProbeTimeout or they can never
// fire, and every failure collapses back into the single "timeout" class the
// ladder exists to split apart. config_test.go asserts both — the check used to
// run at boot against environment input, and became unreachable when these
// became constants, so the invariant moved rather than disappeared.
//
// Raising any of these moves the point at which a slow run stops being a
// latency sample and becomes a censored one, which the dashboard's censoring
// note describes. That is a change to what the page measures, not a tuning
// knob, which is why it lives here and not in the environment.
const (
	PingTimeout   = 5 * time.Second
	DialTimeout   = 10 * time.Second
	HeaderTimeout = 60 * time.Second
	TTFTTimeout   = 150 * time.Second
	IdleTimeout   = 45 * time.Second
	ProbeTimeout  = 240 * time.Second
)

// Config holds all runtime settings.
type Config struct {
	Addr     string // HTTP listen address
	DBPath   string // path to the SQLite file
	LogLevel string

	// MiMo endpoint. BaseURL is the inference host; APIKey is required at boot
	// so a misconfigured deployment fails loudly instead of recording an
	// unbroken wall of auth failures as a MiMo outage.
	BaseURL string
	APIKey  string

	// Models probed every cycle. Always DefaultModels — a field rather than a
	// direct reference to the constant so the registry stays injectable in tests.
	Models []string

	// Probe targets for the TCP ping layer.
	//
	// Two regions, each an edge paired with an independent reference.
	//
	// Both EDGES are constants and neither is configurable: where Xiaomi puts
	// its front doors is not a deployment's choice, and making one of them
	// follow an operator setting is what let the two collapse onto the same
	// host. See DefaultMimoSGPHost for the derivation this replaced.
	//
	// The two REFERENCES are the hosts a deployment can point elsewhere, because
	// they are third-party speedtest nodes and the more renumber-prone half of
	// each pair.
	MimoSGPHost string
	RefSGPHost  string
	MimoAMSHost string
	RefAMSHost  string

	// ProbeUserAgent and ProbeSystemPrompt shape the outgoing inference request.
	ProbeUserAgent    string
	ProbeSystemPrompt string

	// Prices is the per-model list price, keyed by model id. Always
	// DefaultPrices, and always covering every entry in Models.
	Prices map[string]ModelPrice

	// Retention and the timeout ladder, copied from the constants above. Fields
	// rather than package-level reads so a test can drive a scheduler or a probe
	// at a timescale that does not take four minutes.
	Retention time.Duration

	PingTimeout   time.Duration // TCP handshake
	DialTimeout   time.Duration // dial + TLS for inference
	HeaderTimeout time.Duration // response headers
	TTFTTimeout   time.Duration // no first token at all
	IdleTimeout   time.Duration // silence between chunks mid-stream
	ProbeTimeout  time.Duration // overall deadline
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// Load reads configuration from the environment, applying defaults.
//
// Eight variables reach this function. Everything else about how ismimodown probes
// — the model pair, the price table, the system prompt, the retention window,
// the whole timeout ladder — is a constant above, because it describes what the
// dashboard measures rather than where it runs. A deployment that could change
// those could publish a differently-shaped number under the same page.
func Load() (Config, error) {
	cfg := Config{
		Addr: env("BACKEND_ADDR", ":8080"),
		// Still mimostats.db, and deliberately so: this is the one name the
		// rename to ismimodown left alone. A running deployment bind-mounts
		// ./data, and its entire history lives in this file — renaming the
		// default orphans it, silently, on the next restart. config_test
		// pins the string. Do not "finish" this one.
		DBPath:            env("BACKEND_DB_PATH", "/data/mimostats.db"),
		LogLevel:          env("BACKEND_LOG_LEVEL", "info"),
		BaseURL:           env("BACKEND_MIMO_BASE_URL", DefaultBaseURL),
		APIKey:            env("BACKEND_MIMO_API_KEY", ""),
		RefSGPHost:        env("BACKEND_PING_REF_SGP_HOST", DefaultRefSGPHost),
		MimoSGPHost:       DefaultMimoSGPHost,
		MimoAMSHost:       DefaultMimoAMSHost,
		RefAMSHost:        env("BACKEND_PING_REF_AMS_HOST", DefaultRefAMSHost),
		ProbeUserAgent:    env("BACKEND_PROBE_USER_AGENT", DefaultUserAgent),
		ProbeSystemPrompt: DefaultSystemPrompt,
		Models:            append([]string(nil), DefaultModels...),
		Prices:            defaultPrices(),
		Retention:         Retention,
		PingTimeout:       PingTimeout,
		DialTimeout:       DialTimeout,
		HeaderTimeout:     HeaderTimeout,
		TTFTTimeout:       TTFTTimeout,
		IdleTimeout:       IdleTimeout,
		ProbeTimeout:      ProbeTimeout,
	}

	if cfg.APIKey == "" {
		return Config{}, fmt.Errorf("BACKEND_MIMO_API_KEY is required")
	}

	// Validated for its own sake, not for a hostname any more.
	//
	// The returned host used to become the Singapore ping target; both edges are
	// constants now (see DefaultMimoSGPHost). Everything else this check does is
	// still load-bearing and has nothing to do with pinging: it refuses a URL
	// carrying userinfo or a query string, either of which would carry a live
	// tp- key wherever BaseURL travels, and it refuses a non-http scheme or a
	// missing host outright.
	if _, err := validateBaseURL(cfg.BaseURL); err != nil {
		return Config{}, err
	}

	if err := validateRefHost(cfg.RefSGPHost, "BACKEND_PING_REF_SGP_HOST"); err != nil {
		return Config{}, err
	}
	if err := validateRefHost(cfg.RefAMSHost, "BACKEND_PING_REF_AMS_HOST"); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// validateRefHost checks one configurable reference ping target.
//
// Shared by both regions rather than written twice: the two variables are the
// same kind of value, and a guard that drifted between them would leave one
// region accepting a host the other rejects.
//
// A host:port here would be dialled as "host:port:443". Catch it at boot rather
// than as a permanently-failing ping that reads as an outage.
//
// A bare IPv6 literal is REJECTED, and this is a change: it used to be exempted
// from the colon test on the grounds that net.ParseIP tells "2606:4700:4700::1111"
// apart from "example.com:443" exactly, which it does. What changed is
// downstream — probe.Pinger now resolves and dials IPv4 only, so a v6 literal
// can never be probed at all. Accepting it would hand the operator precisely the
// permanently-failing ping this guard exists to prevent, and the colon test
// catches it for free. A v4 literal is still fine and still documented.
func validateRefHost(host, envName string) error {
	if host == "" {
		return fmt.Errorf("%s must not be empty", envName)
	}
	if strings.Contains(host, "/") || strings.Contains(host, ":") {
		return fmt.Errorf(
			"%s must be a bare hostname or IPv4 address without scheme or port "+
				"(the TCP probe is IPv4-only, so an IPv6 literal can never be reached)",
			envName)
	}
	return nil
}

// defaultPrices copies DefaultPrices, so a caller holding a Config cannot reach
// through it and mutate the package-level table every other caller shares.
func defaultPrices() map[string]ModelPrice {
	out := make(map[string]ModelPrice, len(DefaultPrices))
	for k, v := range DefaultPrices {
		out[k] = v
	}
	return out
}

// validateBaseURL checks the one URL a deployment supplies and returns its
// hostname, which is also the TCP ping target for MiMo's edge — see Load.
//
// The hostname comes back from here rather than being parsed again at the call
// site so there is exactly one url.Parse of this value, and the thing that gets
// pinged is provably the thing that passed these checks.
func validateBaseURL(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("BACKEND_MIMO_BASE_URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("BACKEND_MIMO_BASE_URL is not a valid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("BACKEND_MIMO_BASE_URL must be an http or https URL")
	}
	// Hostname(), not just Host: "https://:8443/v1" parses with a non-empty Host
	// of ":8443" and an EMPTY hostname, which would be handed back as the derived
	// ping target. The retired BACKEND_PING_MIMO_HOST had its own non-empty
	// guard; deriving the host is what makes this the place for it.
	if u.Host == "" || u.Hostname() == "" {
		return "", fmt.Errorf("BACKEND_MIMO_BASE_URL must include a host")
	}
	// Userinfo is a credential. "https://user:pass@host/v1" passes every other
	// check here, so without this line a secret typed into the base URL would
	// travel wherever this value travels — logs, error messages, and any future
	// consumer of the config. Rejected at boot rather than stripped later: an
	// operator who put a secret here must be told, not silently have it dropped
	// from one of the several places it would reach.
	if u.User != nil {
		return "", fmt.Errorf("BACKEND_MIMO_BASE_URL must not embed credentials; use BACKEND_MIMO_API_KEY")
	}
	// A query string is not a credential by construction, but it is where an
	// API key most often ends up on an OpenAI-compatible endpoint. Same
	// reasoning.
	if u.RawQuery != "" {
		return "", fmt.Errorf("BACKEND_MIMO_BASE_URL must not carry a query string")
	}
	// Hostname(), not Host: probe.Pinger appends :443 itself, so a port left on
	// here would be dialled as "host:port:443".
	return u.Hostname(), nil
}
