package samples

import "time"

// ModelSummary is one model's state over a window.
//
// Table: -
type ModelSummary struct {
	ModelID string `json:"model_id"`

	TTFT Stats `json:"ttft"`
	ITL  Stats `json:"itl"`
	// TPS is the median gross throughput. It leads the throughput reading
	// rather than ITL: MiMo batches tokens into chunks and delivers them in
	// bursts, so the median inter-chunk gap collapses toward zero on a
	// perfectly healthy run (measured 0.0075 ms against 70 tok/s). Both are
	// served; the client is told which is robust.
	TPS Stats `json:"tps"`

	// Availability counts attempts on ATTRIBUTABLE cycles, successes and
	// failures alike. Failed runs are excluded from the latency percentiles
	// above and counted here — otherwise an outage reads as catastrophic
	// latency.
	//
	// FAILED runs on unattributable cycles ('uplink', and its historical split
	// 'route') are not counted at all: nothing in Singapore answered, so the
	// failure cannot be shown to be MiMo's. A run that SUCCEEDED on such a
	// cycle still counts — it is positive evidence MiMo answered, and dropping
	// it would also drop its answer grade and its reasoning/cache canaries.
	// Attempts is therefore the denominator the percentage is honest about, not
	// the raw cycle count — a window of nothing but unattributable failures
	// reports 0 attempts, which clients render as no data.
	Attempts  int     `json:"attempts"`
	Succeeded int     `json:"succeeded"`
	Available float64 `json:"available_pct"`

	// Censored is how many of those attempts were cut off by our own timeout
	// ladder — see probe.CensoringErrorClasses.
	//
	// It is the caveat the percentiles above cannot carry themselves. Those are
	// computed over successful runs only, so the runs this counts were removed
	// from the TOP of the distribution: the worse the endpoint gets, the more of
	// its slow tail is deleted, and the better P95 looks. A reader who cannot see
	// this number cannot tell a genuinely fast window from one where everything
	// slow was thrown away.
	//
	// Counted per RUN, not per column: one censored run takes that run's TTFT,
	// ITL and throughput with it, so a separate figure per metric would be the
	// same number three times. It shares the attempt denominator, and therefore
	// the same unattributable-cycle exclusion — a run cut off during our own
	// uplink outage is not MiMo's slow tail.
	Censored int `json:"censored"`

	// Correctness is the canary: a silent reroute to a smaller model shows up
	// here before it shows up in any timing.
	Answered   int      `json:"answered"`
	Correct    int      `json:"correct"`
	CorrectPct *float64 `json:"correct_pct"`

	// MaxReasoningTokens must stay 0. Non-zero means thinking came back on and
	// every latency figure in this window is measuring something else.
	MaxReasoningTokens int `json:"max_reasoning_tokens"`
	// MaxCachedTokens must stay near 0, or the system prompt went missing and
	// MiMo's own injected one is being served from cache.
	MaxCachedTokens int `json:"max_cached_tokens"`
}

// NetSummary is the network layer over a window.
//
// Table: -
type NetSummary struct {
	Target    string  `json:"target"`
	Connect   Stats   `json:"connect"`
	Attempts  int     `json:"attempts"`
	Succeeded int     `json:"succeeded"`
	Available float64 `json:"available_pct"`
}

// Summary is the whole dashboard state for one window.
//
// Table: -
type Summary struct {
	Window string         `json:"window"`
	Cycles int            `json:"cycles"`
	Models []ModelSummary `json:"models"`
	Net    []NetSummary   `json:"net"`
	// Recent is NOT window-scoped — see RecentCycle for why. It rides along in
	// this response rather than in an endpoint of its own because the client
	// needs it on exactly the requests it already makes.
	//
	// It is also the ONLY fault channel in this response. A `faults` map held
	// the window's per-fault counts beside it, and a `skipped_runs` count rode
	// along with it; both existed for the attribution panel, and both went when
	// it did. The client never read either — it derives what it needs from
	// Recent[].Fault, per cycle (ui/src/verdict.ts) — so serving them meant two
	// SQL round trips per dashboard build for nothing.
	Recent      []RecentCycle `json:"recent"`
	GeneratedAt time.Time     `json:"generated_at"`
}

