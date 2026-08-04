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
		// The base URL is PUBLISHED verbatim as the "endpoint" field of
		// /api/methodology, so a credential embedded in it would be served to
		// every anonymous visitor. Both of these otherwise pass every check.
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
