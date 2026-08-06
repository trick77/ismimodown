package samples

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/trick77/ismimodown/internal/probe"
	"github.com/trick77/ismimodown/internal/redact"
)

// MinSamplesForPercentile is the suppression threshold.
//
// Below this a window returns insufficient_data rather than a number. A P50
// computed from three samples is exactly the figure that gets screenshotted out
// of context, and "we don't know yet" is both true and more useful than a
// confident wrong number.
const MinSamplesForPercentile = 20

// Window is an allow-listed query window.
//
// An allow-list rather than free-form parsing, for two reasons that both matter
// on a public unauthenticated endpoint: nothing caller-supplied is ever
// interpolated into SQL, and no window can be requested that retention has
// already emptied — offering 6mo when the sweeper deletes at 3 is a bug, not a
// feature.
type Window struct {
	Key      string
	Duration time.Duration
	// Bucket is derived server-side, never accepted from the caller, so a
	// request cannot ask for 100 000 points.
	Bucket time.Duration
}

// Windows is the complete allow-list. Nothing longer than the retention window,
// and nothing SHORTER than the suppression threshold can be satisfied in.
//
// There was a 1h window. At one cycle every 5 minutes it can hold at most 12
// samples against a MinSamplesForPercentile of 20, so it was structurally
// incapable of ever returning a percentile: a flawless hour — 12 cycles, every
// probe successful — still answered insufficient_data. Offering a window that
// cannot answer under any conditions is worse than not offering it, because the
// reader cannot tell "no data yet" from "this will never work".
//
// The floor for any window added here is MinSamplesForPercentile *
// scheduler.CycleInterval, which is 100 minutes at today's numbers. Below that,
// lower the threshold or do not offer the window.
var Windows = []Window{
	{"24h", 24 * time.Hour, 15 * time.Minute},
	{"48h", 48 * time.Hour, 30 * time.Minute},
	{"7d", 7 * 24 * time.Hour, 2 * time.Hour},
	{"30d", 30 * 24 * time.Hour, 6 * time.Hour},
	{"3mo", 2160 * time.Hour, 6 * time.Hour},
}

// LookupWindow resolves a window key. Unknown keys are rejected rather than
// defaulted, so a typo surfaces instead of silently returning a different range
// than the caller charted.
func LookupWindow(key string) (Window, bool) {
	for _, w := range Windows {
		if w.Key == key {
			return w, true
		}
	}
	return Window{}, false
}

// Stats is a latency distribution over some window.
type Stats struct {
	// N is the number of SUCCESSFUL samples behind the figures.
	N int `json:"n"`
	// Sufficient reports whether N cleared MinSamplesForPercentile. When false,
	// P50/P95 are nil — an explicit JSON null, so a client can tell "suppressed"
	// apart from "not sent" — and must be rendered as insufficient_data, never
	// as 0 ms.
	Sufficient bool     `json:"sufficient"`
	P50        *float64 `json:"p50_ms"`
	P95        *float64 `json:"p95_ms"`
}

// ModelSummary is one model's state over a window.
type ModelSummary struct {
	ModelID string `json:"model_id"`
	Probe   string `json:"probe"`

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
	// MaxCachedTokens must stay near 0 on wide, or the prefill numbers have
	// quietly become cache lookups.
	MaxCachedTokens int `json:"max_cached_tokens"`
}

// NetSummary is the network layer over a window.
type NetSummary struct {
	Target    string  `json:"target"`
	Connect   Stats   `json:"connect"`
	Attempts  int     `json:"attempts"`
	Succeeded int     `json:"succeeded"`
	Available float64 `json:"available_pct"`
}

// RecentRun is one model's outcome in one cycle, reduced to the two facts a
// "how is it right now" verdict can act on.
//
// No timings. Latency is scored from a window summary, where the percentiles
// are computed in SQL over enough samples to mean something — not from a
// handful of rows sorted on the client.
type RecentRun struct {
	OK       bool  `json:"ok"`
	AnswerOK *bool `json:"answer_ok"`
}

// RecentCycle is one probe cycle with its stored attribution.
//
// This is the only part of the summary payload that is NOT scoped to the
// window, and deliberately so: the verdict banner answers "how is it right
// now", and reading that off aggregate fault COUNTS over the selected window is
// how one failed cycle published DEGRADED until it aged out — three months, on
// the 3mo view.
//
// Fault is served raw, including the historical 'route' and the empty string a
// cycle with no attribution row would carry. Deciding what those mean is the
// client's job: see ui/src/verdict.ts.
type RecentCycle struct {
	At     time.Time            `json:"at"`
	Fault  string               `json:"fault"`
	Models map[string]RecentRun `json:"models"`
}

// RecentCycleCount is how far back the recent block reaches. It is a payload
// size, not an opinion: the client decides what a red run means (RECENT_CYCLES
// in ui/src/verdict.ts, currently 12) and must never be able to ask for more
// evidence than this serves — so this stays comfortably above it. Three hours
// at the 5-minute cadence, which is what lets a quiet banner still say how long
// ago the last failure was.
const RecentCycleCount = 36

