package probe

import (
	"crypto/rand"
	_ "embed"
	"strings"
)

// wideDocument is a fixed ~2 700-word excerpt from Darwin's On the Origin of
// Species (1859), chapter III.
//
// Public domain worldwide with no attribution obligation, which matters because
// this ships in a public MIT repo — the Project Gutenberg header and footer are
// stripped, since that boilerplate carries its own licence terms while Darwin's
// text does not. Expository rather than narrative, so it summarises to a
// predictable length; politically inert, so it will not trip a refusal; and old
// enough that its exact text is verifiable by anyone reproducing the
// measurement.
//
// ONE document, never rotated. The 12 rotating framings and multiple documents
// of an earlier draft are deliberately not reinstated: they never helped cache
// defeat and only injected variance into the measured metric.
//
//go:embed corpus/wide.txt
var wideDocument string

// wideNonceLen is 8 base62 characters — enough entropy that no two runs collide
// over the retention window, and short enough to sit inside the first cache
// block rather than displacing document text out of it.
const wideNonceLen = 8

// WideMaxTokens caps the wide response. 300 output tokens is ~7 s of
// generation, long enough to watch throughput degrade mid-stream under KV-cache
// pressure. At infer's ~70 tokens (~1.5 s) only the opening moments of decode
// are ever observed.
const WideMaxTokens = 300

// InferMaxTokens caps the infer response at roughly double the 2-3 sentences
// asked for, so a slightly wordy answer completes naturally rather than being
// truncated — a finish_reason of "length" on infer would mean the ITL series
// silently stopped describing a whole response.
const InferMaxTokens = 150

// WidePrompt builds the wide user message: a random nonce, then the fixed
// framing, then the document.
//
// Placement is the whole trick. Prefix caches key on the LEADING prefix in
// sequential blocks, so a nonce at the top invalidates the first block and
// everything after it, while varying anything AFTER the document would leave
// the expensive part matching and the cache hit intact. A cache hit skips
// prefill — which would silently destroy the one measurement wide exists to
// take, leaving a plausible-looking TTFT that is really a lookup.
//
// cached_tokens is recorded on every run and must stay near zero. A rise means
// the nonce stopped working.
func WidePrompt() string {
	var b strings.Builder
	b.Grow(len(wideDocument) + 64)
	b.WriteString(wideNonce())
	b.WriteString("\nSummarise the following.\n")
	b.WriteString(wideDocument)
	return b.String()
}

// WideDocument exposes the corpus for tests, which assert what is actually
// being sent.
func WideDocument() string { return wideDocument }

// wideNonce draws 8 base62 characters. Falls back to a fixed string if the
// entropy source fails: a run with a degraded nonce records a visible
// cached_tokens spike, which is a better failure than aborting the probe and
// leaving a hole in the series.
func wideNonce() string {
	buf := make([]byte, wideNonceLen)
	if _, err := rand.Read(buf); err != nil {
		return "00000000"
	}
	out := make([]byte, wideNonceLen)
	for i, b := range buf {
		out[i] = sessionIDAlphabet[int(b)%len(sessionIDAlphabet)]
	}
	return string(out)
}
