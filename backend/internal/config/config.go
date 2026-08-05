// Package config loads mimostats' runtime configuration from environment
// variables. Secrets come from the environment only — never from a file the
// repo could accidentally carry, since this repo is public and the MiMo
// token-plan key is a live billable credential.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Default probe endpoints and targets. These are the values validated by the
// pre-implementation curls; see docs/plan.html.
const (
	// DefaultBaseURL is the token-plan host the tp- key is issued against.
	DefaultBaseURL = "https://token-plan-sgp.xiaomimimo.com/v1"

	// DefaultMimoHost is the TCP ping target for MiMo's own edge. It is derived
	// from DefaultBaseURL by default, but stays overridable so the ping target
	// and the inference host can be pointed apart during testing.
	DefaultMimoHost = "token-plan-sgp.xiaomimimo.com"

	// DefaultRefSGPHost answers "is *any* Europe->Singapore path healthy".
	//
	// The plan originally named an OVH Singapore endpoint, because the probe box
	// is on OVH and a same-carrier path makes "the route is fine" a stronger
	// claim. No such public hostname exists: `ap-southeast-sgp` does not resolve,
	// and `sgp.ovh` answers from Cloudflare anycast in Europe (~18 ms), which
	// would be a Europe reference mislabelled as Singapore — exactly the failure
	// the reference exists to prevent. DigitalOcean's Singapore endpoint is a
	// genuine SGP target (~268 ms from Zurich), at the cost of the same-carrier
	// argument: an operator-specific backbone fault on MiMo's own path may not
	// show up here.
	DefaultRefSGPHost = "sgp1.digitaloceanspaces.com"
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
// Configurable so a second vendor is a config change rather than an
// architectural one, which is what makes the single-vendor scope a decision
// rather than a limitation.
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
// dashboard publishes as its own cost. Overridable via BACKEND_PRICES when they
// move.
//
// These are LIST rates for these models, not an invoice. The probe runs against
// a token plan, which consumes credits rather than dollars, so the panel reads
// "at list" and never claims to be a bill. It is the right order of magnitude
// and the wrong document to argue with an accountant about.
var DefaultPrices = map[string]ModelPrice{
	"mimo-v2.5":     {In: 0.40, Out: 2.00, Cached: 0.08},
	"mimo-v2.5-pro": {In: 1.00, Out: 3.00, Cached: 0.20},
}

// PricesOff disables pricing outright, for a deployment that would rather show
// nothing than show a list-rate estimate. Any other value is parsed as a table.
const PricesOff = "none"

// OffPeakCoefficient is MiMo's reduced-rate multiplier, applied to credits
// consumed between OffPeakStartUTCHour and midnight UTC.
//
// It multiplies the CREDITS, not the dollars: price the tokens at list, apply
// this to the off-peak share, then convert. The other order rounds in the wrong
// place.
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

	// Models probed every cycle.
	Models []string

	// Probe targets for the TCP ping layer.
	MimoHost   string
	RefSGPHost string

	// ProbeUserAgent and ProbeSystemPrompt shape the outgoing inference request.
	ProbeUserAgent    string
	ProbeSystemPrompt string

	// Prices is the per-model list price, keyed by model id. DefaultPrices
	// unless BACKEND_PRICES says otherwise, and empty when it says "none" — in
	// which case /api/cost serves token counts with no money in them and the
	// dashboard hides the panel.
	Prices map[string]ModelPrice

	// Retention bounds how far back raw samples are kept. Swept nightly.
	Retention time.Duration

	// The timeout ladder. A single outer deadline records "slow" and "dead"
	// identically, so each layer has its own bound and its own error class.
	PingTimeout   time.Duration // TCP handshake
	DialTimeout   time.Duration // dial + TLS for inference
	HeaderTimeout time.Duration // response headers
	TTFTTimeout   time.Duration // no first token at all
	IdleTimeout   time.Duration // silence between chunks mid-stream
	ProbeTimeout  time.Duration // overall deadline
}

// UnpricedModels names the probed models the price table has no entry for, in
// the order they are probed.
//
// Not an error: the daemon's job is to keep probing, and a missing price costs
// nothing but a panel. It is worth a line in the log, though — now that prices
// ship by default, the failure mode is a cost panel that silently disappears
// because BACKEND_MODELS named a model the shipped table has never heard of, and
// nothing on the page can say so.
func (c Config) UnpricedModels() []string {
	// Nil prices is pricing turned off, which is a decision rather than an
	// oversight and has nothing to warn about.
	if len(c.Prices) == 0 {
		return nil
	}
	var missing []string
	for _, m := range c.Models {
		if _, ok := c.Prices[m]; !ok {
			missing = append(missing, m)
		}
	}
	return missing
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// envDuration reads a Go duration (e.g. "2s", "500ms") from key, returning def
// when unset/empty and an error on an unparseable, negative or zero value.
// Zero is rejected rather than treated as "no limit": every duration here is a
// timeout, and an accidentally empty-ish value must not silently disable a
// bound that exists to stop a probe hanging forever.
func envDuration(key string, def time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration like 2s or 500ms: %w", key, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be positive", key)
	}
	return d, nil
}