// Summary is the whole dashboard state for one window.
type Summary struct {
	Window string         `json:"window"`
	Cycles int            `json:"cycles"`
	Models []ModelSummary `json:"models"`
	Net    []NetSummary   `json:"net"`
	Faults map[string]int `json:"faults"`
	// Recent is NOT window-scoped — see RecentCycle for why. It rides along in
	// this response rather than in an endpoint of its own because the client
	// needs it on exactly the requests it already makes.
	Recent      []RecentCycle `json:"recent"`
	Skipped     int           `json:"skipped_runs"`
	GeneratedAt time.Time     `json:"generated_at"`
}

// censoredSQL builds the predicate for a run our own timeout ladder cut off,
// and the arguments that go with it.
//
// Built rather than written out so the class list lives in exactly one place —
// probe.CensoringErrorClasses. A literal list here would drift the moment a
// class is added to the ladder, and it would drift SILENTLY: the count would
// simply stop including the new class, which reads as "less truncation" rather
// than as a bug. Placeholders, never interpolation, on a public endpoint's path.
func censoredSQL(alias string) (string, []any) {
	marks := make([]string, len(probe.CensoringErrorClasses))
	args := make([]any, len(probe.CensoringErrorClasses))
	for i, c := range probe.CensoringErrorClasses {
		marks[i] = "?"
		args[i] = c
	}
	return fmt.Sprintf("%[1]s.ok = 0 AND %[1]s.error_class IN (%s)",
		alias, strings.Join(marks, ", ")), args
}

// percentileSQL builds the nearest-rank percentile expression for a column.
//
// SQLite has no percentile_cont, so the rank is computed with window functions:
// ROW_NUMBER over the ordered values, COUNT over the partition, then pick the
// row whose rank is ceil(n*p/100), floored at 1. Integer arithmetic does the
// ceiling — (n*p + 99)/100 — because SQLite's integer division truncates.
//
// Doing this in SQL rather than in Go matters at the 3-month window: fetching
// ~110 000 raw rows per request to sort them in memory would make the response
// cache the only thing standing between the site and its own database.
const percentileSQL = `
WITH vals AS (
	SELECT %s AS v FROM infer_probes i
	JOIN cycles c ON c.id = i.cycle_id
	WHERE i.model_id = ? AND i.probe = ? AND i.ok = 1 AND %s IS NOT NULL
	  AND c.started_at >= ?
),
ranked AS (
	SELECT v,
		ROW_NUMBER() OVER (ORDER BY v) AS rn,
		COUNT(*) OVER () AS n
	FROM vals
)
SELECT
	COALESCE(MAX(n), 0),
	MAX(CASE WHEN rn = MAX(1, (n * 50 + 99) / 100) THEN v END),
	MAX(CASE WHEN rn = MAX(1, (n * 95 + 99) / 100) THEN v END)
FROM ranked`

func (s *Store) stats(ctx context.Context, column, modelID, probeKind string, since time.Time) (Stats, error) {
	q := fmt.Sprintf(percentileSQL, column, column)
	var n int
	var p50, p95 sql.NullFloat64
	if err := s.db.QueryRowContext(ctx, q, modelID, probeKind, rfc(since)).Scan(&n, &p50, &p95); err != nil {
		return Stats{}, err
	}
	st := Stats{N: n, Sufficient: n >= MinSamplesForPercentile}
	if st.Sufficient {
		if p50.Valid {
			v := p50.Float64
			st.P50 = &v
		}
		if p95.Valid {
			v := p95.Float64
			st.P95 = &v
		}
	}
	return st, nil
}

