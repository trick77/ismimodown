package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// testConfig is a fast ladder so timeout tests finish in milliseconds while
// keeping the ORDERING the production ladder has.
func testConfig(base string) Config {
	return Config{
		BaseURL:       base,
		APIKey:        "tp-test",
		UserAgent:     "opencode/test",
		SystemPrompt:  "You are a helpful assistant.",
		DialTimeout:   2 * time.Second,
		HeaderTimeout: 500 * time.Millisecond,
		TTFTTimeout:   700 * time.Millisecond,
		IdleTimeout:   400 * time.Millisecond,
		Timeout:       3 * time.Second,
	}
}

// sseServer emits chunks at the given inter-chunk delays, so ttft_ms and
// itl_p50_ms can be asserted against a stream of known construction.
func sseServer(t *testing.T, ttft time.Duration, gaps []time.Duration, tokens []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		time.Sleep(ttft)
		// MiMo opens with an empty-content role chunk; reproduce it so the
		// ttft_ms / ttfat_ms split is exercised as it is in production.
		fmt.Fprintf(w, "data: %s\n\n", chunkJSON("", "assistant"))
		flusher.Flush()

		for i, tok := range tokens {
			if i < len(gaps) {
				time.Sleep(gaps[i])
			}
			fmt.Fprintf(w, "data: %s\n\n", chunkJSON(tok, ""))
			flusher.Flush()
		}
		fmt.Fprintf(w, "data: %s\n\n", finishJSON("stop"))
		fmt.Fprintf(w, "data: %s\n\n", usageJSON(len(tokens)))
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
}

func chunkJSON(content, role string) string {
	b, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"delta": map[string]any{"content": content, "role": role},
			"index": 0,
		}},
	})
	return string(b)
}

func finishJSON(reason string) string {
	b, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"delta": map[string]any{}, "finish_reason": reason, "index": 0,
		}},
	})
	return string(b)
}

func usageJSON(completion int) string {
	b, _ := json.Marshal(map[string]any{
		"choices": []any{},
		"usage": map[string]any{
			"prompt_tokens":             20,
			"completion_tokens":         completion,
			"total_tokens":              20 + completion,
			"prompt_tokens_details":     map[string]any{"cached_tokens": 0},
			"completion_tokens_details": map[string]any{"reasoning_tokens": 0},
		},
	})
	return string(b)
}

