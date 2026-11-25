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

// The value goes into the CSP and a script tag on every page: one that is no
// bare origin fails the boot, named and never echoed (it may hold a password).
func TestAnalyticsURLMustBeAnOrigin(t *testing.T) {
	for _, raw := range []string{"https://analytics.example.com/api", "https://user:hunter2@analytics.example.com",
		"analytics.example.com", "https://analytics.example.com;script-src"} {
		setMinimalEnv(t)
		t.Setenv("ANALYTICS_URL", raw)
		t.Setenv("ANALYTICS_FEED_KEY", "feed-key")
		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "ANALYTICS_URL: ") || strings.Contains(err.Error(), "hunter2") ||
			strings.Contains(err.Error(), "ANALYTICS_URL is not") {
			t.Errorf("Load with %q = %v, want one ANALYTICS_URL error that does not echo it", raw, err)
		}
	}
}

// A key no header takes fails the boot, never echoed.
func TestAnalyticsFeedKeyMustFitAHeader(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("ANALYTICS_URL", "https://analytics.example.com")
	t.Setenv("ANALYTICS_FEED_KEY", "feed\nkey")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "ANALYTICS_FEED_KEY holds") || strings.Contains(err.Error(), "feed\nkey") {
		t.Errorf("Load = %v, want an ANALYTICS_FEED_KEY error that does not echo it", err)
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

func TestAnalyticsLoads(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("ANALYTICS_URL", "https://analytics.example.com/")
	t.Setenv("ANALYTICS_FEED_KEY", "feed-key")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AnalyticsOrigin != "https://analytics.example.com" || cfg.AnalyticsFeedKey != "feed-key" {
		t.Errorf("Analytics = %q, key %q", cfg.AnalyticsOrigin, cfg.AnalyticsFeedKey)
	}
}