// Summarize builds the dashboard state for a window.
func (s *Store) Summarize(ctx context.Context, w Window, models []string, probeKind string, now time.Time) (Summary, error) {
	since := now.Add(-w.Duration)
	out := Summary{
		Window: w.Key,
		Faults: map[string]int{}, GeneratedAt: now.UTC(),
	}

	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM cycles WHERE started_at >= ?`, rfc(since)).Scan(&out.Cycles); err != nil {
		return Summary{}, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM skipped_runs WHERE occurred_at >= ?`, rfc(since)).Scan(&out.Skipped); err != nil {
		return Summary{}, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT f.fault, count(*) FROM cycle_fault f
		JOIN cycles c ON c.id = f.cycle_id
		WHERE c.started_at >= ? GROUP BY f.fault`, rfc(since))
	if err != nil {
		return Summary{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var fault string
		var n int
		if err := rows.Scan(&fault, &n); err != nil {
			return Summary{}, err
		}
		out.Faults[fault] = n
	}
	if err := rows.Err(); err != nil {
		return Summary{}, err
	}

	// Not scoped to `since`, unlike everything above and below it. See
	// RecentCycle: the counts above describe the window, this describes now.
	recent, err := s.RecentCycles(ctx, probeKind)
	if err != nil {
		return Summary{}, err
	}
	out.Recent = recent

	for _, model := range models {
		ms, err := s.modelSummary(ctx, model, probeKind, since)
		if err != nil {
			return Summary{}, err
		}
		out.Models = append(out.Models, ms)
	}
	// The constants, not literals: netSummary decides whether to apply the
	// unattributable-cycle exclusion by comparing against probe.TargetMimoSGP,
	// and a literal drifting from it would silently switch the exclusion off
	// without failing a single test.
	for _, target := range []string{probe.TargetMimoSGP, probe.TargetRefSGP} {
		ns, err := s.netSummary(ctx, target, since)
		if err != nil {
			return Summary{}, err
		}
		out.Net = append(out.Net, ns)
	}
	return out, nil
}

func (s *Store) modelSummary(ctx context.Context, modelID, probeKind string, since time.Time) (ModelSummary, error) {
	ms := ModelSummary{ModelID: modelID, Probe: probeKind}

	var err error
	if ms.TTFT, err = s.stats(ctx, "ttft_ms", modelID, probeKind, since); err != nil {
		return ms, err
	}
	if ms.ITL, err = s.stats(ctx, "itl_p50_ms", modelID, probeKind, since); err != nil {
		return ms, err
	}
	if ms.TPS, err = s.stats(ctx, "output_tps", modelID, probeKind, since); err != nil {
		return ms, err
	}

	// Failures on unattributable cycles are excluded from the counting, not
	// merely from the percentiles.
	//
	// When nothing in Singapore answered, the inference probe failed too — on
	// connect, before it ever reached MiMo. Counting that as a failed ATTEMPT
	// charges MiMo for an outage the measurement cannot show was theirs: an hour
	// of our own connectivity being down is 12 cycles per model of manufactured
	// downtime. The availability strip has always said these are excluded;
	// this is where that finally becomes true.
	//
	// 'route' rides along with 'uplink': it is the historical half of the same
	// verdict, produced while a European reference host still split the two, and
	// in both cases MiMo AND the Singapore reference were unreachable. Stored
	// cycles still carry it, so excluding one without the other would leave the
	// same manufactured downtime in every window that reaches back far enough.
	//
	// The i.ok = 1 escape matters: the fault is attributed from TCP handshakes
	// taken at the top of the cycle, and a run that nonetheless SUCCEEDED is
	// positive evidence MiMo answered. Dropping it would also drop its answer
	// grade and its reasoning/cached-token canaries — the hard gates — and would
	// leave its latency in the percentiles while its attempt went uncounted.
	//
	// LEFT JOIN with COALESCE rather than an inner join: every cycle gets a
	// cycle_fault row in the same transaction, so a missing one is impossible
	// today — but an inner join would silently DROP such a cycle from
	// availability entirely, which is a worse failure than counting it.
	//
	// A window of nothing but unattributable failures lands on Attempts = 0,
	// which the clients already render as "no data" rather than as 0%.
	var correct, answered sql.NullInt64
	var maxReason, maxCached sql.NullInt64
	var censored sql.NullInt64
	censoredExpr, censoredArgs := censoredSQL("i")
	args := []any{modelID, probeKind, rfc(since), probe.FaultUplink, probe.FaultRoute}
	err = s.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT
			count(*),
			COALESCE(sum(i.ok), 0),
			COALESCE(sum(CASE WHEN i.answer_ok IS NOT NULL THEN 1 ELSE 0 END), 0),
			COALESCE(sum(CASE WHEN i.answer_ok = 1 THEN 1 ELSE 0 END), 0),
			max(i.reasoning_tokens),
			max(i.cached_tokens),
			COALESCE(sum(CASE WHEN %s THEN 1 ELSE 0 END), 0)
		FROM infer_probes i
		JOIN cycles c ON c.id = i.cycle_id
		LEFT JOIN cycle_fault f ON f.cycle_id = c.id
		WHERE i.model_id = ? AND i.probe = ? AND c.started_at >= ?
		  AND (i.ok = 1 OR COALESCE(f.fault, '') NOT IN (?, ?))`, censoredExpr),
		append(censoredArgs, args...)...,
	).Scan(&ms.Attempts, &ms.Succeeded, &answered, &correct, &maxReason, &maxCached, &censored)
	if err != nil {
		return ms, err
	}
	ms.Censored = int(censored.Int64)

	ms.Answered = int(answered.Int64)
	ms.Correct = int(correct.Int64)
	ms.MaxReasoningTokens = int(maxReason.Int64)
	ms.MaxCachedTokens = int(maxCached.Int64)
	if ms.Attempts > 0 {
		ms.Available = 100 * float64(ms.Succeeded) / float64(ms.Attempts)
	}
	// Correctness is suppressed on thin data for the same reason percentiles
	// are: one wrong answer out of three is not a 67% correctness rate.
	if ms.Answered >= MinSamplesForPercentile {
		pct := 100 * float64(ms.Correct) / float64(ms.Answered)
		ms.CorrectPct = &pct
	}
	return ms, nil
}

