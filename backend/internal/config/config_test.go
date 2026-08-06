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
	if cfg.Retention != Retention {
		t.Errorf("Retention = %v, want %v", cfg.Retention, Retention)
	}
	if cfg.ProbeTimeout != ProbeTimeout {
		t.Errorf("ProbeTimeout = %v, want %v", cfg.ProbeTimeout, ProbeTimeout)
	}
	if cfg.PingTimeout != PingTimeout {
		t.Errorf("PingTimeout = %v, want %v", cfg.PingTimeout, PingTimeout)
	}
}

// Retention is what bounds the database, and it is now a constant, so the only
// thing that can move it is an edit to this number.
func TestRetentionIsThreeMonths(t *testing.T) {
	if Retention != 2160*time.Hour {
		t.Errorf("Retention = %v, want 2160h (3 months)", Retention)
	}
}

// The two ping targets are the whole basis of fault attribution: if they are
// wrong or accidentally equal, the attribution table silently produces a
// confident but meaningless verdict.
func TestLoadPingTargetDefaults(t *testing.T) {
	setMinimalEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.MimoSGPHost != DefaultMimoSGPHost {
		t.Errorf("MimoSGPHost = %q, want %q", cfg.MimoSGPHost, DefaultMimoSGPHost)
	}
	if cfg.RefSGPHost != DefaultRefSGPHost {
		t.Errorf("RefSGPHost = %q, want %q", cfg.RefSGPHost, DefaultRefSGPHost)
	}
	if cfg.MimoSGPHost == cfg.RefSGPHost {
		t.Error("the two ping targets must be distinct; fault attribution is meaningless otherwise")
	}
}

// All four ping targets must be distinct hosts.
//
// Two of them colliding is not a crash — it is four lines on "The wire itself"
// where two are the same series under two region labels, presented as a
// comparison. Nothing downstream can detect that: the labels are static in the
// UI. It was reachable while the Singapore edge was derived from
// BACKEND_MIMO_BASE_URL and the Amsterdam one was a constant; both are
// constants now, and this is what keeps them from drifting back together.
func TestEveryPingTargetIsADistinctHost(t *testing.T) {
	setMinimalEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	seen := map[string]string{}
	for _, target := range []struct{ name, host string }{
		{"MimoSGPHost", cfg.MimoSGPHost},
		{"RefSGPHost", cfg.RefSGPHost},
		{"MimoAMSHost", cfg.MimoAMSHost},
		{"RefAMSHost", cfg.RefAMSHost},
	} {
		if prev, ok := seen[target.host]; ok {
			t.Errorf("%s and %s are both %q; two chart lines would be the same series",
				prev, target.name, target.host)
		}
		seen[target.host] = target.name
	}
}

// The edges do NOT follow the base URL any more.
//
// They did, on the reasoning that pinging one host while inferring against
// another reports a path nobody uses. That held for one region. With two, it
// made one edge target operator-controlled and the other fixed, so pointing the
// base URL at Amsterdam collapsed both onto the same host.
//
// The tradeoff is real and is the point of this test: a deployment that
// repoints the base URL now keeps probing the constants, so the ping layer and
// the inference layer can disagree about where MiMo is. That is chosen, not
// accidental.
func TestPingTargetsIgnoreTheBaseURL(t *testing.T) {
	for _, baseURL := range []string{
		"https://mimo.example.com/v1",
		"https://token-plan-ams.xiaomimimo.com/v1",
		"http://localhost:9000/v1",
	} {
		t.Run(baseURL, func(t *testing.T) {
			setMinimalEnv(t)
			t.Setenv("BACKEND_MIMO_BASE_URL", baseURL)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.MimoSGPHost != DefaultMimoSGPHost {
				t.Errorf("MimoSGPHost = %q, want the constant %q", cfg.MimoSGPHost, DefaultMimoSGPHost)
			}
			if cfg.MimoAMSHost != DefaultMimoAMSHost {
				t.Errorf("MimoAMSHost = %q, want the constant %q", cfg.MimoAMSHost, DefaultMimoAMSHost)
			}
		})
	}
}

