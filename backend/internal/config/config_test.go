package config

import (
	"strings"
	"testing"
	"time"
)

// setMinimalEnv sets only what Load requires, so each test can add the one
// variable it is actually about.
func setMinimalEnv(t *testing.T) {
	t.Helper()
	t.Setenv("BACKEND_MIMO_API_KEY", "tp-test-key")
}

func TestLoadDefaults(t *testing.T) {
	setMinimalEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", cfg.Addr)
	}
	if cfg.BaseURL != DefaultBaseURL {
		t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, DefaultBaseURL)
	}
	if cfg.Retention != 2160*time.Hour {
		t.Errorf("Retention = %v, want 2160h (3 months)", cfg.Retention)
	}
	if cfg.ProbeTimeout != 240*time.Second {
		t.Errorf("ProbeTimeout = %v, want 240s", cfg.ProbeTimeout)
	}
	if cfg.PingTimeout != 5*time.Second {
		t.Errorf("PingTimeout = %v, want 5s", cfg.PingTimeout)
	}
}

// The three ping targets are the whole basis of fault attribution: if they are
// wrong or accidentally equal, the attribution table silently produces a
// confident but meaningless verdict.
func TestLoadPingTargetDefaults(t *testing.T) {
	setMinimalEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.MimoHost != DefaultMimoHost {
		t.Errorf("MimoHost = %q, want %q", cfg.MimoHost, DefaultMimoHost)
	}
	if cfg.RefSGPHost != DefaultRefSGPHost {
		t.Errorf("RefSGPHost = %q, want %q", cfg.RefSGPHost, DefaultRefSGPHost)
	}
	if cfg.MimoHost == cfg.RefSGPHost {
		t.Error("the two ping targets must be distinct; fault attribution is meaningless otherwise")
	}
}

// MiMo injects a ~250-token system prompt when the request carries none, of
// which ~192 come back cached. That breaks both the cost model and the prefill
// measurement, and it breaks them SILENTLY — the probe keeps succeeding. The
// default must therefore be non-empty, and an explicitly empty override must be
// a boot failure rather than a quiet reversion.
func TestSystemPromptDefeatsMimosInjectedPrompt(t *testing.T) {
	setMinimalEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ProbeSystemPrompt == "" {
		t.Fatal("ProbeSystemPrompt must default to a non-empty value")
	}
	if DefaultSystemPrompt == "" {
		t.Fatal("DefaultSystemPrompt must not be empty")
	}
}

func TestLoadRequiresAPIKey(t *testing.T) {
	// Deliberately no BACKEND_MIMO_API_KEY. Without it every probe would record
	// an auth failure, which reads on the dashboard as a MiMo outage.
	t.Setenv("BACKEND_MIMO_API_KEY", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected an error when BACKEND_MIMO_API_KEY is unset")
	}
	if !strings.Contains(err.Error(), "BACKEND_MIMO_API_KEY") {
		t.Errorf("error should name the missing variable, got: %v", err)
	}
}

func TestLoadOverrides(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("BACKEND_ADDR", "127.0.0.1:9999")
	t.Setenv("BACKEND_RETENTION", "720h")
	t.Setenv("BACKEND_PROBE_TIMEOUT", "300s")
	t.Setenv("BACKEND_PING_REF_SGP_HOST", "example.sg")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != "127.0.0.1:9999" {
		t.Errorf("Addr = %q", cfg.Addr)
	}
	if cfg.Retention != 720*time.Hour {
		t.Errorf("Retention = %v", cfg.Retention)
	}
	if cfg.RefSGPHost != "example.sg" {
		t.Errorf("RefSGPHost = %q", cfg.RefSGPHost)
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	cases := []struct {
		name string
		key  string
		val  string
		want string
	}{
		{"unparseable duration", "BACKEND_PROBE_TIMEOUT", "soon", "BACKEND_PROBE_TIMEOUT"},
		{"negative duration", "BACKEND_PING_TIMEOUT", "-5s", "BACKEND_PING_TIMEOUT"},
		{"zero duration", "BACKEND_PING_TIMEOUT", "0s", "BACKEND_PING_TIMEOUT"},
		{"base url without scheme", "BACKEND_MIMO_BASE_URL", "token-plan-sgp.xiaomimimo.com/v1", "BACKEND_MIMO_BASE_URL"},
		{"base url with bad scheme", "BACKEND_MIMO_BASE_URL", "ftp://example.com/v1", "BACKEND_MIMO_BASE_URL"},
		{"empty system prompt", "BACKEND_PROBE_SYSTEM_PROMPT", " ", "BACKEND_PROBE_SYSTEM_PROMPT"},
		{"ping host with port", "BACKEND_PING_MIMO_HOST", "example.com:443", "BACKEND_PING_MIMO_HOST"},
		{"ping host with scheme", "BACKEND_PING_REF_SGP_HOST", "https://example.com", "BACKEND_PING_REF_SGP_HOST"},
		// A credential embedded in the base URL would travel wherever that
		// value travels, so it is refused at boot. Both of these otherwise
		// pass every check.
		{"base url with userinfo", "BACKEND_MIMO_BASE_URL", "https://user:tp-livekey@example.com/v1", "BACKEND_MIMO_BASE_URL"},
		{"base url with a password-only userinfo", "BACKEND_MIMO_BASE_URL", "https://:tp-livekey@example.com/v1", "BACKEND_MIMO_BASE_URL"},
		{"base url with query string", "BACKEND_MIMO_BASE_URL", "https://example.com/v1?api_key=tp-livekey", "BACKEND_MIMO_BASE_URL"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setMinimalEnv(t)
			t.Setenv(tc.key, tc.val)

			_, err := Load()
			if err == nil {
				t.Fatalf("expected an error for %s=%q", tc.key, tc.val)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error should name %s, got: %v", tc.want, err)
			}
		})
	}
}