// The outgoing request is as much a part of the measurement as the timings: a
// probe that quietly stopped disabling thinking, or lost the system message,
// would keep succeeding and only the numbers would become wrong.
func TestRequestShapeIsWhatMimoNeeds(t *testing.T) {
	var got struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Thinking            *struct{ Type string } `json:"thinking"`
		EnableThinking      *bool                  `json:"enable_thinking"`
		MaxCompletionTokens int                    `json:"max_completion_tokens"`
		StreamOptions       *struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	var hdr http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		hdr = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", chunkJSON("Paris", ""))
		fmt.Fprintf(w, "data: %s\n\n", usageJSON(1))
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := NewClient(testConfig(srv.URL))
	if _, err := c.Run(context.Background(), Request{
		ModelID: "mimo-v2.5", Probe: ProbeShort, Prompt: "q", MaxTokens: 150,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got.Model != "mimo-v2.5" {
		t.Errorf("model = %q", got.Model)
	}
	if !got.Stream {
		t.Error("stream must be true; TTFT is unmeasurable without it")
	}
	if got.StreamOptions == nil || !got.StreamOptions.IncludeUsage {
		t.Error("stream_options.include_usage must be true or usage never arrives")
	}
	if got.MaxCompletionTokens != 150 {
		t.Errorf("max_completion_tokens = %d, want 150", got.MaxCompletionTokens)
	}
	// Both spellings, deliberately: which one a deployment honours is not
	// something to discover from a month of quietly wrong numbers.
	if got.Thinking == nil || got.Thinking.Type != "disabled" {
		t.Errorf("thinking = %+v, want {disabled}", got.Thinking)
	}
	if got.EnableThinking == nil || *got.EnableThinking {
		t.Error("enable_thinking must be present and false")
	}

	// The system message is load-bearing: without it MiMo injects a 250-token
	// prompt of its own and serves ~192 of it from cache.
	if len(got.Messages) != 2 || got.Messages[0].Role != "system" {
		t.Fatalf("messages = %+v, want a system message first", got.Messages)
	}
	if got.Messages[0].Content == "" {
		t.Error("system message must not be empty")
	}

	if ua := hdr.Get("User-Agent"); ua != "opencode/test" {
		t.Errorf("User-Agent = %q", ua)
	}
	sid := hdr.Get("X-Session-Id")
	if sid == "" || !strings.HasPrefix(sid, "ses_") {
		t.Errorf("X-Session-Id = %q, want a ses_ id", sid)
	}
	if aff := hdr.Get("X-Session-Affinity"); aff != sid {
		t.Errorf("X-Session-Affinity = %q, must equal X-Session-Id %q", aff, sid)
	}
	if auth := hdr.Get("Authorization"); auth != "Bearer tp-test" {
		t.Errorf("Authorization = %q", auth)
	}
	// Unset on purpose so net/http negotiates and decompresses gzip itself.
	if enc := hdr.Get("Accept-Encoding"); !strings.Contains(enc, "gzip") {
		t.Errorf("Accept-Encoding = %q, want net/http's transparent gzip", enc)
	}
}

// The timings must come out exactly as the stream was constructed, or every
// published number is unfounded.
func TestTimingsMatchTheConstructedStream(t *testing.T) {
	gaps := []time.Duration{
		40 * time.Millisecond, 40 * time.Millisecond, 40 * time.Millisecond,
		120 * time.Millisecond, 40 * time.Millisecond,
	}
	srv := sseServer(t, 200*time.Millisecond, gaps,
		[]string{"The", " capital", " is", " Paris", ".", " Truly."})
	defer srv.Close()

	c := NewClient(testConfig(srv.URL))
	res, err := c.Run(context.Background(), Request{
		ModelID: "mimo-v2.5", Probe: ProbeShort, Prompt: "q", MaxTokens: 150,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.OK {
		t.Fatalf("not OK: class=%s detail=%s", res.ErrorClass, res.ErrorDetail)
	}

	// ttft_ms is the first chunk with ANY delta — the empty-content role chunk
	// at 200ms. Generous upper bound: CI machines are not real-time.
	if res.TTFTMs < 190 || res.TTFTMs > 400 {
		t.Errorf("ttft_ms = %.1f, want ~200", res.TTFTMs)
	}
	// ttfat_ms is the first chunk with actual CONTENT, one gap later.
	if res.TTFATMs <= res.TTFTMs {
		t.Errorf("ttfat_ms (%.1f) must be after ttft_ms (%.1f)", res.TTFATMs, res.TTFTMs)
	}
	// Five content gaps of 40,40,40,120,40 -> median 40.
	if res.ITLP50Ms < 30 || res.ITLP50Ms > 80 {
		t.Errorf("itl_p50_ms = %.1f, want ~40", res.ITLP50Ms)
	}
	// p95 must be pulled up by the single 120ms outlier that the median ignores.
	if res.ITLP95Ms < res.ITLP50Ms {
		t.Errorf("itl_p95 (%.1f) must be >= itl_p50 (%.1f)", res.ITLP95Ms, res.ITLP50Ms)
	}
	if res.Content != "The capital is Paris. Truly." {
		t.Errorf("content = %q", res.Content)
	}
	if res.FinishReason != "stop" {
		t.Errorf("finish_reason = %q", res.FinishReason)
	}
	if res.Usage.CompletionTokens != 6 {
		t.Errorf("completion_tokens = %d, want 6", res.Usage.CompletionTokens)
	}
	if res.Usage.CompletionTokenDetails.ReasoningTokens != 0 {
		t.Error("reasoning_tokens must be 0 — the hard gate that thinking is off")
	}
	// httptrace must report a real connection, not zeros from a reused one.
	if res.ConnectMs <= 0 {
		t.Error("connect_ms must be non-zero; DisableKeepAlives guarantees a fresh conn")
	}
}

// One test per layer of the ladder. The whole point of the ladder is that these
// four situations are DIFFERENT findings, so each must produce its own class.
func TestTimeoutLadderClassifiesEachLayer(t *testing.T) {
	t.Run("headers never sent -> header_timeout", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(900 * time.Millisecond) // past HeaderTimeout (500ms), under Timeout
		}))
		defer srv.Close()

		res, err := NewClient(testConfig(srv.URL)).Run(context.Background(), Request{
			ModelID: "m", Probe: ProbeShort, Prompt: "q", MaxTokens: 150,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if res.OK {
			t.Fatal("expected failure")
		}
		if res.ErrorClass != ErrClassHeaderTimeout {
			t.Errorf("class = %q, want %q", res.ErrorClass, ErrClassHeaderTimeout)
		}
	})

	t.Run("headers then no token -> ttft_timeout", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			time.Sleep(1100 * time.Millisecond) // past TTFTTimeout (700ms)
		}))
		defer srv.Close()

		res, err := NewClient(testConfig(srv.URL)).Run(context.Background(), Request{
			ModelID: "m", Probe: ProbeShort, Prompt: "q", MaxTokens: 150,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if res.ErrorClass != ErrClassTTFTTimeout {
			t.Errorf("class = %q, want %q (queueing, not a stall)", res.ErrorClass, ErrClassTTFTTimeout)
		}
	})

	t.Run("chunks then silence -> stalled", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			f := w.(http.Flusher)
			for i := 0; i < 3; i++ {
				fmt.Fprintf(w, "data: %s\n\n", chunkJSON("tok", ""))
				f.Flush()
				time.Sleep(30 * time.Millisecond)
			}
			time.Sleep(800 * time.Millisecond) // past IdleTimeout (400ms)
		}))
		defer srv.Close()

		res, err := NewClient(testConfig(srv.URL)).Run(context.Background(), Request{
			ModelID: "m", Probe: ProbeShort, Prompt: "q", MaxTokens: 150,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if res.ErrorClass != ErrClassStalled {
			t.Errorf("class = %q, want %q (died mid-stream, not queueing)", res.ErrorClass, ErrClassStalled)
		}
	})

	t.Run("dribbles past the overall deadline -> timeout", func(t *testing.T) {
		cfg := testConfig("")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			f := w.(http.Flusher)
			// Keeps the idle watchdog happy indefinitely; only the overall
			// deadline can stop this. Bounded by the request context so
			// srv.Close() does not block for the full loop once the client
			// has given up.
			for i := 0; i < 500; i++ {
				select {
				case <-r.Context().Done():
					return
				default:
				}
				fmt.Fprintf(w, "data: %s\n\n", chunkJSON("tok", ""))
				f.Flush()
				time.Sleep(50 * time.Millisecond)
			}
		}))
		defer srv.Close()

		cfg.BaseURL = srv.URL
		cfg.Timeout = 600 * time.Millisecond
		cfg.TTFTTimeout = 400 * time.Millisecond
		cfg.IdleTimeout = 300 * time.Millisecond
		res, err := NewClient(cfg).Run(context.Background(), Request{
			ModelID: "m", Probe: ProbeShort, Prompt: "q", MaxTokens: 150,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if res.ErrorClass != ErrClassTimeout {
			t.Errorf("class = %q, want %q", res.ErrorClass, ErrClassTimeout)
		}
	})
}