// The base URL is still VALIDATED, even though no ping target derives from it
// any more.
//
// That validation was never really about the hostname: it refuses userinfo and
// a query string, either of which would carry a live tp- key wherever BaseURL
// travels, and it refuses a non-http scheme or a missing host. Dropping the
// derivation must not quietly drop those with it.
func TestBaseURLIsStillValidatedWithoutTheDerivation(t *testing.T) {
	for _, tc := range []struct{ name, baseURL string }{
		{"userinfo", "https://user:tp-livekey@example.com/v1"},
		{"query string", "https://example.com/v1?api_key=tp-livekey"},
		{"bad scheme", "ftp://example.com/v1"},
		{"no host", "https://:8443/v1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setMinimalEnv(t)
			t.Setenv("BACKEND_MIMO_BASE_URL", tc.baseURL)

			if _, err := Load(); err == nil {
				t.Fatalf("Load accepted BACKEND_MIMO_BASE_URL = %q", tc.baseURL)
			}
		})
	}
}

// The SGP reference must not silently become the MiMo host it exists to be
// compared against. Nothing enforces this at boot — an operator can point it
// anywhere — but the SHIPPED pair has to be two different networks or the
// default deployment attributes nothing.
func TestDefaultRefHostIsNotMimosOwn(t *testing.T) {
	if strings.Contains(DefaultRefSGPHost, "xiaomimimo.com") {
		t.Errorf("DefaultRefSGPHost = %q; the reference must not be MiMo's own edge", DefaultRefSGPHost)
	}
	// sgp.ovh resolves to Cloudflare anycast and answers from Europe in ~18 ms.
	// It looks like an OVH Singapore host and is not one, which is exactly the
	// mislabelling this reference exists to detect.
	if DefaultRefSGPHost == "sgp.ovh" {
		t.Error("sgp.ovh is Cloudflare anycast in Europe, not a Singapore target")
	}
}

