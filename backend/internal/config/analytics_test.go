package config

import (
	"strings"
	"testing"
)

// Unset, Analytics is off entirely: no origin for the CSP, no feed.
func TestAnalyticsIsOffByDefault(t *testing.T) {
	setMinimalEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AnalyticsOrigin != "" || cfg.AnalyticsFeedKey != "" {
		t.Errorf("Analytics = %q / key set %v, want both empty", cfg.AnalyticsOrigin, cfg.AnalyticsFeedKey != "")
	}
}

// The value goes into the CSP and a script tag on every page: only a bare
// origin passes, normalised.
func TestAnalyticsURLMustBeAnOrigin(t *testing.T) {
	for _, tc := range []struct {
		raw, want string
		ok        bool
	}{
		{"https://analytics.example.com", "https://analytics.example.com", true},
		{"https://Analytics.example.com/", "https://analytics.example.com", true},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080", true},
		{"https://analytics.example.com/api", "", false},
		{"https://analytics.example.com?x=1", "", false},
		{"https://analytics.example.com/#x", "", false},
		{"https://user:pw@analytics.example.com", "", false},
		{"ftp://analytics.example.com", "", false},
		{"analytics.example.com", "", false},
		{"https://analytics.example.com;script-src", "", false},
		{"https://analytics'.example.com", "", false},
		{"https://analytics.example.com:", "", false},
		{"https://analytics.example.com:8443", "https://analytics.example.com:8443", true},
	} {
		got, err := validateAnalyticsURL(tc.raw)
		if tc.ok && (err != nil || got != tc.want) {
			t.Errorf("validateAnalyticsURL(%q) = %q, %v; want %q", tc.raw, got, err, tc.want)
		}
		if !tc.ok && err == nil {
			t.Errorf("validateAnalyticsURL(%q) = %q, want an error", tc.raw, got)
		}
	}
}

// A feed key with no collector is a half-done setup and fails the boot.
func TestAnalyticsFeedKeyNeedsTheURL(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("ANALYTICS_FEED_KEY", "feed-key")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "ANALYTICS_URL") {
		t.Errorf("Load = %v, want a ANALYTICS_URL error", err)
	}
}

// A feed key without the site it was issued for would have every batch refused: it fails the boot too.
func TestAnalyticsFeedKeyNeedsTheSite(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("ANALYTICS_URL", "https://analytics.example.com")
	t.Setenv("ANALYTICS_FEED_KEY", "feed-key")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "ANALYTICS_SITE") {
		t.Errorf("Load = %v, want a ANALYTICS_SITE error", err)
	}
}

func TestAnalyticsLoads(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("ANALYTICS_URL", "https://analytics.example.com/")
	t.Setenv("ANALYTICS_FEED_KEY", "feed-key")
	t.Setenv("ANALYTICS_SITE", "ismimodown.com")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AnalyticsOrigin != "https://analytics.example.com" || cfg.AnalyticsFeedKey != "feed-key" ||
		cfg.AnalyticsSite != "ismimodown.com" {
		t.Errorf("Analytics = %q, key %q, site %q", cfg.AnalyticsOrigin, cfg.AnalyticsFeedKey, cfg.AnalyticsSite)
	}
}