// A failure is a recorded sample, never a dropped one — and it must carry how
// far it got, or a partial failure is indistinguishable from an instant one.
func TestFailedRunIsStillARecordedSample(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f := w.(http.Flusher)
		fmt.Fprintf(w, "data: %s\n\n", chunkJSON("tok", ""))
		f.Flush()
		time.Sleep(800 * time.Millisecond)
	}))
	defer srv.Close()

	res, err := NewClient(testConfig(srv.URL)).Run(context.Background(), Request{
		ModelID: "mimo-v2.5", Probe: ProbeShort, Prompt: "q", MaxTokens: 150,
	})
	if err != nil {
		t.Fatalf("Run must not return an error for a transport failure: %v", err)
	}
	if res.OK {
		t.Fatal("expected OK=false")
	}
	if res.ErrorClass == "" {
		t.Error("a failed sample must carry an error class")
	}
	if res.ModelID != "mimo-v2.5" || res.Probe != ProbeShort {
		t.Error("a failed sample must still identify what was being probed")
	}
	if res.TotalMs <= 0 {
		t.Error("a failed sample must record how far it got")
	}
}

func TestHTTPStatusClassification(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusTooManyRequests, ErrClassRateLimited},
		{http.StatusUnauthorized, ErrClassAuth},
		{http.StatusForbidden, ErrClassAuth},
		{http.StatusInternalServerError, ErrClassHTTP},
		{http.StatusBadGateway, ErrClassHTTP},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":{"message":"nope"}}`))
			}))
			defer srv.Close()

			res, err := NewClient(testConfig(srv.URL)).Run(context.Background(), Request{
				ModelID: "m", Probe: ProbeShort, Prompt: "q", MaxTokens: 150,
			})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if res.ErrorClass != tc.want {
				t.Errorf("class = %q, want %q", res.ErrorClass, tc.want)
			}
			if res.HTTPStatus != tc.status {
				t.Errorf("http_status = %d, want %d", res.HTTPStatus, tc.status)
			}
			// The provider body goes to error_detail (operator-only), never
			// anywhere public.
			if !strings.Contains(res.ErrorDetail, "nope") {
				t.Errorf("error_detail should carry the provider body, got %q", res.ErrorDetail)
			}
		})
	}
}

