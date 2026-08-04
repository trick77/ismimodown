package samples

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/trick77/mimostats/internal/probe"
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

// NowWindow is what "right now" means on the dashboard.
//
// Deliberately short: a 24-hour median hides a problem that started an hour
// ago, which is exactly the problem you most want to see on opening the page.
const NowWindow = 30 * time.Minute

// BaselineWindow is the rolling reference that higher-is-worse metrics are
// scored against. Rolling rather than absolute so the page keeps working if
// MiMo gets permanently faster or slower.
const BaselineWindow = 7 * 24 * time.Hour

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

// Summary is the whole dashboard state for one window.
type Summary struct {
	Window      string         `json:"window"`
	Cycles      int            `json:"cycles"`
	Models      []ModelSummary `json:"models"`
	Net         []NetSummary   `json:"net"`
	Faults      map[string]int `json:"faults"`
	Skipped     int            `json:"skipped_runs"`
	GeneratedAt time.Time      `json:"generated_at"`
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
	// downtime. /api/methodology and the availability strip have both always
	// said these are excluded; this is where that finally becomes true.
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
	err = s.db.QueryRowContext(ctx, `
		SELECT
			count(*),
			COALESCE(sum(i.ok), 0),
			COALESCE(sum(CASE WHEN i.answer_ok IS NOT NULL THEN 1 ELSE 0 END), 0),
			COALESCE(sum(CASE WHEN i.answer_ok = 1 THEN 1 ELSE 0 END), 0),
			max(i.reasoning_tokens),
			max(i.cached_tokens)
		FROM infer_probes i
		JOIN cycles c ON c.id = i.cycle_id
		LEFT JOIN cycle_fault f ON f.cycle_id = c.id
		WHERE i.model_id = ? AND i.probe = ? AND c.started_at >= ?
		  AND (i.ok = 1 OR COALESCE(f.fault, '') NOT IN (?, ?))`,
		modelID, probeKind, rfc(since), probe.FaultUplink, probe.FaultRoute,
	).Scan(&ms.Attempts, &ms.Succeeded, &answered, &correct, &maxReason, &maxCached)
	if err != nil {
		return ms, err
	}

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
	N int   `json:"n"`
	// Values are nil when the bucket produced no percentile at all. A null
	// renders as a gap; a zero would render as a floor, which is a lie.
	//
	// Note that no minimum-sample threshold is applied per bucket: a bucket
	// exists only because at least one successful sample landed in it, so N is
	// always >= 1 here. Suppression is a headline-figure rule (see
	// MinSamplesForPercentile), deliberately not a chart rule.
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
		)
		SELECT bucket, MAX(n),
			MAX(CASE WHEN rn = MAX(1, (n * 50 + 99) / 100) THEN v END),
			MAX(CASE WHEN rn = MAX(1, (n * 95 + 99) / 100) THEN v END)
		FROM ranked GROUP BY bucket ORDER BY bucket`, bucketSecs, column)

	rows, err := s.db.QueryContext(ctx, q, modelID, probeKind, rfc(since))
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
// Note what is absent: error_detail. It is operator-only, because a provider
// error body can echo request fragments, and a test asserts it never appears in
// any public payload.
type Sample struct {
	At         time.Time `json:"at"`
	ModelID    string    `json:"model_id"`
	Probe      string    `json:"probe"`
	TTFTMs     *float64  `json:"ttft_ms"`
	TotalMs    *float64  `json:"total_ms"`
	ITLP50Ms   *float64  `json:"itl_p50_ms"`
	OutputTPS  *float64  `json:"output_tps"`
	OK         bool      `json:"ok"`
	AnswerOK   *bool     `json:"answer_ok"`
	ErrorClass *string   `json:"error_class"`
}

// LastProbeAt returns when a probe kind last RAN for any model, and whether it
// ever has.
//
// The scheduler asks this rather than counting cycles, because a counter only
// knows about the process holding it: restart the daemon and a memory-resident
// counter says "cycle zero" again, which re-fires the hourly probe on a daemon
// that has been up for three minutes. The database remembers across restarts;
// nothing else here does.
//
// Attempts, not successes: a failed wide probe still cost the endpoint the
// request, and retrying it every five minutes until one succeeds is exactly the
// behaviour this exists to prevent. `skipped_runs` carries overruns, which were
// never sent, so they are correctly absent here.
func (s *Store) LastProbeAt(ctx context.Context, probeKind string) (time.Time, bool, error) {
	var at sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT MAX(c.started_at)
		FROM infer_probes i
		JOIN cycles c ON c.id = i.cycle_id
		WHERE i.probe = ?`, probeKind).Scan(&at)
	if err != nil {
		return time.Time{}, false, err
	}
	if !at.Valid {
		return time.Time{}, false, nil
	}
	t, err := time.Parse(time.RFC3339Nano, at.String)
	if err != nil {
		return time.Time{}, false, err
	}
	return t, true, nil
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
		       i.itl_p50_ms, i.output_tps, i.ok, i.answer_ok, i.error_class
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
		// question_id is recorded but never selected here: it names what is
		// being asked, and this row is served publicly.
		var class sql.NullString
		if err := rows.Scan(&at, &s.ModelID, &s.Probe, &ttft, &total, &itl, &tps,
			&okInt, &answerOK, &class); err != nil {
			return nil, err
		}
		s.At, _ = time.Parse(time.RFC3339Nano, at)
		s.OK = okInt == 1
		s.TTFTMs = nullF(ttft)
		s.TotalMs = nullF(total)
		s.ITLP50Ms = nullF(itl)
		s.OutputTPS = nullF(tps)
		if answerOK.Valid {
			b := answerOK.Int64 == 1
			s.AnswerOK = &b
		}
		s.ErrorClass = nullS(class)
		out = append(out, s)
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

func nullS(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
