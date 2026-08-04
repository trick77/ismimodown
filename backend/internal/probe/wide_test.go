package probe

import (
	"strings"
	"testing"
)

func TestWideDocumentIsTheRightSize(t *testing.T) {
	doc := WideDocument()
	words := len(strings.Fields(doc))
	// ~2 700 words is roughly 3 500–4 000 tokens: squarely in the range where
	// prefill is measurable and caching is a real risk, which is the whole
	// reason wide exists.
	if words < 2000 || words > 3500 {
		t.Errorf("document is %d words, want ~2700 (a 40 -> 4000 token gradient)", words)
	}
}

// The repo is public and MIT. Project Gutenberg's boilerplate carries its own
// licence terms; Darwin's text does not.
func TestWideDocumentCarriesNoGutenbergBoilerplate(t *testing.T) {
	doc := strings.ToLower(WideDocument())
	for _, marker := range []string{"gutenberg", "*** start of", "*** end of", "ebook"} {
		if strings.Contains(doc, marker) {
			t.Errorf("document contains %q — strip the Gutenberg boilerplate", marker)
		}
	}
}

// Placement is the whole trick: caches key on the LEADING prefix in sequential
// blocks. A nonce after the document would leave the expensive part matching and
// the cache hit intact, silently destroying the one measurement wide takes.
func TestWidePromptPutsTheNonceBeforeTheDocument(t *testing.T) {
	prompt := WidePrompt()

	docStart := strings.Index(prompt, "Before entering on the subject")
	if docStart <= 0 {
		t.Fatalf("document not found in prompt")
	}
	framingAt := strings.Index(prompt, "Summarise the following.")
	if framingAt <= 0 {
		t.Fatal("framing line not found")
	}
	if framingAt > docStart {
		t.Error("the framing must precede the document")
	}
	// The nonce is the first thing in the prompt, ahead of everything.
	nonce := prompt[:wideNonceLen]
	if len(nonce) != wideNonceLen {
		t.Fatalf("nonce is %d chars, want %d", len(nonce), wideNonceLen)
	}
	if strings.ContainsAny(nonce, " \n") {
		t.Errorf("nonce %q must be a solid base62 run at the very start", nonce)
	}
	if framingAt < wideNonceLen {
		t.Error("the nonce must come before the framing, not after it")
	}
}

// A repeating nonce is the same as no nonce: the cache block would match and
// cached_tokens would climb.
func TestWideNonceVariesBetweenRuns(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		n := wideNonce()
		if len(n) != wideNonceLen {
			t.Fatalf("nonce %q has length %d, want %d", n, len(n), wideNonceLen)
		}
		seen[n] = true
	}
	if len(seen) < 45 {
		t.Errorf("only %d distinct nonces in 50 draws — cache defeat would not hold", len(seen))
	}
}

// One document, never rotated. Rotating framings and multiple documents were
// deliberately dropped: they never helped cache defeat and only injected
// variance into the measured metric.
func TestWidePromptIsStableApartFromTheNonce(t *testing.T) {
	a := WidePrompt()[wideNonceLen:]
	b := WidePrompt()[wideNonceLen:]
	if a != b {
		t.Error("everything after the nonce must be byte-identical between runs")
	}
}

// The two probes must stay far apart in output length: wide exists partly to
// observe sustained decode, which infer's ~1.5 s window cannot show.
func TestOutputCapsKeepTheTwoProbesDistinct(t *testing.T) {
	if InferMaxTokens >= WideMaxTokens {
		t.Errorf("infer cap (%d) must be well below wide's (%d)", InferMaxTokens, WideMaxTokens)
	}
	if WideMaxTokens < 250 {
		t.Errorf("wide cap %d is too low to observe throughput degrading mid-stream", WideMaxTokens)
	}
}