// MiMo injects a ~250-token system prompt when the request carries none, of
// which ~192 come back cached. That breaks both the cost model and the prefill
// measurement, and it breaks them SILENTLY — the probe keeps succeeding.
//
// This used to be enforced at boot against an environment variable. The
// variable is gone and the check with it, so the invariant is asserted here
// instead: an edit that blanks the constant fails the build's tests rather than
// shipping a probe that measures a cache lookup.
func TestSystemPromptDefeatsMimosInjectedPrompt(t *testing.T) {
	if strings.TrimSpace(DefaultSystemPrompt) == "" {
		t.Fatal("DefaultSystemPrompt must not be empty or whitespace")
	}

	setMinimalEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ProbeSystemPrompt != DefaultSystemPrompt {
		t.Errorf("ProbeSystemPrompt = %q, want the constant", cfg.ProbeSystemPrompt)
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
	t.Setenv("BACKEND_DB_PATH", "/tmp/somewhere.db")
	t.Setenv("BACKEND_LOG_LEVEL", "debug")
	t.Setenv("BACKEND_PING_REF_SGP_HOST", "example.sg")
	t.Setenv("BACKEND_PING_REF_AMS_HOST", "example.nl")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != "127.0.0.1:9999" {
		t.Errorf("Addr = %q", cfg.Addr)
	}
	if cfg.DBPath != "/tmp/somewhere.db" {
		t.Errorf("DBPath = %q", cfg.DBPath)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q", cfg.LogLevel)
	}
	if cfg.RefSGPHost != "example.sg" {
		t.Errorf("RefSGPHost = %q", cfg.RefSGPHost)
	}
	if cfg.RefAMSHost != "example.nl" {
		t.Errorf("RefAMSHost = %q", cfg.RefAMSHost)
	}
	// The EDGES are not configurable, and an override attempt must be inert
	// rather than surprising: pinging a host nobody infers against would put a
	// latency figure on the page for a path no request takes.
	t.Setenv("BACKEND_PING_MIMO_AMS_HOST", "somewhere.else")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MimoAMSHost != DefaultMimoAMSHost {
		t.Errorf("MimoAMSHost = %q, want the constant %q — the Amsterdam edge is not configurable",
			cfg.MimoAMSHost, DefaultMimoAMSHost)
	}
}

// The probe shape is not configurable, and the point of that is that setting
// one of the retired variables does NOTHING rather than something surprising.
// An operator with a stale .env gets the shipped behaviour, not a half-applied
// override.
func TestRetiredVariablesAreIgnored(t *testing.T) {
	setMinimalEnv(t)
	for key, val := range map[string]string{
		"BACKEND_MODELS":              "mimo-v2-flash",
		"BACKEND_PRICES":              "none",
		"BACKEND_RETENTION":           "1h",
		"BACKEND_PROBE_SYSTEM_PROMPT": "",
		"BACKEND_PING_MIMO_HOST":      "somewhere.else.example",
		"BACKEND_PROBE_TIMEOUT":       "1s",
		"BACKEND_PROBE_TTFT_TIMEOUT":  "9999s",
		"BACKEND_PING_TIMEOUT":        "not-a-duration",
	} {
		t.Setenv(key, val)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load must ignore retired variables, not fail on them: %v", err)
	}
	if strings.Join(cfg.Models, ",") != strings.Join(DefaultModels, ",") {
		t.Errorf("Models = %v, want the built-in pair", cfg.Models)
	}
	if len(cfg.Prices) != len(DefaultPrices) {
		t.Errorf("Prices = %+v, want the shipped table", cfg.Prices)
	}
	if cfg.Retention != Retention {
		t.Errorf("Retention = %v, want %v", cfg.Retention, Retention)
	}
	if cfg.ProbeSystemPrompt != DefaultSystemPrompt {
		t.Errorf("ProbeSystemPrompt = %q, want the constant", cfg.ProbeSystemPrompt)
	}
	if cfg.MimoSGPHost == "somewhere.else.example" {
		t.Error("MimoSGPHost must be the constant, not the retired variable")
	}
	if cfg.ProbeTimeout != ProbeTimeout || cfg.PingTimeout != PingTimeout {
		t.Errorf("ladder = %v/%v, want the constants", cfg.PingTimeout, cfg.ProbeTimeout)
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	cases := []struct {
		name string
		key  string
		val  string
		want string
	}{
		{"base url without scheme", "BACKEND_MIMO_BASE_URL", "token-plan-sgp.xiaomimimo.com/v1", "BACKEND_MIMO_BASE_URL"},
		{"base url with bad scheme", "BACKEND_MIMO_BASE_URL", "ftp://example.com/v1", "BACKEND_MIMO_BASE_URL"},
		// Host is ":8443" here, which is non-empty, but the hostname is not —
		// and the hostname is what the MiMo ping target is derived from. An
		// empty ping target fails resolution on every cycle and reads on the
		// dashboard as MiMo's edge being down.
		{"base url with a port and no host", "BACKEND_MIMO_BASE_URL", "https://:8443/v1", "BACKEND_MIMO_BASE_URL"},
		{"ping host with port", "BACKEND_PING_REF_SGP_HOST", "example.com:443", "BACKEND_PING_REF_SGP_HOST"},
		{"ping host with scheme", "BACKEND_PING_REF_SGP_HOST", "https://example.com", "BACKEND_PING_REF_SGP_HOST"},
		// The error must name the variable the operator actually set. Both
		// references run through one shared guard, so the wrong name here is a
		// realistic slip and an infuriating one to debug.
		{"ams ping host with port", "BACKEND_PING_REF_AMS_HOST", "example.com:443", "BACKEND_PING_REF_AMS_HOST"},
		{"ams ping host with scheme", "BACKEND_PING_REF_AMS_HOST", "https://example.com", "BACKEND_PING_REF_AMS_HOST"},
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

// The ladder must be strictly ordered, and TTFT and idle must both stay under
// the overall deadline or they can never fire — which collapses every failure
// back into the single "timeout" class the whole ladder exists to split apart.
//
// Asserted on the CONSTANTS. Load used to check this against environment input
// and cannot fail any more, so the guard moved here rather than disappearing:
// editing one of these numbers into an unreachable ladder still trips something.
func TestTimeoutLadderIsReachable(t *testing.T) {
	if !(PingTimeout < DialTimeout &&
		DialTimeout < HeaderTimeout &&
		HeaderTimeout < TTFTTimeout &&
		TTFTTimeout < ProbeTimeout) {
		t.Errorf("ladder is not strictly ordered: ping=%v dial=%v header=%v ttft=%v overall=%v",
			PingTimeout, DialTimeout, HeaderTimeout, TTFTTimeout, ProbeTimeout)
	}
	if IdleTimeout >= ProbeTimeout {
		t.Errorf("idle=%v must be below overall=%v; above it the watchdog can never fire",
			IdleTimeout, ProbeTimeout)
	}
	for name, d := range map[string]time.Duration{
		"PingTimeout":   PingTimeout,
		"DialTimeout":   DialTimeout,
		"HeaderTimeout": HeaderTimeout,
		"TTFTTimeout":   TTFTTimeout,
		"IdleTimeout":   IdleTimeout,
		"ProbeTimeout":  ProbeTimeout,
	} {
		if d <= 0 {
			t.Errorf("%s = %v; a non-positive bound disables the timeout entirely", name, d)
		}
	}
}

// Load copies the ladder onto the Config it returns, so a consumer reading
// cfg.TTFTTimeout gets the same number as a test reading the constant.
func TestLoadCarriesTheLadder(t *testing.T) {
	setMinimalEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, tc := range []struct {
		name      string
		got, want time.Duration
	}{
		{"PingTimeout", cfg.PingTimeout, PingTimeout},
		{"DialTimeout", cfg.DialTimeout, DialTimeout},
		{"HeaderTimeout", cfg.HeaderTimeout, HeaderTimeout},
		{"TTFTTimeout", cfg.TTFTTimeout, TTFTTimeout},
		{"IdleTimeout", cfg.IdleTimeout, IdleTimeout},
		{"ProbeTimeout", cfg.ProbeTimeout, ProbeTimeout},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}

// The ping-host guard catches "example.com:443", which would be dialled as
// "host:port:443", and it now also catches a bare IPv6 literal.
//
// The v6 case USED to be exempted, on the reasoning that the error message
// advertises "or IP" and net.JoinHostPort brackets v6 correctly. Both still
// true; what changed is that probe.Pinger resolves and dials IPv4 only, so a v6
// literal is a host the probe can never reach. Accepting it would trade a loud
// boot failure for a permanently-failing ping published as an outage — the exact
// outcome this guard exists to prevent — so the exemption became a trap and went.
//
// Both reference variables share validateRefHost, and both are checked here: a
// guard that drifted between the regions would leave one accepting what the
// other refuses.
func TestLoadRejectsUnreachableAndMalformedPingHosts(t *testing.T) {
	for _, envName := range []string{
		"BACKEND_PING_REF_SGP_HOST",
		"BACKEND_PING_REF_AMS_HOST",
	} {
		t.Run(envName, func(t *testing.T) {
			cases := []struct{ name, value string }{
				{"bare IPv6 literal", "2606:4700:4700::1111"},
				{"host:port", "cloudflare.com:443"},
				{"scheme", "https://example.com"},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					setMinimalEnv(t)
					t.Setenv(envName, tc.value)

					if _, err := Load(); err == nil {
						t.Fatalf("Load accepted %s = %q", envName, tc.value)
					}
				})
			}

			t.Run("bare IPv4 literal is accepted", func(t *testing.T) {
				setMinimalEnv(t)
				t.Setenv(envName, "1.1.1.1")

				if _, err := Load(); err != nil {
					t.Fatalf("Load rejected an IPv4 literal: %v", err)
				}
			})
		})
	}
}

// compose.yaml passes every optional variable through as "${VAR:-}", so an
// operator who does not set one hands the container an EMPTY string rather than
// leaving it unset. That form was chosen so this file stays the only place a
// default is written — and it is only safe because set-but-empty and unset are
// treated identically here.
func TestSetButEmptyIsTreatedAsUnset(t *testing.T) {
	setMinimalEnv(t)

	// Exactly the variables compose.yaml passes with an empty fallback.
	for _, key := range []string{
		"BACKEND_DB_PATH",
		"BACKEND_MIMO_BASE_URL",
		"BACKEND_PING_REF_SGP_HOST",
		"BACKEND_PING_REF_AMS_HOST",
		"BACKEND_PROBE_USER_AGENT",
	} {
		t.Setenv(key, "")
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with every optional variable set to empty: %v", err)
	}

	if cfg.DBPath != "/data/mimostats.db" {
		t.Errorf("DBPath = %q, want /data/mimostats.db", cfg.DBPath)
	}
	if cfg.BaseURL != DefaultBaseURL {
		t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, DefaultBaseURL)
	}
	if cfg.RefSGPHost != DefaultRefSGPHost {
		t.Errorf("RefSGPHost = %q, want %q", cfg.RefSGPHost, DefaultRefSGPHost)
	}
	if cfg.RefAMSHost != DefaultRefAMSHost {
		t.Errorf("RefAMSHost = %q, want %q", cfg.RefAMSHost, DefaultRefAMSHost)
	}
	if cfg.ProbeUserAgent != DefaultUserAgent {
		t.Errorf("ProbeUserAgent = %q, want %q", cfg.ProbeUserAgent, DefaultUserAgent)
	}
}

// Every probed model must have a price. Nothing downstream tolerates a gap any
// more: /api/cost prices every row it finds, so a model without an entry is
// billed at the zero value and drops out of a total that still presents itself
// as complete. Both lists are constants, so this is checkable here and is the
// only thing standing between an edit to one and a quietly wrong public figure.
func TestEveryProbedModelHasAPrice(t *testing.T) {
	for _, model := range DefaultModels {
		if _, ok := DefaultPrices[model]; !ok {
			t.Errorf("no price for %s, which is probed by default", model)
		}
	}
	if len(DefaultPrices) != len(DefaultModels) {
		t.Errorf("DefaultPrices has %d entries for %d probed models; a price for a model "+
			"that is not probed is dead weight, and a model with no price is a wrong total",
			len(DefaultPrices), len(DefaultModels))
	}
}

func TestLoadCarriesTheShippedTable(t *testing.T) {
	setMinimalEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, model := range DefaultModels {
		if _, ok := cfg.Prices[model]; !ok {
			t.Errorf("no price for %s on the loaded config", model)
		}
	}
}

// The shipped values, pinned. They are read off a third party's table and will
// drift; this is what makes the drift a failing test rather than a quietly wrong
// figure on a public page.
func TestDefaultPricesMatchTheirSource(t *testing.T) {
	want := map[string]ModelPrice{
		"mimo-v2.5":     {In: 0.40, Out: 2.00, Cached: 0.08},
		"mimo-v2.5-pro": {In: 1.00, Out: 3.00, Cached: 0.20},
	}
	for model, price := range want {
		if got := DefaultPrices[model]; got != price {
			t.Errorf("%s = %+v, want %+v", model, got, price)
		}
	}
	if len(DefaultPrices) != len(want) {
		t.Errorf("DefaultPrices has %d entries, want %d", len(DefaultPrices), len(want))
	}
}

// A mutable package-level map is one careless write away from a process that
// prices differently after its first Load. Same for the model list, which is a
// slice and just as reachable.
func TestLoadDoesNotHandOutThePackageLevelDefaults(t *testing.T) {
	setMinimalEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.Prices["mimo-v2.5"] = ModelPrice{In: 999}
	if DefaultPrices["mimo-v2.5"].In == 999 {
		t.Error("Load returned the package-level price map; a caller can rewrite the prices")
	}

	cfg.Models[0] = "mimo-v2-flash"
	if DefaultModels[0] == "mimo-v2-flash" {
		t.Error("Load returned the package-level model slice; a caller can rewrite what is probed")
	}
}
