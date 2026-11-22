package config

import (
	"strings"
	"testing"
)

// Unset, nui is off entirely: no origin for the CSP, no feed.
func TestNuiIsOffByDefault(t *testing.T) {
	setMinimalEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.NuiOrigin != "" || cfg.NuiFeedKey != "" {
		t.Errorf("nui = %q / key set %v, want both empty", cfg.NuiOrigin, cfg.NuiFeedKey != "")
	}
}

// The value goes into the CSP and a script tag on every page: only a bare
// origin passes, normalised.
func TestNuiURLMustBeAnOrigin(t *testing.T) {
	for _, tc := range []struct {
		raw, want string
		ok        bool
	}{
		{"https://nui.example.com", "https://nui.example.com", true},
		{"https://NUI.example.com/", "https://nui.example.com", true},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080", true},
		{"https://nui.example.com/api", "", false},
		{"https://nui.example.com?x=1", "", false},
		{"https://nui.example.com/#x", "", false},
		{"https://user:pw@nui.example.com", "", false},
		{"ftp://nui.example.com", "", false},
		{"nui.example.com", "", false},
		{"https://nui.example.com;script-src", "", false},
		{"https://nui'.example.com", "", false},
		{"https://nui.example.com:", "", false},
		{"https://nui.example.com:8443", "https://nui.example.com:8443", true},
	} {
		got, err := validateNuiURL(tc.raw)
		if tc.ok && (err != nil || got != tc.want) {
			t.Errorf("validateNuiURL(%q) = %q, %v; want %q", tc.raw, got, err, tc.want)
		}
		if !tc.ok && err == nil {
			t.Errorf("validateNuiURL(%q) = %q, want an error", tc.raw, got)
		}
	}
}

// A feed key with no collector is a half-done setup and fails the boot.
func TestNuiFeedKeyNeedsTheURL(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("BACKEND_NUI_FEED_KEY", "feed-key")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "BACKEND_NUI_URL") {
		t.Errorf("Load = %v, want a BACKEND_NUI_URL error", err)
	}
}

func TestNuiLoads(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("BACKEND_NUI_URL", "https://nui.example.com/")
	t.Setenv("BACKEND_NUI_FEED_KEY", "feed-key")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.NuiOrigin != "https://nui.example.com" || cfg.NuiFeedKey != "feed-key" {
		t.Errorf("nui = %q, key %q", cfg.NuiOrigin, cfg.NuiFeedKey)
	}
}