// auth_error must be its own class: a bad key is almost always ours, and
// letting it read as a MiMo outage on a public dashboard is a credibility
// problem, not a cosmetic one.
func TestAuthFailureIsNotReportedAsAMimoOutage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid API Key"}}`))
	}))
	defer srv.Close()

	res, _ := NewClient(testConfig(srv.URL)).Run(context.Background(), Request{
		ModelID: "m", Probe: ProbeShort, Prompt: "q", MaxTokens: 150,
	})
	if res.ErrorClass != ErrClassAuth {
		t.Errorf("class = %q, want %q", res.ErrorClass, ErrClassAuth)
	}
	if res.ErrorClass == ErrClassHTTP || res.ErrorClass == ErrClassTimeout {
		t.Error("an auth failure must never be indistinguishable from a provider fault")
	}
}

func TestAnswerAssertionDrivesTheCorrectnessCanary(t *testing.T) {
	run := func(t *testing.T, content string, want bool) {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: %s\n\n", chunkJSON(content, ""))
			fmt.Fprintf(w, "data: %s\n\n", usageJSON(1))
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		defer srv.Close()

		q := Question{ID: "capital-france", Ask: "What is the capital city of France?", Want: "Paris"}
		res, err := NewClient(testConfig(srv.URL)).Run(context.Background(), Request{
			ModelID: "m", Probe: ProbeShort, Prompt: q.Prompt(), MaxTokens: 150,
			QuestionID: q.ID, Assert: q.Assert,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if res.AnswerOK == nil {
			t.Fatal("answer_ok must be set on a successful run")
		}
		if *res.AnswerOK != want {
			t.Errorf("answer_ok = %v for content %q, want %v", *res.AnswerOK, content, want)
		}
		if res.QuestionID != "capital-france" {
			t.Errorf("question_id = %q", res.QuestionID)
		}
	}

	t.Run("correct answer", func(t *testing.T) {
		run(t, "The capital of France is Paris, a city on the Seine.", true)
	})
	t.Run("case insensitive", func(t *testing.T) {
		run(t, "it is PARIS.", true)
	})
	// A silent reroute to a smaller model shows up HERE, before any latency
	// metric moves.
	t.Run("wrong answer trips the canary", func(t *testing.T) {
		run(t, "The capital of France is Lyon.", false)
	})
}

// A failed run has no answer — which is NOT the same as a wrong answer, and
// conflating them would make an outage look like a quality collapse.
func TestFailedRunHasNoAnswerVerdict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	q := Question{ID: "q", Ask: "a", Want: "Paris"}
	res, _ := NewClient(testConfig(srv.URL)).Run(context.Background(), Request{
		ModelID: "m", Probe: ProbeShort, Prompt: q.Prompt(), MaxTokens: 150, Assert: q.Assert,
	})
	if res.AnswerOK != nil {
		t.Errorf("answer_ok = %v, must stay nil when the run never produced an answer", *res.AnswerOK)
	}
}

func TestMalformedChunkIsAProtocolError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {not json\n\n")
	}))
	defer srv.Close()

	res, _ := NewClient(testConfig(srv.URL)).Run(context.Background(), Request{
		ModelID: "m", Probe: ProbeShort, Prompt: "q", MaxTokens: 150,
	})
	if res.ErrorClass != ErrClassProtocol {
		t.Errorf("class = %q, want %q", res.ErrorClass, ErrClassProtocol)
	}
}