// Point is one bucket of a series.
//
// Table: -
type Point struct {
	// T is the bucket's start, as Unix seconds.
	T int64 `json:"t"`
	// N is the successful samples the percentiles were computed from.
	N int `json:"n"`
	// Censored is how many samples in this bucket our own timeout ladder cut
	// off — see probe.CensoringErrorClasses.
	//
	// A bucket can be censored and still plot a value: the line is then drawn
	// from the runs that FINISHED, with the slow ones missing, which is a chart
	// that looks better the worse things got. A bucket can also be entirely
	// censored, N = 0 with a nil P50 — indistinguishable, without this field,
	// from a bucket where the probe simply was not running. Both are exactly the
	// moment a reader most needs to know the top of the distribution was cut off,
	// so the client marks these buckets rather than drawing them as ordinary.
	Censored int `json:"censored"`
	// Values are nil when the bucket produced no percentile at all. A null
	// renders as a gap; a zero would render as a floor, which is a lie.
	//
	// Note that no minimum-sample threshold is applied per bucket: suppression is
	// a headline-figure rule (see MinSamplesForPercentile), deliberately not a
	// chart rule. N is 0 only on a bucket that exists solely because something
	// was censored in it.
	P50 *float64 `json:"p50"`
	P95 *float64 `json:"p95"`
}

// Sample is one raw row, as served publicly.
//
// Note what is absent: error_detail. It is operator-only, because a provider
// error body can echo request fragments, and a test asserts it never appears in
// any public payload.
//
// Table: infer_probes
type Sample struct {
	At        time.Time `json:"at"`
	ModelID   string    `json:"model_id"`
	TTFTMs    *float64  `json:"ttft_ms"`
	TotalMs   *float64  `json:"total_ms"`
	ITLP50Ms  *float64  `json:"itl_p50_ms"`
	OutputTPS *float64  `json:"output_tps"`
	// What went in and what came out. output_tps is the completion count over
	// the decode window, so a row with a fast tok/s and a tiny answer reads very
	// differently from the same rate over a long one — the rate alone cannot
	// tell those apart.
	//
	// The prompt side used to stay out on the grounds that it says what the run
	// cost rather than what it produced, and that the cost panel already sums it.
	// The sum is the wrong shape for this table: reading per-run input off a
	// daily total means reconstructing it from an aggregate, on the one surface
	// that promises nothing is aggregated away.
	//
	// cached_tokens and reasoning_tokens still stay out. Both are invariants
	// that must sit at zero rather than measurements that vary per run, and the
	// model cards are where a breach of either is meant to surface.
	PromptTokens *int64  `json:"prompt_tokens"`
	OutputTokens *int64  `json:"output_tokens"`
	OK           bool    `json:"ok"`
	AnswerOK     *bool   `json:"answer_ok"`
	ErrorClass   *string `json:"error_class"`
}

// Failure is one failed inference call, as the errors block serves it.
//
// Note what is absent, and that it stays absent: error_detail. The column holds
// raw upstream bytes — on an HTTP error it is the response body verbatim — and
// a provider error body can echo request fragments, including credentials and
// the prompt. Redacting it on the way out was considered and rejected:
// redaction is a denylist tuned to one credential's shape, which is the right
// bet for an operator log and the wrong one for a public unauthenticated
// endpoint. The detail stays in the database and in the daemon's logs, where
// the operator can read it.
//
// HTTPStatus is what this shape adds over the raw table, and it carries no such
// risk — it is an integer. It is what tells a 429 apart from a 503 when both
// land as error_class "http_error". Null when the run never reached a status,
// which is every transport failure.
// Fault is the cycle's stored attribution, carried so the card can say whose
// failure this was. Without it the block was the only public surface that
// neither excluded unattributable cycles nor reported them: during our own
// uplink outage it listed rows that Summarize and the charts' censored band
// deliberately refuse to blame on MiMo, with nothing on the row saying so.
//
// Carried rather than filtered, and that is the whole point. Dropping those
// rows would make the card print "nothing went wrong in the last 24 hours" during
// a real outage, which is worse than listing them unlabelled — the runs DID
// fail, and a monitor that goes quiet when the network dies is the failure mode
// this page exists to avoid. So they stay, labelled.
//
// Served raw, including the historical 'route' and the empty string a cycle
// with no attribution row carries. Deciding what those mean is the client's
// job, exactly as it is for RecentCycle.Fault — see ui/src/verdict.ts.
//
// AnswerOK is what tells the block's two kinds of row apart. It is false on a
// run that SUCCEEDED and was then graded wrong — a 200, a body, and the wrong
// element in it — and nil on everything else, which since migration 0006 means
// only that the run failed before an answer existed. The card must read it
// before it reaches for the failure colour, because the one thing a
// graded-wrong row cannot claim is that the endpoint was down.
//
// Table: infer_probes
type Failure struct {
	At         time.Time `json:"at"`
	ModelID    string    `json:"model_id"`
	ErrorClass *string   `json:"error_class"`
	HTTPStatus *int64    `json:"http_status"`
	AnswerOK   *bool     `json:"answer_ok"`
	Fault      string    `json:"fault"`
}
