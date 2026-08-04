// Package config loads mimostats' runtime configuration from environment
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

	// DefaultMimoHost is the TCP ping target for MiMo's own edge. It is derived
	// from DefaultBaseURL by default, but stays overridable so the ping target
	// and the inference host can be pointed apart during testing.
	DefaultMimoHost = "token-plan-sgp.xiaomimimo.com"

	// DefaultRefSGPHost answers "is *any* Roubaix->Singapore path healthy".
	//
	// The plan originally named an OVH Singapore endpoint, because the probe box
	// is OVH Roubaix and a same-carrier path makes "the route is fine" a stronger
	// claim. No such public hostname exists: `ap-southeast-sgp` does not resolve,
	// and `sgp.ovh` answers from Cloudflare anycast in Europe (~18 ms), which
	// would be a Europe reference mislabelled as Singapore — exactly the failure
	// the reference exists to prevent. DigitalOcean's Singapore endpoint is a
	// genuine SGP target (~268 ms from Zurich), at the cost of the same-carrier
	// argument. Disclosed on the methodology page.
	DefaultRefSGPHost = "sgp1.digitaloceanspaces.com"

	// DefaultRefEUHost answers "is our uplink up at all" from a nearby PoP.
	DefaultRefEUHost = "1.1.1.1"
)

// DefaultUserAgent impersonates opencode, because MiMo's token-plan endpoint is
// an opencode-facing product and a neutral UA is not what production traffic
// looks like. Disclosed verbatim on the methodology page rather than hidden;
// overridable via BACKEND_PROBE_USER_AGENT.
const DefaultUserAgent = "opencode/1.18.11 ai-sdk/openai-compatible/3.0.20 ai-sdk/provider-utils/5.0.18 runtime/bun/1.3.14"

// DefaultSystemPrompt is sent on every probe request, and is NOT cosmetic.
//
// When a request carries no system message, MiMo injects its own — measured at
// 250 prompt tokens, of which 192 came back as `cached_tokens`. That breaks the
// probe twice over: a "~40 token" infer request actually costs ~263 tokens
// (blowing the cost model by ~6.5x), and most of the prefill it is supposed to
// be timing is served from cache instead of computed. Supplying any system
// message suppresses the injection entirely: prompt_tokens drops to 20 and
// cached_tokens comes back null.
//
// config_test.go pins this, because losing it re-introduces the fault silently —
// the probe keeps working and only the numbers become wrong.
const DefaultSystemPrompt = "You are a helpful assistant."

// Config holds all runtime settings.
type Config struct {
	Addr      string // HTTP listen address
	PublicURL string // externally reachable base URL
	DBPath    string // path to the SQLite file
	LogLevel  string

	// Origin labels which egress produced a sample. Present from day one so a
	// second probe location can be added without a migration.
	Origin string

	// MiMo endpoint. BaseURL is the inference host; APIKey is required at boot
	// so a misconfigured deployment fails loudly instead of recording an
	// unbroken wall of auth failures as a MiMo outage.
	BaseURL string
	APIKey  string

	// Probe targets for the TCP ping layer.
	MimoHost   string
	RefSGPHost string
	RefEUHost  string

	// ProbeUserAgent and ProbeSystemPrompt shape the outgoing inference request.
	ProbeUserAgent    string
	ProbeSystemPrompt string

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
		PublicURL:         env("BACKEND_PUBLIC_URL", ""),
		DBPath:            env("BACKEND_DB_PATH", "/data/mimostats.db"),
		LogLevel:          env("BACKEND_LOG_LEVEL", "info"),
		Origin:            env("BACKEND_ORIGIN", "rbx"),
		BaseURL:           env("BACKEND_MIMO_BASE_URL", DefaultBaseURL),
		APIKey:            env("BACKEND_MIMO_API_KEY", ""),
		MimoHost:          env("BACKEND_PING_MIMO_HOST", DefaultMimoHost),
		RefSGPHost:        env("BACKEND_PING_REF_SGP_HOST", DefaultRefSGPHost),
		RefEUHost:         env("BACKEND_PING_REF_EU_HOST", DefaultRefEUHost),
		ProbeUserAgent:    env("BACKEND_PROBE_USER_AGENT", DefaultUserAgent),
		ProbeSystemPrompt: env("BACKEND_PROBE_SYSTEM_PROMPT", DefaultSystemPrompt),
	}

	var err error
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
	// TrimSpace, not == "": env() substitutes the default for an empty value, so
	// an `== ""` test here could never fire and would be validation in name
	// only. A whitespace-only override IS reachable, and it would stamp every
	// sample with a blank origin — invisible until a second probe location is
	// added and the two cannot be told apart.
	if strings.TrimSpace(cfg.Origin) == "" {
		return Config{}, fmt.Errorf("BACKEND_ORIGIN must not be empty")
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
		"BACKEND_PING_REF_EU_HOST":  cfg.RefEUHost,
	} {
		if host == "" {
			return Config{}, fmt.Errorf("%s must not be empty", name)
		}
		// A host:port here would be dialled as "host:port:443". Catch it at boot
		// rather than as a permanently-failing ping that reads as an outage.
		if strings.Contains(host, "/") || strings.Contains(host, ":") {
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
	return nil
}