// Load reads configuration from the environment, applying defaults.
func Load() (Config, error) {
	cfg := Config{
		Addr:              env("BACKEND_ADDR", ":8080"),
		DBPath:            env("BACKEND_DB_PATH", "/data/mimostats.db"),
		LogLevel:          env("BACKEND_LOG_LEVEL", "info"),
		BaseURL:           env("BACKEND_MIMO_BASE_URL", DefaultBaseURL),
		APIKey:            env("BACKEND_MIMO_API_KEY", ""),
		MimoHost:          env("BACKEND_PING_MIMO_HOST", DefaultMimoHost),
		RefSGPHost:        env("BACKEND_PING_REF_SGP_HOST", DefaultRefSGPHost),
		ProbeUserAgent:    env("BACKEND_PROBE_USER_AGENT", DefaultUserAgent),
		ProbeSystemPrompt: env("BACKEND_PROBE_SYSTEM_PROMPT", DefaultSystemPrompt),
		Models:            splitList(env("BACKEND_MODELS", ""), DefaultModels),
	}

	var err error
	if cfg.Prices, err = parsePrices(os.Getenv("BACKEND_PRICES")); err != nil {
		return Config{}, err
	}
	if cfg.Retention, err = envDuration("BACKEND_RETENTION", 2160*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.PingTimeout, err = envDuration("BACKEND_PING_TIMEOUT", 5*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.DialTimeout, err = envDuration("BACKEND_PROBE_DIAL_TIMEOUT", 10*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.HeaderTimeout, err = envDuration("BACKEND_PROBE_HEADER_TIMEOUT", 60*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.TTFTTimeout, err = envDuration("BACKEND_PROBE_TTFT_TIMEOUT", 150*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.IdleTimeout, err = envDuration("BACKEND_PROBE_IDLE_TIMEOUT", 45*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.ProbeTimeout, err = envDuration("BACKEND_PROBE_TIMEOUT", 240*time.Second); err != nil {
		return Config{}, err
	}

	if cfg.APIKey == "" {
		return Config{}, fmt.Errorf("BACKEND_MIMO_API_KEY is required")
	}
	if len(cfg.Models) == 0 {
		return Config{}, fmt.Errorf("BACKEND_MODELS must name at least one model")
	}
	if err := validateBaseURL(cfg.BaseURL); err != nil {
		return Config{}, err
	}
	// Whitespace-only, not just empty: MiMo's injection is suppressed by the
	// presence of a system message, but a blank one is a configuration mistake
	// that would ship a meaningless prompt to production. See DefaultSystemPrompt.
	if strings.TrimSpace(cfg.ProbeSystemPrompt) == "" {
		return Config{}, fmt.Errorf("BACKEND_PROBE_SYSTEM_PROMPT must not be empty")
	}
	for name, host := range map[string]string{
		"BACKEND_PING_MIMO_HOST":    cfg.MimoHost,
		"BACKEND_PING_REF_SGP_HOST": cfg.RefSGPHost,
	} {
		if host == "" {
			return Config{}, fmt.Errorf("%s must not be empty", name)
		}
		// A host:port here would be dialled as "host:port:443". Catch it at boot
		// rather than as a permanently-failing ping that reads as an outage.
		//
		// A bare IPv6 literal is exempted from the colon test, which would
		// otherwise reject every one of them — the error says "hostname or IP"
		// and .env.example documents IPs, so an operator pointing the europe
		// reference at 2606:4700:4700::1111 would be refused at boot by a guard
		// aimed at "example.com:443". net.ParseIP tells the two apart exactly:
		// it accepts the literal and rejects host:port. Safe downstream because
		// probe.Pinger resolves via net.Resolver.LookupHost and dials through
		// net.JoinHostPort, both of which bracket a v6 address correctly.
		if strings.Contains(host, "/") {
			return Config{}, fmt.Errorf("%s must be a bare hostname or IP without scheme or port", name)
		}
		if strings.Contains(host, ":") && net.ParseIP(host) == nil {
			return Config{}, fmt.Errorf("%s must be a bare hostname or IP without scheme or port", name)
		}
	}

	// The TTFT watchdog and the idle bound are both subordinate to the overall
	// deadline. Configured the other way round they can never fire, and every
	// failure collapses back into the single "timeout" class the ladder exists
	// to split apart.
	if cfg.TTFTTimeout >= cfg.ProbeTimeout {
		return Config{}, fmt.Errorf("BACKEND_PROBE_TTFT_TIMEOUT must be less than BACKEND_PROBE_TIMEOUT")
	}
	if cfg.IdleTimeout >= cfg.ProbeTimeout {
		return Config{}, fmt.Errorf("BACKEND_PROBE_IDLE_TIMEOUT must be less than BACKEND_PROBE_TIMEOUT")
	}

	return cfg, nil
}

// splitList parses a comma-separated override, trimming blanks. An override
// that is present but yields nothing usable returns empty rather than silently
// reverting to the default: "BACKEND_MODELS=," is a mistake worth failing on,
// not a request for the defaults.
// parsePrices reads BACKEND_PRICES.
//
// Format is one entry per model, comma separated:
//
//	mimo-v2.5=0.30/1.20/0.30,mimo-v2.5-pro=0.60/2.40/0.60
//
// The three numbers are USD per million tokens: input, output, cached-input. The
// third is optional and defaults to the input rate, which OVERSTATES the bill on
// a cache hit rather than flattering it — the honest direction to be wrong in,
// and cached_tokens is required to sit near zero anyway.
//
// Unset means DefaultPrices — the point of shipping a table is that a
// deployment does not have to carry one. "none" turns pricing off and the panel
// with it. A MALFORMED value is an error, because someone tried to configure
// prices and coming up with none would look identical to not having tried.
//
// A table given here REPLACES the defaults rather than merging into them: a
// half-overridden price list is the kind of state where one model quietly keeps
// a stale rate, and the whole table is two short lines to write.
func parsePrices(raw string) (map[string]ModelPrice, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		out := make(map[string]ModelPrice, len(DefaultPrices))
		for k, v := range DefaultPrices {
			out[k] = v
		}
		return out, nil
	}
	if trimmed == PricesOff {
		return nil, nil
	}
	out := make(map[string]ModelPrice, 2)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		model, rates, ok := strings.Cut(entry, "=")
		model = strings.TrimSpace(model)
		if !ok || model == "" {
			return nil, fmt.Errorf("BACKEND_PRICES entry %q must be model=in/out[/cached]", entry)
		}
		parts := strings.Split(rates, "/")
		if len(parts) < 2 || len(parts) > 3 {
			return nil, fmt.Errorf("BACKEND_PRICES entry %q must be model=in/out[/cached]", entry)
		}
		var p ModelPrice
		for i, target := range []*float64{&p.In, &p.Out, &p.Cached} {
			if i >= len(parts) {
				break
			}
			v, err := strconv.ParseFloat(strings.TrimSpace(parts[i]), 64)
			if err != nil {
				return nil, fmt.Errorf("BACKEND_PRICES entry %q: %q is not a number", entry, parts[i])
			}
			// Negative is rejected, zero is not: a model on a free tier is a
			// real thing to configure, and it produces a true total of nothing.
			if v < 0 {
				return nil, fmt.Errorf("BACKEND_PRICES entry %q: rates must not be negative", entry)
			}
			*target = v
		}
		if len(parts) == 2 {
			p.Cached = p.In
		}
		out[model] = p
	}
	return out, nil
}

func splitList(raw string, def []string) []string {
	if strings.TrimSpace(raw) == "" {
		return append([]string(nil), def...)
	}
	out := make([]string, 0, 2)
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func validateBaseURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("BACKEND_MIMO_BASE_URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("BACKEND_MIMO_BASE_URL is not a valid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("BACKEND_MIMO_BASE_URL must be an http or https URL")
	}
	if u.Host == "" {
		return fmt.Errorf("BACKEND_MIMO_BASE_URL must include a host")
	}
	// Userinfo is a credential. "https://user:pass@host/v1" passes every other
	// check here, so without this line a secret typed into the base URL would
	// travel wherever this value travels — logs, error messages, and any future
	// consumer of the config. Rejected at boot rather than stripped later: an
	// operator who put a secret here must be told, not silently have it dropped
	// from one of the several places it would reach.
	if u.User != nil {
		return fmt.Errorf("BACKEND_MIMO_BASE_URL must not embed credentials; use BACKEND_MIMO_API_KEY")
	}
	// A query string is not a credential by construction, but it is where an
	// API key most often ends up on an OpenAI-compatible endpoint. Same
	// reasoning.
	if u.RawQuery != "" {
		return fmt.Errorf("BACKEND_MIMO_BASE_URL must not carry a query string")
	}
	return nil
}