// A TTFT watchdog or idle bound at or above the overall deadline can never
// fire, which collapses every failure back into the single "timeout" class the
// whole ladder exists to split apart.
func TestLoadRejectsUnreachableTimeoutLadder(t *testing.T) {
	t.Run("ttft above overall", func(t *testing.T) {
		setMinimalEnv(t)
		t.Setenv("BACKEND_PROBE_TIMEOUT", "60s")
		t.Setenv("BACKEND_PROBE_TTFT_TIMEOUT", "90s")

		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "BACKEND_PROBE_TTFT_TIMEOUT") {
			t.Fatalf("expected a TTFT ladder error, got: %v", err)
		}
	})

	t.Run("idle above overall", func(t *testing.T) {
		setMinimalEnv(t)
		t.Setenv("BACKEND_PROBE_TIMEOUT", "30s")
		t.Setenv("BACKEND_PROBE_TTFT_TIMEOUT", "20s")
		t.Setenv("BACKEND_PROBE_IDLE_TIMEOUT", "45s")

		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "BACKEND_PROBE_IDLE_TIMEOUT") {
			t.Fatalf("expected an idle ladder error, got: %v", err)
		}
	})
}

// The default ladder must itself satisfy the ordering it enforces on overrides.
func TestDefaultTimeoutLadderIsOrdered(t *testing.T) {
	setMinimalEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !(cfg.PingTimeout < cfg.DialTimeout &&
		cfg.DialTimeout < cfg.HeaderTimeout &&
		cfg.HeaderTimeout < cfg.TTFTTimeout &&
		cfg.TTFTTimeout < cfg.ProbeTimeout) {
		t.Errorf("default ladder is not strictly ordered: ping=%v dial=%v header=%v ttft=%v overall=%v",
			cfg.PingTimeout, cfg.DialTimeout, cfg.HeaderTimeout, cfg.TTFTTimeout, cfg.ProbeTimeout)
	}
	if cfg.IdleTimeout >= cfg.ProbeTimeout {
		t.Errorf("idle=%v must be below overall=%v", cfg.IdleTimeout, cfg.ProbeTimeout)
	}
}

// The ping-host guard exists to catch "example.com:443", which would be dialled
// as "host:port:443". It must not also reject a bare IPv6 literal: the error
// message and .env.example both advertise that an IP is acceptable, and
// probe.Pinger dials through net.JoinHostPort, which brackets v6 correctly.
func TestLoadAcceptsIPv6PingHostButStillRejectsHostPort(t *testing.T) {
	t.Run("bare IPv6 literal is accepted", func(t *testing.T) {
		t.Setenv("BACKEND_MIMO_API_KEY", "tp-test")
		t.Setenv("BACKEND_PING_REF_SGP_HOST", "2606:4700:4700::1111")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.RefSGPHost != "2606:4700:4700::1111" {
			t.Errorf("RefSGPHost = %q, want the IPv6 literal", cfg.RefSGPHost)
		}
	})

	t.Run("host:port is still rejected", func(t *testing.T) {
		t.Setenv("BACKEND_MIMO_API_KEY", "tp-test")
		t.Setenv("BACKEND_PING_REF_SGP_HOST", "cloudflare.com:443")

		if _, err := Load(); err == nil {
			t.Fatal("Load accepted a host:port ping target")
		}
	})
}