// The failure a proxy in front of the model produces: 200 OK, and a body that
// is not a stream at all. The class is the same protocol error either way, so
// the body IS the diagnosis — reporting only that nothing arrived leaves the
// operator unable to tell a broken model from a WAF.
func TestNonStreamBodyOnA200IsKept(t *testing.T) {
	const body = `<?xml version="1.0"?><Error><Code>ServiceUnavailable</Code></Error>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	res, _ := NewClient(testConfig(srv.URL)).Run(context.Background(), Request{
		ModelID: "m", Probe: ProbeShort, Prompt: "q", MaxTokens: 150,
	})
	if res.ErrorClass != ErrClassProtocol {
		t.Errorf("class = %q, want %q", res.ErrorClass, ErrClassProtocol)
	}
	if !strings.Contains(res.ErrorDetail, "ServiceUnavailable") {
		t.Errorf("detail = %q, want it to carry the body", res.ErrorDetail)
	}
}

// A hostile or broken upstream must not be able to grow error_detail without
// bound, and one SSE line may be a megabyte on its own — so the cap has to
// survive a body that is a single enormous line, not just many small ones.
func TestKeptBodyIsBounded(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"one enormous line", strings.Repeat("x", 64*1024)},
		// The separator has to be capped too, not just the text: this body is
		// mostly newlines, and appending one per line after the cap is reached
		// would grow error_detail without ever adding a character of evidence.
		{"ten thousand short lines", strings.Repeat("boom\n", 10000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()

			res, _ := NewClient(testConfig(srv.URL)).Run(context.Background(), Request{
				ModelID: "m", Probe: ProbeShort, Prompt: "q", MaxTokens: 150,
			})
			if n := len(res.ErrorDetail); n > maxErrorBodyBytes+64 {
				t.Errorf("detail is %d bytes, want it clipped near %d", n, maxErrorBodyBytes)
			}
		})
	}
}

// The commonest shape of a failure served inside a 200 stream: a well-formed
// SSE frame carrying an error object and no choices. It is not a non-data line,
// so keeping only the non-SSE preamble would still leave the operator with
// nothing but "nothing arrived".
func TestErrorObjectInsideAStreamIsKept(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"error":{"message":"upstream at capacity"}}`+"\n\n")
	}))
	defer srv.Close()

	res, _ := NewClient(testConfig(srv.URL)).Run(context.Background(), Request{
		ModelID: "m", Probe: ProbeShort, Prompt: "q", MaxTokens: 150,
	})
	if res.ErrorClass != ErrClassProtocol {
		t.Errorf("class = %q, want %q", res.ErrorClass, ErrClassProtocol)
	}
	if !strings.Contains(res.ErrorDetail, "upstream at capacity") {
		t.Errorf("detail = %q, want it to carry the error object", res.ErrorDetail)
	}
}

// A healthy stream says nothing extra. The frames before the first delta are
// collected, and dropping them once the stream proves real is what keeps a
// normal run's error_detail empty.
func TestHealthyStreamKeepsNoEvidence(t *testing.T) {
	srv := sseServer(t, 5*time.Millisecond, nil, []string{"Pa", "ris"})
	defer srv.Close()

	res, _ := NewClient(testConfig(srv.URL)).Run(context.Background(), Request{
		ModelID: "m", Probe: ProbeShort, Prompt: "q", MaxTokens: 150,
	})
	if !res.OK || res.ErrorDetail != "" {
		t.Errorf("ok = %v, detail = %q, want a clean run", res.OK, res.ErrorDetail)
	}
}

// An upstream error page is text, and often not in English. Cutting it at a
// byte boundary would put a replacement glyph where the last character of the
// diagnosis should be.
func TestKeptBodyIsCutOnARuneBoundary(t *testing.T) {
	body := strings.Repeat("服务不可用", 4096) // 3 bytes per rune, so 4096 splits one
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	res, _ := NewClient(testConfig(srv.URL)).Run(context.Background(), Request{
		ModelID: "m", Probe: ProbeShort, Prompt: "q", MaxTokens: 150,
	})
	if !utf8.ValidString(res.ErrorDetail) {
		t.Errorf("detail is not valid UTF-8: %q", res.ErrorDetail)
	}
}

// Nothing at all is still its own finding, and it keeps the original wording:
// there is no body to quote, and inventing an empty one would read as though
// something had been captured.
func TestEmptyBodyOnA200KeepsThePlainReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	defer srv.Close()

	res, _ := NewClient(testConfig(srv.URL)).Run(context.Background(), Request{
		ModelID: "m", Probe: ProbeShort, Prompt: "q", MaxTokens: 150,
	})
	if res.ErrorClass != ErrClassProtocol {
		t.Errorf("class = %q, want %q", res.ErrorClass, ErrClassProtocol)
	}
	if res.ErrorDetail != "stream produced no deltas" {
		t.Errorf("detail = %q, want the plain reason", res.ErrorDetail)
	}
}