func (s *Store) netSummary(ctx context.Context, target string, since time.Time) (NetSummary, error) {
	ns := NetSummary{Target: target}

	var n int
	var p50, p95 sql.NullFloat64
	err := s.db.QueryRowContext(ctx, `
		WITH vals AS (
			SELECT n.connect_ms AS v FROM net_probes n
			JOIN cycles c ON c.id = n.cycle_id
			WHERE n.target = ? AND n.ok = 1 AND n.connect_ms IS NOT NULL
			  AND c.started_at >= ?
		),
		ranked AS (
			SELECT v, ROW_NUMBER() OVER (ORDER BY v) AS rn, COUNT(*) OVER () AS n FROM vals
		)
		SELECT COALESCE(MAX(n), 0),
			MAX(CASE WHEN rn = MAX(1, (n * 50 + 99) / 100) THEN v END),
			MAX(CASE WHEN rn = MAX(1, (n * 95 + 99) / 100) THEN v END)
		FROM ranked`, target, rfc(since)).Scan(&n, &p50, &p95)
	if err != nil {
		return ns, err
	}
	ns.Connect = Stats{N: n, Sufficient: n >= MinSamplesForPercentile}
	if ns.Connect.Sufficient {
		if p50.Valid {
			v := p50.Float64
			ns.Connect.P50 = &v
		}
		if p95.Valid {
			v := p95.Float64
			ns.Connect.P95 = &v
		}
	}

	// The unattributable-cycle exclusion applies to MiMo's target and to it
	// alone.
	//
	// MiMo's edge is a PROVIDER figure, and the promise is that we never publish
	// our own outage as theirs — so a cycle where nothing at all answered is
	// excluded here for exactly the reason it is excluded from model
	// availability.
	//
	// The reference host's own availability is not a provider figure. It is the
	// instrument, and its raw reachability is a diagnostic about OUR setup:
	// DEPLOY.md tells an operator to watch it, and filtering the same cycles out
	// would make a flaky reference look healthier than it is — hiding the thing
	// the number exists to reveal. So the references keep their unfiltered
	// count, and the asymmetry is the point rather than an oversight.
	//
	// The n.ok = 1 escape is unreachable under today's attribution — a cycle is
	// only 'uplink'/'route' when the mimo handshake failed — but it is what makes
	// ns.Connect.N <= ns.Attempts hold structurally rather than by coincidence,
	// exactly as i.ok = 1 does in modelSummary. Keep it if the rule ever changes.
	excludeUplink := target == probe.TargetMimoSGP
	err = s.db.QueryRowContext(ctx, `
		SELECT count(*), COALESCE(sum(n.ok), 0) FROM net_probes n
		JOIN cycles c ON c.id = n.cycle_id
		LEFT JOIN cycle_fault f ON f.cycle_id = c.id
		WHERE n.target = ? AND c.started_at >= ?
		  AND (? = 0 OR n.ok = 1 OR COALESCE(f.fault, '') NOT IN (?, ?))`,
		target, rfc(since), boolToInt(excludeUplink), probe.FaultUplink, probe.FaultRoute,
	).Scan(&ns.Attempts, &ns.Succeeded)
	if err != nil {
		return ns, err
	}
	if ns.Attempts > 0 {
		ns.Available = 100 * float64(ns.Succeeded) / float64(ns.Attempts)
	}
	return ns, nil
}

// Point is one bucket of a series.
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