// compose.yaml passes every optional variable through as "${VAR:-}", so an
// operator who does not set one hands the container an EMPTY string rather than
// leaving it unset. That form was chosen so this file stays the only place a
// default is written — and it is only safe because set-but-empty and unset are
// treated identically here.
//
// If that ever stops holding, a stock `docker compose up` blanks the whole
// configuration at once: empty models, an empty database path, and — the one
// that would not even fail loudly — an empty system prompt, which is the
// difference between measuring prefill and measuring a cache lookup.
func TestSetButEmptyIsTreatedAsUnset(t *testing.T) {
	setMinimalEnv(t)

	// Exactly the variables compose.yaml passes with an empty fallback.
	for _, key := range []string{
		"BACKEND_MODELS",
		"BACKEND_DB_PATH",
		"BACKEND_MIMO_BASE_URL",
		"BACKEND_RETENTION",
		"BACKEND_LOG_LEVEL",
		"BACKEND_PING_MIMO_HOST",
		"BACKEND_PING_REF_SGP_HOST",
		"BACKEND_PROBE_SYSTEM_PROMPT",
		"BACKEND_PROBE_USER_AGENT",
		"BACKEND_PING_TIMEOUT",
		"BACKEND_PROBE_DIAL_TIMEOUT",
		"BACKEND_PROBE_HEADER_TIMEOUT",
		"BACKEND_PROBE_TTFT_TIMEOUT",
		"BACKEND_PROBE_IDLE_TIMEOUT",
		"BACKEND_PROBE_TIMEOUT",
	} {
		t.Setenv(key, "")
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with every optional variable set to empty: %v", err)
	}

	if got, want := strings.Join(cfg.Models, ","), strings.Join(DefaultModels, ","); got != want {
		t.Errorf("Models = %q, want the built-in %q", got, want)
	}
	if cfg.DBPath != "/data/mimostats.db" {
		t.Errorf("DBPath = %q, want /data/mimostats.db", cfg.DBPath)
	}
	if cfg.BaseURL != DefaultBaseURL {
		t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, DefaultBaseURL)
	}
	// The invariant that would fail silently rather than loudly.
	if cfg.ProbeSystemPrompt != DefaultSystemPrompt {
		t.Errorf("ProbeSystemPrompt = %q, want the default; an empty one lets MiMo inject its own",
			cfg.ProbeSystemPrompt)
	}
	if cfg.ProbeUserAgent != DefaultUserAgent {
		t.Errorf("ProbeUserAgent = %q, want %q", cfg.ProbeUserAgent, DefaultUserAgent)
	}
	if cfg.Retention != 2160*time.Hour {
		t.Errorf("Retention = %v, want 2160h", cfg.Retention)
	}
	// The whole ladder, since compose passes all six the same way.
	for _, tc := range []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"PingTimeout", cfg.PingTimeout, 5 * time.Second},
		{"DialTimeout", cfg.DialTimeout, 10 * time.Second},
		{"HeaderTimeout", cfg.HeaderTimeout, 60 * time.Second},
		{"TTFTTimeout", cfg.TTFTTimeout, 150 * time.Second},
		{"IdleTimeout", cfg.IdleTimeout, 45 * time.Second},
		{"ProbeTimeout", cfg.ProbeTimeout, 240 * time.Second},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}

// BACKEND_PRICES unset is a supported state: the cost endpoint then serves
// tokens with no money in them, which is honest. A default price would publish
// a number that looks like a bill and is not one.
func TestLoadWithoutPricesIsNotAnError(t *testing.T) {
	setMinimalEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Prices) != 0 {
		t.Errorf("Prices = %+v, want none", cfg.Prices)
	}
}

func TestLoadParsesPrices(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("BACKEND_PRICES", "mimo-v2.5=0.30/1.20/0.15, mimo-v2.5-pro=0.60/2.40")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.Prices["mimo-v2.5"], (ModelPrice{In: 0.30, Out: 1.20, Cached: 0.15}); got != want {
		t.Errorf("mimo-v2.5 = %+v, want %+v", got, want)
	}
	// The cached rate is optional and falls back to the INPUT rate, which
	// overstates a cache hit rather than flattering it.
	if got, want := cfg.Prices["mimo-v2.5-pro"], (ModelPrice{In: 0.60, Out: 2.40, Cached: 0.60}); got != want {
		t.Errorf("mimo-v2.5-pro = %+v, want %+v", got, want)
	}
}

// A malformed table is an error rather than a silent fallback to none: someone
// tried to configure prices, and coming up publishing no cost at all would look
// identical to not having tried.
func TestLoadRejectsMalformedPrices(t *testing.T) {
	for _, bad := range []string{
		"mimo-v2.5",
		"mimo-v2.5=0.30",
		"mimo-v2.5=0.30/1.20/0.15/0.02",
		"mimo-v2.5=cheap/1.20",
		"=0.30/1.20",
		"mimo-v2.5=-0.30/1.20",
	} {
		t.Run(bad, func(t *testing.T) {
			setMinimalEnv(t)
			t.Setenv("BACKEND_PRICES", bad)

			if _, err := Load(); err == nil {
				t.Errorf("%q must be rejected", bad)
			} else if !strings.Contains(err.Error(), "BACKEND_PRICES") {
				t.Errorf("error must name the variable: %v", err)
			}
		})
	}
}

// A free tier is a real thing to configure and produces a true total of nothing.
func TestLoadAcceptsZeroPrices(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("BACKEND_PRICES", "mimo-v2.5=0/0")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.Prices["mimo-v2.5"]; !ok {
		t.Error("a zero-priced model must still be in the table")
	}
}
