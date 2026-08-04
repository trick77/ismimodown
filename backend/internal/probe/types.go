package probe

// Probe kinds, matching the infer_probes CHECK constraint.
const (
	// ProbeInfer is the ~20-token question, run every cycle.
	ProbeInfer = "infer"
	// ProbeWide is the ~4 000-token summarisation, run hourly. It exists for
	// the two things infer structurally cannot see: prefill scaling (40 -> 4 000
	// tokens is a real gradient; at 40 alone there is no slope) and sustained
	// decode (300 output tokens is ~7 s, long enough to watch throughput degrade).
	ProbeWide = "wide"
)

type chatCompletionRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
	// Thinking is MiMo's native switch. EnableThinking is the older
	// OpenAI-compatible spelling. BOTH are sent: the plan's hard gate is
	// reasoning_tokens == 0, and which field a given deployment honours is not
	// something to discover from a month of quietly wrong latency numbers.
	Thinking       *thinkingOption `json:"thinking,omitempty"`
	EnableThinking *bool           `json:"enable_thinking,omitempty"`
	// MaxCompletionTokens, not max_tokens. Verified against the live endpoint:
	// a cap of 8 returned exactly 8 completion tokens with
	// finish_reason == "length". MiMo rejects a long list of other
	// OpenAI-standard params, so nothing here is assumed.
	MaxCompletionTokens int            `json:"max_completion_tokens,omitempty"`
	StreamOptions       *streamOptions `json:"stream_options,omitempty"`
}

// Message is one OpenAI-compatible chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// thinkingOption is MiMo's native switch for chain-of-thought.
// {"type":"disabled"} turns thinking off entirely.
type thinkingOption struct {
	Type string `json:"type"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatCompletionChunk struct {
	Choices []chunkChoice `json:"choices"`
	Usage   *TokenUsage   `json:"usage"`
}

type chunkChoice struct {
	Delta        chunkDelta `json:"delta"`
	FinishReason string     `json:"finish_reason"`
}

type chunkDelta struct {
	// Role is set only on the opening chunk. It matters because that chunk
	// carries empty content, and it is nonetheless the first thing the model
	// emitted — so it is what ttft_ms measures. Without tracking Role, ttft_ms
	// would collapse onto ttfat_ms and the divergence that reveals reasoning
	// creeping back on could never appear.
	Role    string `json:"role"`
	Content string `json:"content"`
	// ReasoningContent is watched even though thinking is disabled: a provider
	// can regress and start emitting it, and the divergence between ttft_ms and
	// ttfat_ms is what makes that visible.
	ReasoningContent string `json:"reasoning_content"`
}

// TokenUsage mirrors the usage block MiMo returns on the final streamed chunk.
//
// Note the nesting: reasoning_tokens lives under completion_tokens_details and
// cached_tokens under prompt_tokens_details — NOT at the top level, as an
// earlier draft of the plan assumed. Verified against the live endpoint.
type TokenUsage struct {
	PromptTokens           int                    `json:"prompt_tokens"`
	CompletionTokens       int                    `json:"completion_tokens"`
	TotalTokens            int                    `json:"total_tokens"`
	PromptTokensDetails    PromptTokenDetails     `json:"prompt_tokens_details"`
	CompletionTokenDetails CompletionTokenDetails `json:"completion_tokens_details"`
}

type PromptTokenDetails struct {
	// CachedTokens must stay near zero. On wide a rise means the cache-defeat
	// nonce stopped working; on infer it means the system message went missing
	// and MiMo's own injected prompt is being served from cache.
	CachedTokens int `json:"cached_tokens"`
}

type CompletionTokenDetails struct {
	// ReasoningTokens must be 0. It is the hard gate proving thinking is off.
	ReasoningTokens int `json:"reasoning_tokens"`
}

// InferResult is one inference-layer reading.
//
// Every timing is nullable-by-zero-with-OK rather than an error return, because
// a timeout is a RECORDED sample, never a dropped one: ok=0, an error class,
// and however far it got.
type InferResult struct {
	ModelID string
	Probe   string

	// TTFTMs is the first chunk carrying ANY delta; TTFATMs the first chunk
	// carrying actual content. MiMo opens with an empty-content role chunk, so
	// in the healthy case they differ by one chunk. Divergence beyond that is
	// the alarm that reasoning has silently come back on.
	TTFTMs  float64
	TTFATMs float64
	TotalMs float64

	// ITLP50Ms leads the throughput chart, not OutputTPS. At ~70 output tokens
	// the decode window is ~1.5 s, so gross tok/s is structurally noisy — a
	// couple of stray gaps swing it several percent. The same run yields ~70
	// inter-token intervals whose median is statistically solid.
	ITLP50Ms  float64
	ITLP95Ms  float64
	OutputTPS float64

	// Connection-level split from httptrace, as a cross-check against the
	// standalone ping. One fresh connection per run, so these are never zeros
	// from a reused keep-alive.
	DNSMs     float64
	ConnectMs float64
	TLSMs     float64

	Usage TokenUsage

	QuestionID string
	OK         bool
	// AnswerOK is the correctness canary. Nil when the run failed before an
	// answer existed — which is NOT the same as a wrong answer, and the schema
	// keeps them apart.
	AnswerOK     *bool
	HTTPStatus   int
	FinishReason string
	ErrorClass   string
	// ErrorDetail is operator-only and never served publicly.
	ErrorDetail string
	// Content is kept in memory for the answer assertion only; it is never
	// stored, so a provider response can never become part of the public API.
	Content string
}