// Series returns bucketed percentiles for one metric.
//
// Buckets deliberately carry NO minimum-sample threshold, unlike the headline
// figures: a 5-minute bucket holds at most one sample per model, so applying
// MinSamplesForPercentile here would blank every short-window chart. The
// headline figures keep the strict threshold; a chart point is a shape, not a
// published number. Each point carries its own N so a client can weight it.
func (s *Store) Series(ctx context.Context, column, modelID, probeKind string, w Window, now time.Time) ([]Point, error) {
	if err := checkSeriesColumn(column); err != nil {
		return nil, err
	}
	since := now.Add(-w.Duration)
	bucketSecs := int64(w.Bucket / time.Second)
	censoredExpr, censoredArgs := censoredSQL("i")

	// The bucket universe is the UNION of the two sides, not the successful side
	// alone. A bucket in which every run was cut off has no successful sample to
	// group, so on an inner reading it would not exist at all — and a
	// non-existent bucket renders as a gap, identical to a stretch where the
	// probe was not running. That is the worst possible collapse: total
	// truncation is the one case where the reader most needs to be told.
	q := fmt.Sprintf(`
		WITH vals AS (
			SELECT unixepoch(c.started_at) / %[1]d * %[1]d AS bucket, i.%[2]s AS v
			FROM infer_probes i
			JOIN cycles c ON c.id = i.cycle_id
			WHERE i.model_id = ? AND i.probe = ? AND i.ok = 1 AND i.%[2]s IS NOT NULL
			  AND c.started_at >= ?
		),
		ranked AS (
			SELECT bucket, v,
				ROW_NUMBER() OVER (PARTITION BY bucket ORDER BY v) AS rn,
				COUNT(*) OVER (PARTITION BY bucket) AS n
			FROM vals
		),
		agg AS (
			SELECT bucket, MAX(n) AS n,
				MAX(CASE WHEN rn = MAX(1, (n * 50 + 99) / 100) THEN v END) AS p50,
				MAX(CASE WHEN rn = MAX(1, (n * 95 + 99) / 100) THEN v END) AS p95
			FROM ranked GROUP BY bucket
		),
		cens AS (
			SELECT unixepoch(c.started_at) / %[1]d * %[1]d AS bucket, count(*) AS c
			FROM infer_probes i
			JOIN cycles c ON c.id = i.cycle_id
			LEFT JOIN cycle_fault f ON f.cycle_id = c.id
			WHERE i.model_id = ? AND i.probe = ? AND %[3]s
			  AND c.started_at >= ?
			  -- The SAME unattributable-cycle exclusion the summary applies to
			  -- its censored count. A band on the chart is a claim about MiMo's
			  -- distribution, exactly as the count on the card is, and a run cut
			  -- off during OUR uplink outage is neither. Without this the card
			  -- refuses to count a run that the chart still bands, over a caption
			  -- saying probes here were cut off — two published figures for one
			  -- concept, disagreeing.
			  --
			  -- Reachable, not theoretical: the fault is attributed from TCP
			  -- handshakes at the top of the cycle while the HTTP request can get
			  -- further, so a timeout on an 'uplink' cycle is a real row.
			  AND COALESCE(f.fault, '') NOT IN (?, ?)
			GROUP BY bucket
		),
		buckets AS (
			SELECT bucket FROM agg UNION SELECT bucket FROM cens
		)
		SELECT b.bucket, COALESCE(a.n, 0), a.p50, a.p95, COALESCE(x.c, 0)
		FROM buckets b
		LEFT JOIN agg a ON a.bucket = b.bucket
		LEFT JOIN cens x ON x.bucket = b.bucket
		ORDER BY b.bucket`, bucketSecs, column, censoredExpr)

	args := []any{modelID, probeKind, rfc(since), modelID, probeKind}
	args = append(args, censoredArgs...)
	args = append(args, rfc(since), probe.FaultUplink, probe.FaultRoute)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Point
	for rows.Next() {
		var p Point
		var p50, p95 sql.NullFloat64
		if err := rows.Scan(&p.T, &p.N, &p50, &p95, &p.Censored); err != nil {
			return nil, err
		}
		if p50.Valid {
			v := p50.Float64
			p.P50 = &v
		}
		if p95.Valid {
			v := p95.Float64
			p.P95 = &v
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// NetSeries returns bucketed connect_ms for one ping target.
func (s *Store) NetSeries(ctx context.Context, target string, w Window, now time.Time) ([]Point, error) {
	since := now.Add(-w.Duration)
	bucketSecs := int64(w.Bucket / time.Second)

	q := fmt.Sprintf(`
		WITH vals AS (
			SELECT unixepoch(c.started_at) / %[1]d * %[1]d AS bucket, n.connect_ms AS v
			FROM net_probes n
			JOIN cycles c ON c.id = n.cycle_id
			WHERE n.target = ? AND n.ok = 1 AND n.connect_ms IS NOT NULL
			  AND c.started_at >= ?
		),
		ranked AS (
			SELECT bucket, v,
				ROW_NUMBER() OVER (PARTITION BY bucket ORDER BY v) AS rn,
				COUNT(*) OVER (PARTITION BY bucket) AS n
			FROM vals
		)
		SELECT bucket, MAX(n),
			MAX(CASE WHEN rn = MAX(1, (n * 50 + 99) / 100) THEN v END),
			MAX(CASE WHEN rn = MAX(1, (n * 95 + 99) / 100) THEN v END)
		FROM ranked GROUP BY bucket ORDER BY bucket`, bucketSecs)

	rows, err := s.db.QueryContext(ctx, q, target, rfc(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Point
	for rows.Next() {
		var p Point
		var p50, p95 sql.NullFloat64
		if err := rows.Scan(&p.T, &p.N, &p50, &p95); err != nil {
			return nil, err
		}
		if p50.Valid {
			v := p50.Float64
			p.P50 = &v
		}
		if p95.Valid {
			v := p95.Float64
			p.P95 = &v
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// seriesColumns is the allow-list of chartable columns.
//
// An allow-list because the column name is interpolated into SQL — it cannot be
// a bound parameter — so this is the boundary that keeps a public, caller-
// supplied metric name from becoming an injection point.
var seriesColumns = map[string]bool{
	"ttft_ms":    true,
	"ttfat_ms":   true,
	"total_ms":   true,
	"itl_p50_ms": true,
	"itl_p95_ms": true,
	"output_tps": true,
}

func checkSeriesColumn(column string) error {
	if !seriesColumns[column] {
		return fmt.Errorf("unknown series column %q", column)
	}
	return nil
}

// Sample is one raw row, as served publicly.
//
// Note what is absent: error_detail. A provider error body can echo request
// fragments, so this projection — which carries every run, healthy or not —
// does not select it. The one public shape that does is Failure, over at most a
// handful of failed runs and only after redaction and clipping.
type Sample struct {
	At        time.Time `json:"at"`
	ModelID   string    `json:"model_id"`
	Probe     string    `json:"probe"`
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
	// cost rather than what it produced, and that the cost panel already sums it. The
	// sum is the wrong shape for this table: prompt_tokens is ~20 on short and
	// ~3800 on wide, and that 200x step IS the difference between the two
	// probes. Reading it off a daily total means reconstructing per-run input
	// from an aggregate, on the one surface that promises nothing is aggregated
	// away.
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

// LastProbeAtByModel returns when a probe kind last RAN for each model. A model
// that has never run it is absent from the map rather than present with a zero
// time, so "never" stays distinguishable from "at the epoch".
//
// The scheduler asks this rather than counting cycles, because a counter only
// knows about the process holding it: restart the daemon and a memory-resident
// counter says "cycle zero" again, which re-fires the hourly probe on a daemon
// that has been up for three minutes. The database remembers across restarts;
// nothing else here does.
//
// Per MODEL, not one timestamp for the kind. The wide probe runs for one model
// at a time and each model is due on its own hour, so a single MAX across all of
// them would answer a question no longer being asked: it would report the OTHER
// model's recent run and hold this one back indefinitely.
//
// Attempts, not successes: a failed wide probe still cost the endpoint the
// request, and retrying it every five minutes until one succeeds is exactly the
// behaviour this exists to prevent. `skipped_runs` carries overruns, which were
// never sent, so they are correctly absent here.
func (s *Store) LastProbeAtByModel(ctx context.Context, probeKind string) (map[string]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT i.model_id, MAX(c.started_at)
		FROM infer_probes i
		JOIN cycles c ON c.id = i.cycle_id
		WHERE i.probe = ?
		GROUP BY i.model_id`, probeKind)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string]time.Time{}
	for rows.Next() {
		var model string
		var at sql.NullString
		if err := rows.Scan(&model, &at); err != nil {
			return nil, err
		}
		// MAX over a grouped set cannot be NULL when the group exists, but the
		// column is nullable and a scan that assumed otherwise would panic on a
		// schema change rather than skip a row.
		if !at.Valid {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, at.String)
		if err != nil {
			return nil, err
		}
		out[model] = t
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// MaxSampleLimit clamps the raw-sample endpoint server-side.
const MaxSampleLimit = 500

// RecentSamples returns the most recent raw rows for a model.
func (s *Store) RecentSamples(ctx context.Context, modelID, probeKind string, limit int) ([]Sample, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > MaxSampleLimit {
		limit = MaxSampleLimit
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT c.started_at, i.model_id, i.probe, i.ttft_ms, i.total_ms,
		       i.itl_p50_ms, i.output_tps, i.prompt_tokens, i.output_tokens,
		       i.ok, i.answer_ok, i.error_class
		FROM infer_probes i
		JOIN cycles c ON c.id = i.cycle_id
		WHERE i.model_id = ? AND i.probe = ?
		ORDER BY c.started_at DESC, i.id DESC
		LIMIT ?`, modelID, probeKind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Sample
	for rows.Next() {
		var s Sample
		var at string
		var okInt int
		var answerOK sql.NullInt64
		var ttft, total, itl, tps sql.NullFloat64
		var promptTokens, outTokens sql.NullInt64
		// question_id is recorded but never selected here: it names what is
		// being asked, and this row is served publicly.
		var class sql.NullString
		if err := rows.Scan(&at, &s.ModelID, &s.Probe, &ttft, &total, &itl, &tps,
			&promptTokens, &outTokens, &okInt, &answerOK, &class); err != nil {
			return nil, err
		}
		s.At, _ = time.Parse(time.RFC3339Nano, at)
		s.OK = okInt == 1
		s.TTFTMs = nullF(ttft)
		s.TotalMs = nullF(total)
		s.ITLP50Ms = nullF(itl)
		s.OutputTPS = nullF(tps)
		s.PromptTokens = nullI(promptTokens)
		s.OutputTokens = nullI(outTokens)
		if answerOK.Valid {
			b := answerOK.Int64 == 1
			s.AnswerOK = &b
		}
		s.ErrorClass = nullS(class)
		out = append(out, s)
	}
	return out, rows.Err()
}

// MaxFailureDetail bounds the upstream text a served failure quotes.
//
// The same 300 the scheduler allows a log line, and for the same reason: a
// provider answering with an HTML error page would otherwise put a whole
// document in the payload. It matters more here than in a log, because this
// column is the one operator-only field the failures block now serves — see
// Failure — so the bound is a containment measure, not a formatting one.
const MaxFailureDetail = 300

// clip shortens s to n bytes, marking that it did so, cutting on a rune
// boundary so no half-character reaches the page.
//
// Callers must redact BEFORE clipping: cutting a credential in half leaves the
// first half of a live key in the response. redact.String's own doc comment
// states the same ordering rule.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// Failure is one failed inference call, as the errors block serves it.
//
// This is the ONE public shape that carries error_detail, and it does so under
// two conditions that nothing else may drop: the text passes through
// redact.String, and it is clipped to MaxFailureDetail. Both happen in
// RecentFailures rather than in the handler, so no future caller can select the
// raw column by taking a different route to it.
//
// Understand what is being traded. The column stores raw upstream bytes — on an
// HTTP error it is the response body verbatim — and a provider error body can
// echo request fragments, which is why every other public projection here omits
// it. Redaction is a denylist tuned to the MiMo key's shape, so it is a strong
// bet against a leaked credential and a weaker one against anything else the
// upstream chose to quote back. The clip is what bounds that residue.
//
// HTTPStatus carries no such risk — it is an integer — and it is the field that
// tells a 429 apart from a 503 when both land as error_class "http_error".
type Failure struct {
	At         time.Time `json:"at"`
	ModelID    string    `json:"model_id"`
	Probe      string    `json:"probe"`
	ErrorClass *string   `json:"error_class"`
	HTTPStatus *int64    `json:"http_status"`
	// Empty when the run recorded no detail — a transport failure often has
	// only its class. Absent rather than a placeholder, so the client decides
	// how to draw "nothing to quote".
	ErrorDetail string `json:"error_detail"`
}

// MaxFailureLimit clamps how many failures any caller can ask for.
const MaxFailureLimit = 50

// RecentFailures returns the most recent FAILED inference calls across every
// configured model and both probe kinds, newest first.
//
// Deliberately its own query rather than a filter over RecentSamples. That
// projection is capped per model and probe at what the raw table draws — about
// three quarters of an hour of runs — so filtering it client-side would answer
// "did anything fail in the last 45 minutes", which on a healthy endpoint is an
// empty block that looks broken. This reaches back over `since` instead and
// returns nothing when nothing failed, which is a different and honest answer.
//
// `since` is the caller's, and the dashboard fixes it at a day regardless of
// which window the reader selected — the same independence RecentCycles has,
// and for the same reason: the last five failures are a fact about the endpoint,
// not about the chart selector.
//
// Models are filtered to the configured list. A model that has been retired
// from the config still has rows in the database, and surfacing its failures
// under a name no other panel draws would read as an outage in something the
// page does not otherwise mention.
func (s *Store) RecentFailures(ctx context.Context, models []string, since time.Time, limit int) ([]Failure, error) {
	if len(models) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}
	if limit > MaxFailureLimit {
		limit = MaxFailureLimit
	}

	// Placeholders built from len(models), never from the values: the model
	// names are still bound parameters, so nothing caller-shaped reaches the
	// SQL text.
	args := make([]any, 0, len(models)+2)
	for _, m := range models {
		args = append(args, m)
	}
	args = append(args, since.UTC().Format(time.RFC3339Nano), limit)

	rows, err := s.db.QueryContext(ctx, `
		SELECT c.started_at, i.model_id, i.probe, i.error_class, i.http_status,
		       i.error_detail
		FROM infer_probes i
		JOIN cycles c ON c.id = i.cycle_id
		WHERE i.ok = 0
		  AND i.model_id IN (`+placeholders(len(models))+`)
		  AND c.started_at >= ?
		ORDER BY c.started_at DESC, i.id DESC
		LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Failure
	for rows.Next() {
		var f Failure
		var at string
		var class, detail sql.NullString
		var status sql.NullInt64
		if err := rows.Scan(&at, &f.ModelID, &f.Probe, &class, &status, &detail); err != nil {
			return nil, err
		}
		f.At, _ = time.Parse(time.RFC3339Nano, at)
		f.ErrorClass = nullS(class)
		f.HTTPStatus = nullI(status)
		// Redact first, then clip. The other order leaves half a key behind.
		f.ErrorDetail = clip(redact.String(detail.String), MaxFailureDetail)
		out = append(out, f)
	}
	return out, rows.Err()
}

// placeholders renders n bound-parameter markers for an IN clause.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// Pulse is one cycle reduced to what a single bar of the strip draws: when it
// ran, how tall it is, what colour it is, and what the tooltip says.
//
// It exists so a day of cycles can reach the page without the page being handed
// a day of measurements. The strip needs 288 rows — one bar each — but it never
// reads total_ms, itl_p50_ms or output_tps, and repeating model_id and probe on
// every row restates what the envelope already said once. Serving Sample here
// would disclose the full detail series to draw a shape.
//
// The ten rows that DO show every number come from RecentSamples at limit=10.
// Two narrow requests, not one wide one.
type Pulse struct {
	At         time.Time `json:"at"`
	TTFTMs     *float64  `json:"ttft_ms"`
	OK         bool      `json:"ok"`
	AnswerOK   *bool     `json:"answer_ok"`
	ErrorClass *string   `json:"error_class"`
}

// RecentPulse returns the most recent cycles for a model, projected down to the
// strip's fields. Newest first, like RecentSamples — the client reverses.
func (s *Store) RecentPulse(ctx context.Context, modelID, probeKind string, limit int) ([]Pulse, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > MaxSampleLimit {
		limit = MaxSampleLimit
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT c.started_at, i.ttft_ms, i.ok, i.answer_ok, i.error_class
		FROM infer_probes i
		JOIN cycles c ON c.id = i.cycle_id
		WHERE i.model_id = ? AND i.probe = ?
		ORDER BY c.started_at DESC, i.id DESC
		LIMIT ?`, modelID, probeKind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Pulse
	for rows.Next() {
		var p Pulse
		var at string
		var okInt int
		var ttft sql.NullFloat64
		var answerOK sql.NullInt64
		// error_detail is not selected here for the same reason it is not
		// selected in RecentSamples: it can echo request fragments, and a day
		// of bars is the last place to quote upstream bytes. Failure is where
		// it surfaces, bounded and redacted.
		var class sql.NullString
		if err := rows.Scan(&at, &ttft, &okInt, &answerOK, &class); err != nil {
			return nil, err
		}
		p.At, _ = time.Parse(time.RFC3339Nano, at)
		p.TTFTMs = nullF(ttft)
		p.OK = okInt == 1
		if answerOK.Valid {
			b := answerOK.Int64 == 1
			p.AnswerOK = &b
		}
		p.ErrorClass = nullS(class)
		out = append(out, p)
	}
	return out, rows.Err()
}

// RecentCycles returns the last RecentCycleCount cycles, newest first, each
// with its stored fault attribution and every model's outcome in it.
//
// Deliberately unbounded by any window. This is the block the verdict banner
// reads, and bounding it by `since` would tie the banner back to the chart
// selector — which is the bug it exists to fix. The handler test asserting the
// block is identical on 24h and 3mo is what holds that line.
//
// Note what this query does NOT do: it does not exclude uplink and route
// cycles, unlike modelSummary and netSummary. Those compute availability FROM
// the attribution, so an unattributable cycle has to come out of the
// denominator. This one REPORTS the attribution and lets the client decide —
// so the uplink/route pairing lives in ui/src/verdict.ts instead, and both
// classes have to be handled there.
func (s *Store) RecentCycles(ctx context.Context, probeKind string) ([]RecentCycle, error) {
	// LEFT JOIN on both sides. A cycle whose fault row is missing must still
	// appear — dropping it would make the gap look like a clean stretch — and so
	// must one that recorded no inference run at all.
	rows, err := s.db.QueryContext(ctx, `
		WITH recent AS (
			SELECT c.id, c.started_at, COALESCE(f.fault, '') AS fault
			FROM cycles c
			LEFT JOIN cycle_fault f ON f.cycle_id = c.id
			ORDER BY c.started_at DESC, c.id DESC
			LIMIT ?
		)
		SELECT r.id, r.started_at, r.fault, i.model_id, i.ok, i.answer_ok
		FROM recent r
		LEFT JOIN infer_probes i ON i.cycle_id = r.id AND i.probe = ?
		ORDER BY r.started_at DESC, r.id DESC`, RecentCycleCount, probeKind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// One row per (cycle, model), so consecutive rows collapse into one cycle.
	// Grouped on the cycle id, not the timestamp: two cycles sharing a
	// started_at is pathological but would silently merge two verdicts' worth of
	// evidence into one.
	var out []RecentCycle
	var cur *RecentCycle
	var curID int64 = -1
	for rows.Next() {
		var id int64
		var at, fault string
		var modelID sql.NullString
		var okInt, answerOK sql.NullInt64
		if err := rows.Scan(&id, &at, &fault, &modelID, &okInt, &answerOK); err != nil {
			return nil, err
		}
		if cur == nil || id != curID {
			parsed, err := time.Parse(time.RFC3339Nano, at)
			if err != nil {
				return nil, err
			}
			out = append(out, RecentCycle{
				At: parsed, Fault: fault, Models: map[string]RecentRun{},
			})
			cur = &out[len(out)-1]
			curID = id
		}
		if !modelID.Valid {
			continue
		}
		run := RecentRun{OK: okInt.Int64 == 1}
		if answerOK.Valid {
			b := answerOK.Int64 == 1
			run.AnswerOK = &b
		}
		cur.Models[modelID.String] = run
	}
	return out, rows.Err()
}

func nullF(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}

func nullI(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}

func nullS(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