// Truncated without [DONE] means the upstream went away mid-answer. Reporting
// that as success would publish a partial response as a complete one.
func TestTruncatedStreamIsNotSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", chunkJSON("partial", ""))
	}))
	defer srv.Close()

	res, _ := NewClient(testConfig(srv.URL)).Run(context.Background(), Request{
		ModelID: "m", Probe: ProbeShort, Prompt: "q", MaxTokens: 150,
	})
	if res.OK {
		t.Error("a stream that ended without [DONE] must not be recorded as success")
	}
	if res.ErrorClass != ErrClassStalled {
		t.Errorf("class = %q, want %q", res.ErrorClass, ErrClassStalled)
	}
}

// Shutdown is not a fault and must never be counted against availability.
func TestCancellationIsNotAFault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(700 * time.Millisecond)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	res, err := NewClient(testConfig(srv.URL)).Run(ctx, Request{
		ModelID: "m", Probe: ProbeShort, Prompt: "q", MaxTokens: 150,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ErrorClass != ErrClassCanceled {
		t.Errorf("class = %q, want %q", res.ErrorClass, ErrClassCanceled)
	}
}

func TestPercentile(t *testing.T) {
	cases := []struct {
		name string
		vals []float64
		p    float64
		want float64
	}{
		{"empty is zero", nil, 50, 0},
		{"single", []float64{7}, 50, 7},
		{"median of odd", []float64{1, 2, 3, 4, 5}, 50, 3},
		{"median ignores a single outlier", []float64{40, 40, 40, 120, 40}, 50, 40},
		{"p95 catches the outlier", []float64{40, 40, 40, 120, 40}, 95, 120},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := percentile(tc.vals, tc.p); got != tc.want {
				t.Errorf("percentile(%v, %v) = %v, want %v", tc.vals, tc.p, got, tc.want)
			}
		})
	}
}

// percentile must not reorder its input: the caller keeps the gap slice.
func TestPercentileDoesNotMutateInput(t *testing.T) {
	vals := []float64{5, 1, 3}
	_ = percentile(vals, 50)
	if vals[0] != 5 || vals[1] != 1 || vals[2] != 3 {
		t.Errorf("percentile mutated its input: %v", vals)
	}
}

// MaxCompletionTokens is `omitempty`, so a zero cap silently drops the field
// and the model runs to its own default — breaking both the latency numbers and
// the cost model at once. That is the exact failure the pre-implementation
// curls existed to rule out, so it must be a loud caller error.
func TestRunRejectsARequestWithNoOutputCap(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	_, err := NewClient(testConfig(srv.URL)).Run(context.Background(), Request{
		ModelID: "mimo-v2.5", Probe: ProbeWide, Prompt: "q", // MaxTokens deliberately unset
	})
	if err == nil {
		t.Fatal("expected an error for a request with no output cap")
	}
	if called {
		t.Error("an uncapped request must never reach the endpoint — it would be billed")
	}
}

// A resolver that runs out of time reports dns_error, even though the error it
// produces also satisfies errors.Is(err, context.DeadlineExceeded).
//
// The transport's DialTimeout is far below the run deadline, so in production
// this is the way a DNS outage arrives: a *net.DNSError carrying the dial
// context's deadline. Classifying it off the error alone publishes a resolver
// failure of ours as `timeout` — a MiMo-side fault the endpoint never saw.
func TestDNSTimeoutIsDNSErrorNotTimeout(t *testing.T) {
	// A resolver whose transport never answers, killed by its own short
	// deadline rather than by the run's.
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	dialCtx, cancelDial := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelDial()
	_, err := resolver.LookupHost(dialCtx, "probe-dns-timeout.invalid")
	if err == nil {
		t.Fatal("lookup unexpectedly succeeded")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("precondition: %v does not wrap context.DeadlineExceeded", err)
	}

	// The run is still alive — only the lookup timed out.
	outer := context.Background()
	stream, cancelStream := context.WithCancelCause(context.Background())
	defer cancelStream(nil)

	if got := classifyRequestErr(outer, stream, err); got != ErrClassDNS {
		t.Errorf("class = %q, want %q", got, ErrClassDNS)
	}
}

// The run deadline expiring is still `timeout`, whatever the error looks like.
func TestRunDeadlineIsTimeout(t *testing.T) {
	stream, cancelStream := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancelStream()
	<-stream.Done()

	got := classifyRequestErr(context.Background(), stream,
		fmt.Errorf("Post \"https://x/chat/completions\": %w", context.DeadlineExceeded))
	if got != ErrClassTimeout {
		t.Errorf("class = %q, want %q", got, ErrClassTimeout)
	}
}
