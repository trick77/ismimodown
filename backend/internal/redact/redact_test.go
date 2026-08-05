package redact

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The key must not survive, whatever the gateway chose to call the header it
// echoed it back in. bearerRe only fires on the literal word "Bearer"; these are
// the shapes that get past it.
func TestKeyIsStrippedWhateverTheHeaderIsCalled(t *testing.T) {
	const key = "tp-livebillablekey000000000000000"
	for _, in := range []string{
		"authorization: Bearer " + key,
		"x-api-key: " + key,
		`{"error":"bad credential","api_key":"` + key + `"}`,
		"upstream rejected key=" + key + " at edge",
	} {
		got := String(in)
		if strings.Contains(got, key) {
			t.Errorf("String(%q) leaked the key: %s", in, got)
		}
		if !strings.Contains(got, "redacted") {
			t.Errorf("String(%q) dropped the key silently, with no marker: %s", in, got)
		}
	}
}

// The length floor keeps the rule off ordinary prose: "tp-" is a plausible
// fragment of a word, and a log that redacts its own diagnostics is worse than
// one that does not redact at all.
func TestShortTPFragmentsSurvive(t *testing.T) {
	for _, in := range []string{
		"tp-1", "the tp-ack timer expired", "no credential here at all",
	} {
		if got := String(in); got != in {
			t.Errorf("String(%q) = %q, want it untouched", in, got)
		}
	}
}

// Query strings and userinfo are stripped whole, since an allowlist of "safe"
// parameter names is a leak waiting for the next parameter.
func TestQueryStringAndUserinfoAreStripped(t *testing.T) {
	got := String(`Post "https://user:pw@api.example.com/v1/chat?key=secret": timeout`)
	for _, unwanted := range []string{"secret", "user:pw", "?"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("%q survived: %s", unwanted, got)
		}
	}
	// The path is the useful diagnostic and stays visible.
	if !strings.Contains(got, "/v1/chat") {
		t.Errorf("the path was stripped too: %s", got)
	}
}

// Err works on the rendered message, so wrapping depth cannot hide a credential
// from it.
func TestErrRedactsThroughWrapping(t *testing.T) {
	inner := errors.New("Bearer tp-livebillablekey000000000000000")
	wrapped := fmt.Errorf("probe failed: %w", inner)

	got := Err(wrapped).Error()
	if strings.Contains(got, "tp-livebillablekey") {
		t.Errorf("the key survived wrapping: %s", got)
	}
	if !strings.Contains(got, "probe failed") {
		t.Errorf("the outer context was lost: %s", got)
	}
}

// Nothing to redact means the original error, chain intact — callers that do
// wrap a sentinel should not silently lose errors.Is against it.
func TestCleanErrIsReturnedUnchanged(t *testing.T) {
	if Err(nil) != nil {
		t.Error("Err(nil) must stay nil")
	}
	sentinel := errors.New("connection refused")
	wrapped := fmt.Errorf("dial: %w", sentinel)
	if !errors.Is(Err(wrapped), sentinel) {
		t.Error("a clean error lost its chain")
	}
}
