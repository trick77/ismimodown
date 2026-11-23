package config

import (
	"strings"
	"testing"
)

// Unset, Stakeout is off entirely: no origin for the CSP, no feed.
func TestStakeoutIsOffByDefault(t *testing.T) {
	setMinimalEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.StakeoutOrigin != "" || cfg.StakeoutFeedKey != "" {
		t.Errorf("Stakeout = %q / key set %v, want both empty", cfg.StakeoutOrigin, cfg.StakeoutFeedKey != "")
	}
}

// The value goes into the CSP and a script tag on every page: only a bare
// origin passes, normalised.
func TestStakeoutURLMustBeAnOrigin(t *testing.T) {
	for _, tc := range []struct {
		raw, want string
		ok        bool
	}{
		{"https://stakeout.example.com", "https://stakeout.example.com", true},
		{"https://Stakeout.example.com/", "https://stakeout.example.com", true},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080", true},
		{"https://stakeout.example.com/api", "", false},
		{"https://stakeout.example.com?x=1", "", false},
		{"https://stakeout.example.com/#x", "", false},
		{"https://user:pw@stakeout.example.com", "", false},
		{"ftp://stakeout.example.com", "", false},
		{"stakeout.example.com", "", false},
		{"https://stakeout.example.com;script-src", "", false},
		{"https://stakeout'.example.com", "", false},
		{"https://stakeout.example.com:", "", false},
		{"https://stakeout.example.com:8443", "https://stakeout.example.com:8443", true},
	} {
		got, err := validateStakeoutURL(tc.raw)
		if tc.ok && (err != nil || got != tc.want) {
			t.Errorf("validateStakeoutURL(%q) = %q, %v; want %q", tc.raw, got, err, tc.want)
		}
		if !tc.ok && err == nil {
			t.Errorf("validateStakeoutURL(%q) = %q, want an error", tc.raw, got)
		}
	}
}

// A feed key with no collector is a half-done setup and fails the boot.
func TestStakeoutFeedKeyNeedsTheURL(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("ANALYTICS_FEED_KEY", "feed-key")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "ANALYTICS_URL") {
		t.Errorf("Load = %v, want a ANALYTICS_URL error", err)
	}
}

// A feed key without the site it was issued for would have every batch refused: it fails the boot too.
func TestStakeoutFeedKeyNeedsTheSite(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("ANALYTICS_URL", "https://stakeout.example.com")
	t.Setenv("ANALYTICS_FEED_KEY", "feed-key")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "ANALYTICS_SITE") {
		t.Errorf("Load = %v, want a ANALYTICS_SITE error", err)
	}
}

func TestStakeoutLoads(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("ANALYTICS_URL", "https://stakeout.example.com/")
	t.Setenv("ANALYTICS_FEED_KEY", "feed-key")
	t.Setenv("ANALYTICS_SITE", "ismimodown.com")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.StakeoutOrigin != "https://stakeout.example.com" || cfg.StakeoutFeedKey != "feed-key" ||
		cfg.StakeoutSite != "ismimodown.com" {
		t.Errorf("Stakeout = %q, key %q, site %q", cfg.StakeoutOrigin, cfg.StakeoutFeedKey, cfg.StakeoutSite)
	}
}
