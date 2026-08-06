package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/trick77/ismimodown/internal/probe"
	"github.com/trick77/ismimodown/internal/samples"
	"github.com/trick77/ismimodown/internal/store"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(db); err != nil {
		t.Fatalf("store.Migrate: %v", err)
	}
	return db
}

// fakePinger always answers, so a test can isolate the inference side.
type fakePinger struct {
	mu      sync.Mutex
	calls   []string
	results map[string]bool
}

func (f *fakePinger) Ping(_ context.Context, target, _ string) probe.NetResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, target)
	ok := true
	if f.results != nil {
		v, present := f.results[target]
		ok = !present || v
	}
	return probe.NetResult{Target: target, OK: ok, ConnectMs: 100}
}

// fakeProber records what it was asked and can block, so the overrun guard is
// testable without timing luck.
type fakeProber struct {
	mu      sync.Mutex
	reqs    []probe.Request
	running atomic.Int32
	// block holds a run inside Run until closed. blockModel scopes that to ONE
	// model: blocking every model would deadlock the overrun test, because the
	// second cycle's other model would wait on the same channel instead of
	// proceeding past the skipped one.
	block      chan struct{}
	blockModel string
	err        error
}

func (f *fakeProber) Run(_ context.Context, req probe.Request) (probe.InferResult, error) {
	f.running.Add(1)
	defer f.running.Add(-1)

	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()

	if f.block != nil && (f.blockModel == "" || f.blockModel == req.ModelID) {
		<-f.block
	}
	if f.err != nil {
		return probe.InferResult{}, f.err
	}
	return probe.InferResult{
		ModelID: req.ModelID, Probe: req.Probe, QuestionID: req.QuestionID,
		TTFTMs: 900, TotalMs: 1700, ITLP50Ms: 24, OK: true,
	}, nil
}

func (f *fakeProber) requests() []probe.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]probe.Request(nil), f.reqs...)
}

func newTestScheduler(t *testing.T, prober Prober, pinger Pinger) (*Scheduler, *sql.DB) {
	t.Helper()
	db := openTestDB(t)
	return New(Deps{
		Store:  samples.New(db),
		Prober: prober,
		Pinger: pinger,
		Models: []string{"mimo-v2.5", "mimo-v2.5-pro"},
	}), db
}

// newSchedulerOn builds a scheduler against an EXISTING database and a movable
// clock. Both matter for the wide cadence: the clock so a test can reach the
// hour without sleeping, and the shared database so a second scheduler can be
// stood up on the same data — which is all a restart is.
func newSchedulerOn(db *sql.DB, prober Prober, pinger Pinger, now *time.Time) *Scheduler {
	return New(Deps{
		Store:  samples.New(db),
		Prober: prober,
		Pinger: pinger,
		Models: []string{"mimo-v2.5", "mimo-v2.5-pro"},
		Now:    func() time.Time { return *now },
	})
}

// seedWideProbe writes a completed wide probe at `at`, so the cadence rule sees
// one already on the clock. Tests that are not about the wide cadence use it to
// keep wide out of their cycles.
func seedWideProbe(t *testing.T, db *sql.DB, at time.Time) {
	t.Helper()
	if _, err := samples.New(db).Save(context.Background(), samples.Cycle{
		StartedAt: at,
		Net: []probe.NetResult{
			{Target: probe.TargetMimoSGP, OK: true, ConnectMs: 170},
			{Target: probe.TargetRefSGP, OK: true, ConnectMs: 265},
		},
		Infer: []probe.InferResult{{
			ModelID: "mimo-v2.5", Probe: probe.ProbeWide, TTFTMs: 1200, OK: true,
		}},
	}); err != nil {
		t.Fatalf("seed wide probe: %v", err)
	}
}

func wideRuns(prober *fakeProber) int {
	n := 0
	for _, r := range prober.requests() {
		if r.Probe == probe.ProbeWide {
			n++
		}
	}
	return n
}

// gradingProber returns one fixed graded result, so a test can drive the
// wrong-answer path without a server.
type gradingProber struct{ res probe.InferResult }

func (g *gradingProber) Run(_ context.Context, req probe.Request) (probe.InferResult, error) {
	res := g.res
	res.ModelID, res.Probe, res.QuestionID = req.ModelID, req.Probe, req.QuestionID
	return res, nil
}

// captureLogs redirects the default logger for one test and returns what was
// written to it.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// A wrong answer is stored as a bare zero, so the log line is the only record
// of what the model actually said — and without it a reroute to a smaller
// model, a truncated reply and a provider error served as a clean stream are
// indistinguishable forever after.
func TestWrongAnswerLogsTheReply(t *testing.T) {
	answerOK := false
	prober := &gradingProber{res: probe.InferResult{
		OK: true, TTFTMs: 900, TotalMs: 1700,
		AnswerOK: &answerOK, Content: "The capital of France is Lyon.",
		FinishReason: "stop",
	}}
	s, _ := newTestScheduler(t, prober, &fakePinger{})
	buf := captureLogs(t)

	if _, ok := s.runProbe(context.Background(), "mimo-v2.5", probe.ProbeShort, 7, time.Now()); !ok {
		t.Fatal("runProbe reported the run as skipped")
	}

	got := buf.String()
	for _, want := range []string{
		"answer graded wrong", "The capital of France is Lyon.",
		"mimo-v2.5", `"finish_reason":"stop"`, `"cycle":7`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("log is missing %q; got %s", want, got)
		}
	}
}

// The reply is quoted, not dumped: a model that ignores MaxTokens, or a proxy
// answering with a page of HTML inside a valid stream, must not be able to put
// an unbounded string into every log line.
func TestLoggedReplyIsBounded(t *testing.T) {
	answerOK := false
	prober := &gradingProber{res: probe.InferResult{
		OK: true, AnswerOK: &answerOK, Content: strings.Repeat("é", 4000),
	}}
	s, _ := newTestScheduler(t, prober, &fakePinger{})
	buf := captureLogs(t)

	s.runProbe(context.Background(), "mimo-v2.5", probe.ProbeShort, 1, time.Now())

	// Measured per line rather than over the buffer: the run now also emits its
	// own "inference call" line, and widening a whole-buffer budget to absorb it
	// would quietly buy a second line's worth of headroom for the clip this test
	// exists to hold.
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if n := len(line); n > maxLoggedContent+512 {
			t.Errorf("log line is %d bytes, want the reply clipped near %d: %s",
				n, maxLoggedContent, line)
		}
	}
	// Clipped on a rune boundary, so the evidence is not a row of replacement
	// glyphs where the last character was.
	if strings.Contains(buf.String(), `�`) {
		t.Errorf("reply was cut mid-rune: %s", buf.String())
	}
}

// Silence on the paths that are not a wrong answer. A correct run is not news,
// and a run that FAILED has no answer at all — logging it as one would be the
// same conflation the nil verdict exists to prevent.
func TestOnlyWrongAnswersAreLogged(t *testing.T) {
	answerOK := true
	for _, tc := range []struct {
		name string
		res  probe.InferResult
	}{
		{"correct answer", probe.InferResult{OK: true, AnswerOK: &answerOK, Content: "Paris"}},
		{"ungraded run", probe.InferResult{OK: true, Content: "a wide summary"}},
		{"failed run", probe.InferResult{ErrorClass: probe.ErrClassHTTP, ErrorDetail: "502"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestScheduler(t, &gradingProber{res: tc.res}, &fakePinger{})
			buf := captureLogs(t)

			s.runProbe(context.Background(), "mimo-v2.5", probe.ProbeShort, 1, time.Now())

			if strings.Contains(buf.String(), "answer graded wrong") {
				t.Errorf("logged a wrong answer for a %s: %s", tc.name, buf.String())
			}
		})
	}
}

// Every inference call leaves a line, whichever prompt it carried. "cycle
// complete" names the model that went wide and counts the rest, so without this
// a short run is invisible in the log while a wide one is not.
func TestEveryInferenceCallIsLogged(t *testing.T) {
	answerOK := true
	for _, tc := range []struct {
		kind string
		res  probe.InferResult
	}{
		{probe.ProbeShort, probe.InferResult{OK: true, AnswerOK: &answerOK, TTFTMs: 900, TotalMs: 1700}},
		{probe.ProbeWide, probe.InferResult{OK: true, TTFTMs: 2400, TotalMs: 9100}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			s, _ := newTestScheduler(t, &gradingProber{res: tc.res}, &fakePinger{})
			buf := captureLogs(t)

			s.runProbe(context.Background(), "mimo-v2.5", tc.kind, 7, time.Now())

			got := buf.String()
			for _, want := range []string{
				`"msg":"inference call"`, `"probe":"` + tc.kind + `"`,
				`"model":"mimo-v2.5"`, `"cycle":7`, `"ok":true`, `"level":"INFO"`,
			} {
				if !strings.Contains(got, want) {
					t.Errorf("log is missing %q; got %s", want, got)
				}
			}
		})
	}
}

// A failed run is the reason the line exists: it is a row in infer_probes with
// an error class and, until now, nothing at all in the container log. The level
// carries the verdict so WARN alone finds it.
func TestFailedInferenceCallLogsTheClassAtWarn(t *testing.T) {
	prober := &gradingProber{res: probe.InferResult{
		OK:          false,
		HTTPStatus:  401,
		ErrorClass:  probe.ErrClassAuth,
		ErrorDetail: `{"error":"invalid api key"}`,
	}}
	s, _ := newTestScheduler(t, prober, &fakePinger{})
	buf := captureLogs(t)

	s.runProbe(context.Background(), "mimo-v2.5", probe.ProbeShort, 3, time.Now())

	got := buf.String()
	for _, want := range []string{
		`"msg":"inference call"`, `"level":"WARN"`, `"ok":false`,
		`"http_status":401`, `"error_class":"` + probe.ErrClassAuth + `"`, "invalid api key",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("log is missing %q; got %s", want, got)
		}
	}
}

// ErrorDetail is raw upstream bytes, and a gateway that echoes request headers
// on a 4xx puts the live billable key in them. AGENTS.md says it may never reach
// a log line — so it is stripped, and stripped BEFORE the clip, since half a key
// is still key material.
func TestLoggedErrorDetailIsRedactedAndBounded(t *testing.T) {
	key := "tp-livebillablekey000000000000000"
	prober := &gradingProber{res: probe.InferResult{
		ErrorClass:  probe.ErrClassHTTP,
		ErrorDetail: "upstream rejected {authorization: Bearer " + key + "} " + strings.Repeat("x", 4000),
	}}
	s, _ := newTestScheduler(t, prober, &fakePinger{})
	buf := captureLogs(t)

	s.runProbe(context.Background(), "mimo-v2.5", probe.ProbeShort, 1, time.Now())

	got := buf.String()
	if strings.Contains(got, key) {
		t.Errorf("the api key reached the log: %s", got)
	}
	if !strings.Contains(got, "Bearer [redacted]") {
		t.Errorf("log is missing the redaction marker; got %s", got)
	}
	// A page of HTML served as an error body must not become one log line.
	if n := len(got); n > maxLoggedContent+512 {
		t.Errorf("log line is %d bytes, want the detail clipped near %d", n, maxLoggedContent)
	}
}

// A healthy line carries no empty error fields: a reader scanning for the
// failure should not have to step over two blank strings on every good run.
func TestHealthyInferenceCallOmitsTheErrorFields(t *testing.T) {
	s, _ := newTestScheduler(t, &gradingProber{res: probe.InferResult{OK: true}}, &fakePinger{})
	buf := captureLogs(t)

	s.runProbe(context.Background(), "mimo-v2.5", probe.ProbeShort, 1, time.Now())

	for _, unwanted := range []string{"error_class", "error_detail"} {
		if strings.Contains(buf.String(), unwanted) {
			t.Errorf("healthy line carries %q: %s", unwanted, buf.String())
		}
	}
}

// wideOrder is the models that went wide, in the order they were dispatched. The
// stagger is a statement about sequence, not just about counts.
func wideOrder(prober *fakeProber) []string {
	var out []string
	for _, r := range prober.requests() {
		if r.Probe == probe.ProbeWide {
			out = append(out, r.ModelID)
		}
	}
	return out
}

func TestRunCycleProbesEveryTargetAndModel(t *testing.T) {
	pinger := &fakePinger{}
	prober := &fakeProber{}
	s, db := newTestScheduler(t, prober, pinger)

	s.RunCycle(context.Background())

	// All four network targets, every cycle. The Singapore pair because the
	// subtraction and the fault verdict need them; the Amsterdam pair because a
	// gap in its series is indistinguishable from an outage in it.
	wantTargets := []string{
		probe.TargetMimoSGP, probe.TargetRefSGP,
		probe.TargetMimoAMS, probe.TargetRefAMS,
	}
	if len(pinger.calls) != len(wantTargets) {
		t.Errorf("pinged %d targets, want %d: %v",
			len(pinger.calls), len(wantTargets), pinger.calls)
	}
	for _, want := range wantTargets {
		if !slices.Contains(pinger.calls, want) {
			t.Errorf("target %q was never pinged: %v", want, pinger.calls)
		}
	}
	var nNet, nInfer int
	db.QueryRow(`SELECT count(*) FROM net_probes`).Scan(&nNet)
	db.QueryRow(`SELECT count(*) FROM infer_probes`).Scan(&nInfer)
	if nNet != len(wantTargets) {
		t.Errorf("net_probes = %d, want %d", nNet, len(wantTargets))
	}
	// The first cycle carries wide because no wide sample exists yet — a fresh
	// deploy should not show an empty prefill panel for an hour. One model at a
	// time, so 2 infer plus 1 wide rather than a wide run each.
	if nInfer != 3 {
		t.Errorf("infer_probes = %d, want 3 on a wide cycle", nInfer)
	}
}

// wide runs hourly PER MODEL, landing ON a cycle so it gets its own network
// reading and its TTFT is decomposable exactly like infer's — and one model at a
// time, so the fleet produces a wide run every WideInterval/N.
func TestWideRunsHourlyPerModelStaggeredAcrossThem(t *testing.T) {
	prober := &fakeProber{}
	db := openTestDB(t)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	s := newSchedulerOn(db, prober, &fakePinger{}, &now)

	// 13 cycles at the real cadence: one full hour plus one.
	for i := 0; i < 13; i++ {
		s.RunCycle(context.Background())
		now = now.Add(CycleInterval)
	}

	inferCount := 0
	for _, r := range prober.requests() {
		if r.Probe == probe.ProbeShort {
			inferCount++
		}
	}
	if inferCount != 13*2 {
		t.Errorf("infer runs = %d, want %d", inferCount, 13*2)
	}
	// Two models over 65 minutes: :00, :30 and :60, alternating. Each model is
	// still an hour apart from ITSELF, which is what keeps the sample rate and
	// the bill where they were.
	want := []string{"mimo-v2.5", "mimo-v2.5-pro", "mimo-v2.5"}
	if got := wideOrder(prober); !slices.Equal(got, want) {
		t.Errorf("wide order = %v, want %v", got, want)
	}
}

// No cycle may carry wide for more than one model. Two ~3800-token prefills
// dispatched together contend on the endpoint and the uplink, and each measures
// a share of the other's queueing — the same confound the within-model
// sequencing exists to avoid, which does not stop being one because the runs are
// aimed at different models.
func TestWideNeverRunsTwoModelsInOneCycle(t *testing.T) {
	prober := &fakeProber{}
	db := openTestDB(t)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	s := newSchedulerOn(db, prober, &fakePinger{}, &now)

	// A full day, so every slot boundary and both models' hours are crossed many
	// times over.
	for i := 0; i < 288; i++ {
		before := wideRuns(prober)
		s.RunCycle(context.Background())
		if got := wideRuns(prober) - before; got > 1 {
			t.Fatalf("cycle %d dispatched %d wide runs, want at most 1", i, got)
		}
		now = now.Add(CycleInterval)
	}

	// And the day buys the same number of wide runs it always did: one per model
	// per hour, so the stagger costs nothing and hides nothing.
	if got := wideRuns(prober); got != 48 {
		t.Errorf("wide runs in a day = %d, want 48", got)
	}
	for _, model := range []string{"mimo-v2.5", "mimo-v2.5-pro"} {
		n := 0
		for _, m := range wideOrder(prober) {
			if m == model {
				n++
			}
		}
		if n != 24 {
			t.Errorf("%s went wide %d times in a day, want 24", model, n)
		}
	}
}

// THE regression: the cadence used to come from an in-memory counter, so every
// process start replayed cycle zero and re-fired the hourly probe. A daemon
// restarted three times during a deploy sent three ~3800-token probes per
// model, and re-anchored the hour to the last restart.
func TestWideCadenceSurvivesARestart(t *testing.T) {
	db := openTestDB(t)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	first := &fakeProber{}
	newSchedulerOn(db, first, &fakePinger{}, &now).RunCycle(context.Background())
	if got := wideRuns(first); got != 1 {
		t.Fatalf("wide runs before restart = %d, want 1 (one model at a time)", got)
	}

	// Restart: a brand new scheduler, zeroed counters, same database.
	now = now.Add(CycleInterval)
	restarted := &fakeProber{}
	s := newSchedulerOn(db, restarted, &fakePinger{}, &now)
	s.RunCycle(context.Background())

	if got := wideRuns(restarted); got != 0 {
		t.Errorf("wide runs = %d five minutes after a restart, want 0", got)
	}

	// And the next slot still arrives on schedule. Half an hour after the first
	// wide run — not an hour — because the OTHER model is due, and its own hour
	// is not what the restart could have disturbed.
	now = now.Add(s.WideSlot() - CycleInterval)
	s.RunCycle(context.Background())
	if got := wideOrder(restarted); !slices.Equal(got, []string{"mimo-v2.5-pro"}) {
		t.Errorf("wide order = %v a slot later, want [mimo-v2.5-pro]", got)
	}
}

// Cycles are jittered, so the one nearest the hour is as likely to land just
// before it as just after. Without slack that cycle misses and "hourly" becomes
// every 65 minutes, drifting further each time.
func TestWideAcceptsACycleThatLandsJustShortOfTheHour(t *testing.T) {
	db := openTestDB(t)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	prober := &fakeProber{}
	s := newSchedulerOn(db, prober, &fakePinger{}, &now)

	// Both models first, a slot apart, so the model under test has a real
	// timestamp to be measured against rather than the "never ran" shortcut.
	s.RunCycle(context.Background())
	now = now.Add(s.WideSlot())
	s.RunCycle(context.Background())
	if got := wideRuns(prober); got != 2 {
		t.Fatalf("wide runs = %d after two slots, want 2", got)
	}

	// A minute shy of ITS hour: inside the slack, so it counts.
	now = now.Add(WideInterval - s.WideSlot() - time.Minute)
	s.RunCycle(context.Background())
	if got := wideRuns(prober); got != 3 {
		t.Errorf("wide runs = %d at 59 minutes, want 3 — the slack window must accept it", got)
	}

	// But the very next cycle must NOT: one full interval after a wide run is
	// always outside the window, which is what keeps the slack from double-firing.
	now = now.Add(CycleInterval)
	s.RunCycle(context.Background())
	if got := wideRuns(prober); got != 3 {
		t.Errorf("wide runs = %d one cycle later, want 3 — the slack must not double-fire", got)
	}
}

// A wide probe that was SENT counts against the hour even if nothing about it
// reached the database. Reads that keep succeeding while writes fail — a full
// disk, a read-only volume, a cycle abandoned mid-shutdown — would otherwise
// leave the cadence reading a stale timestamp and fire the expensive probe
// every five minutes, indefinitely.
func TestWideDoesNotRefireWhenTheDispatchWasNeverPersisted(t *testing.T) {
	db := openTestDB(t)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	prober := &fakeProber{}
	s := newSchedulerOn(db, prober, &fakePinger{}, &now)

	s.RunCycle(context.Background())
	if got := wideRuns(prober); got != 1 {
		t.Fatalf("wide runs = %d on the first cycle, want 1", got)
	}

	// Everything the cycle wrote is gone, exactly as if the write had failed.
	if _, err := db.Exec(`DELETE FROM infer_probes`); err != nil {
		t.Fatalf("clear infer_probes: %v", err)
	}

	for i := 0; i < 3; i++ {
		now = now.Add(CycleInterval)
		s.RunCycle(context.Background())
	}
	if got := wideRuns(prober); got != 1 {
		t.Errorf("wide runs = %d within the slot, want 1 — an unpersisted dispatch still counts", got)
	}

	// The hour still arrives on schedule, and the model whose dispatch vanished
	// is not owed a second one: the floor is the dispatch, not the row.
	now = now.Add(WideInterval)
	s.RunCycle(context.Background())
	if got := wideRuns(prober); got != 2 {
		t.Errorf("wide runs = %d an hour later, want 2", got)
	}
}

// The two probes must never be aggregated: the gap between their TTFTs IS the
// prefill signal, so wide has to carry the bigger cap and no question id.
func TestWideAndInferRequestsDifferAsTheMeasurementRequires(t *testing.T) {
	prober := &fakeProber{}
	s, _ := newTestScheduler(t, prober, &fakePinger{})

	s.RunCycle(context.Background()) // cycle 0 runs both

	var sawInfer, sawWide bool
	for _, r := range prober.requests() {
		switch r.Probe {
		case probe.ProbeShort:
			sawInfer = true
			if r.MaxTokens != probe.ShortMaxTokens {
				t.Errorf("infer cap = %d, want %d", r.MaxTokens, probe.ShortMaxTokens)
			}
			if r.QuestionID == "" {
				t.Error("infer must carry a question id; it is the correctness canary")
			}
			if r.Assert == nil {
				t.Error("infer must carry an assertion")
			}
		case probe.ProbeWide:
			sawWide = true
			if r.MaxTokens != probe.WideMaxTokens {
				t.Errorf("wide cap = %d, want %d", r.MaxTokens, probe.WideMaxTokens)
			}
			if len(r.Prompt) < 10000 {
				t.Errorf("wide prompt is only %d chars; the prefill gradient needs the document", len(r.Prompt))
			}
			if r.Assert != nil {
				t.Error("wide has no single assertable answer")
			}
		}
	}
	if !sawInfer || !sawWide {
		t.Fatal("cycle 0 must run both probe kinds")
	}
}

// The question rotates per cycle, so a per-question correctness rate is
// meaningful rather than dominated by one unlucky entry.
func TestQuestionRotatesBetweenCycles(t *testing.T) {
	prober := &fakeProber{}
	s, _ := newTestScheduler(t, prober, &fakePinger{})

	s.RunCycle(context.Background())
	s.RunCycle(context.Background())

	seen := map[string]bool{}
	for _, r := range prober.requests() {
		if r.Probe == probe.ProbeShort {
			seen[r.QuestionID] = true
		}
	}
	if len(seen) < 2 {
		t.Errorf("consecutive cycles asked the same question: %v", seen)
	}
}

// Both models must get the SAME question within a cycle, or their correctness
// series are not comparable.
func TestBothModelsGetTheSameQuestionInACycle(t *testing.T) {
	prober := &fakeProber{}
	s, _ := newTestScheduler(t, prober, &fakePinger{})

	s.RunCycle(context.Background())

	var ids []string
	for _, r := range prober.requests() {
		if r.Probe == probe.ProbeShort {
			ids = append(ids, r.QuestionID)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 infer runs, got %d", len(ids))
	}
	if ids[0] != ids[1] {
		t.Errorf("models got different questions (%q vs %q); their correctness is not comparable",
			ids[0], ids[1])
	}
}

// The overrun guard: if the previous run is still in flight when the next cycle
// fires, skip and count it. Two concurrent runs against one model would contend
// for the same upstream node and each would measure the other's queueing.
func TestOverrunSkipsAndCountsExactlyOnce(t *testing.T) {
	prober := &fakeProber{block: make(chan struct{}), blockModel: "mimo-v2.5"}
	s, db := newTestScheduler(t, prober, &fakePinger{})

	// A wide probe ran a moment ago, so no cycle here is due one. Without this
	// the first cycle carries wide for the blocked model too, and since the
	// prober blocks by MODEL the cycle waits on its own channel forever. The
	// overrun guard is what is under test; the wide cadence has its own tests.
	seedWideProbe(t, db, time.Now().UTC())

	// Start a cycle that blocks inside the prober.
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.RunCycle(context.Background())
	}()

	// Wait until the first probe is genuinely in flight.
	deadline := time.Now().Add(3 * time.Second)
	for prober.running.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("first probe never started")
		}
		time.Sleep(time.Millisecond)
	}

	// Fire a second cycle while the first is still running.
	s.RunCycle(context.Background())

	close(prober.block)
	<-done

	var skipped int
	if err := db.QueryRow(`SELECT count(*) FROM skipped_runs`).Scan(&skipped); err != nil {
		t.Fatalf("query: %v", err)
	}
	if skipped == 0 {
		t.Fatal("an overrun must be recorded; silent skipping makes availability lie by omission")
	}

	// And the skip must not have produced a phantom sample.
	var inferRows int
	db.QueryRow(`SELECT count(*) FROM infer_probes`).Scan(&inferRows)
	if inferRows == 0 {
		t.Error("the completed cycle should still have written its own rows")
	}
}

// A slow model must not suppress the other's samples — a global lock would
// manufacture a correlated outage across two independent series.
func TestOverrunGuardIsPerModelAndProbe(t *testing.T) {
	s, _ := newTestScheduler(t, &fakeProber{}, &fakePinger{})

	if !s.acquire("mimo-v2.5/short") {
		t.Fatal("first acquire must succeed")
	}
	if s.acquire("mimo-v2.5/short") {
		t.Error("the same key must not be acquirable twice")
	}
	// A different model, and a different probe on the same model, must both be
	// unaffected.
	if !s.acquire("mimo-v2.5-pro/short") {
		t.Error("a different model must not be blocked by another model's run")
	}
	if !s.acquire("mimo-v2.5/wide") {
		t.Error("a different probe kind must not be blocked by short")
	}

	s.release("mimo-v2.5/short")
	if !s.acquire("mimo-v2.5/short") {
		t.Error("release must free the key")
	}
}

// A failed probe is still a recorded sample, and the cycle must still persist.
func TestCycleWithAFailedProbeIsStillPersisted(t *testing.T) {
	pinger := &fakePinger{results: map[string]bool{probe.TargetMimoSGP: false}}
	s, db := newTestScheduler(t, &fakeProber{}, pinger)

	s.RunCycle(context.Background())

	var fault string
	if err := db.QueryRow(`SELECT fault FROM cycle_fault`).Scan(&fault); err != nil {
		t.Fatalf("query: %v", err)
	}
	// MiMo unreachable while both references answer: the edge, not the route.
	if fault != probe.FaultEdge {
		t.Errorf("fault = %q, want %q", fault, probe.FaultEdge)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM cycles`).Scan(&n)
	if n != 1 {
		t.Errorf("cycles = %d; a network failure must still produce a cycle", n)
	}
}

// Shutdown mid-cycle must not persist a half-measured cycle, whose missing
// models would read as an outage.
func TestCycleAbandonedOnShutdownIsNotPersisted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s, db := newTestScheduler(t, &fakeProber{}, &fakePinger{})

	cancel()
	s.RunCycle(ctx)

	var n int
	db.QueryRow(`SELECT count(*) FROM cycles`).Scan(&n)
	if n != 0 {
		t.Errorf("cycles = %d, want 0; a cycle abandoned during shutdown must not be stored", n)
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	s, _ := newTestScheduler(t, &fakeProber{}, &fakePinger{})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after its context was cancelled")
	}
}

// The cadence must be anchored to the wall clock, not to when the last cycle
// happened to finish. Sleeping a fixed interval AFTER each cycle adds that
// cycle's duration to every gap — and the error compounds in the worst
// direction, sampling LESS often exactly when the endpoint is slow.
func TestNextDelayIsAnchoredToTheWallClockNotToCycleDuration(t *testing.T) {
	s, _ := newTestScheduler(t, &fakeProber{}, &fakePinger{})
	s.deps.Rand = func() float64 { return 0.5 } // zero jitter

	// A cycle that finished 22 seconds past an aligned tick must wait for the
	// NEXT tick, not a full interval from now.
	finishedAt := time.Date(2026, 8, 4, 6, 0, 22, 0, time.UTC)
	s.deps.Now = func() time.Time { return finishedAt }

	d, missed := s.nextDelay()
	if len(missed) != 0 {
		t.Errorf("missed = %v, want none: this cycle did not overrun its slot", missed)
	}

	want := CycleInterval - 22*time.Second
	if d != want {
		t.Errorf("delay = %v, want %v (the next aligned tick, not a full interval)", d, want)
	}
	if fired := finishedAt.Add(d); fired.Second() != 0 || fired.Minute()%5 != 0 {
		t.Errorf("cycle would fire at %v, which is not an aligned tick", fired)
	}
}

// A cycle that overruns its slot lands on the following one; the cadence must
// not walk forward by the overrun.
func TestOverrunningCycleLandsOnTheNextSlotWithoutDrifting(t *testing.T) {
	s, _ := newTestScheduler(t, &fakeProber{}, &fakePinger{})
	s.deps.Rand = func() float64 { return 0.5 }

	// One quick cycle first, to establish the anchor. Before that there is no
	// previous slot to have overrun — the slots ahead of process start were not
	// missed, they simply predate the daemon.
	now := time.Date(2026, 8, 4, 6, 0, 0, 0, time.UTC)
	s.deps.Now = func() time.Time { return now }
	if _, missed := s.nextDelay(); len(missed) != 0 {
		t.Fatalf("missed = %v on the very first delay, want none", missed)
	}

	// The 06:05 cycle then runs 6m30s, straight through the 06:10 slot.
	now = time.Date(2026, 8, 4, 6, 11, 30, 0, time.UTC)
	d, missed := s.nextDelay()
	fired := now.Add(d)

	// The eaten slot must be REPORTED, not merely absorbed. See
	// recordMissedTicks: absorbing it silently is what made the overrun counter
	// read zero while the series thinned out.
	if len(missed) != 1 {
		t.Fatalf("missed = %v, want exactly the 06:10 slot the cycle ran through", missed)
	}
	if want := time.Date(2026, 8, 4, 6, 10, 0, 0, time.UTC); !missed[0].Equal(want) {
		t.Errorf("missed slot = %v, want %v", missed[0], want)
	}

	if want := time.Date(2026, 8, 4, 6, 15, 0, 0, time.UTC); !fired.Equal(want) {
		t.Errorf("next cycle at %v, want %v — the schedule must not walk", fired, want)
	}
}

// A cycle that ran through several slots must report every one of them, or the
// count understates how much of the incident went unsampled.
func TestNextDelayReportsEverySlotAnOverrunAteNotJustTheLastOne(t *testing.T) {
	s, _ := newTestScheduler(t, &fakeProber{}, &fakePinger{})
	s.deps.Rand = func() float64 { return 0.5 }

	now := time.Date(2026, 8, 4, 6, 0, 0, 0, time.UTC)
	s.deps.Now = func() time.Time { return now }
	s.nextDelay() // anchor on 06:05

	// The 06:05 cycle takes 16 minutes — two models at the 240 s ceiling plus a
	// wide probe each. It runs through 06:10, 06:15 and 06:20.
	now = time.Date(2026, 8, 4, 6, 21, 0, 0, time.UTC)
	_, missed := s.nextDelay()

	want := []time.Time{
		time.Date(2026, 8, 4, 6, 10, 0, 0, time.UTC),
		time.Date(2026, 8, 4, 6, 15, 0, 0, time.UTC),
		time.Date(2026, 8, 4, 6, 20, 0, 0, time.UTC),
	}
	if len(missed) != len(want) {
		t.Fatalf("missed = %v, want %v", missed, want)
	}
	for i := range want {
		if !missed[i].Equal(want[i]) {
			t.Errorf("missed[%d] = %v, want %v", i, missed[i], want[i])
		}
	}
}

// The overrun counter beside the availability strip used to be structurally
// incapable of being non-zero: its only writer was the in-flight guard, which
// cannot fire while cycles run one at a time. A dropped slot has to reach it.
func TestMissedTicksAreRecordedPerModel(t *testing.T) {
	s, db := newTestScheduler(t, &fakeProber{}, &fakePinger{})

	ticks := []time.Time{
		time.Date(2026, 8, 4, 6, 10, 0, 0, time.UTC),
		time.Date(2026, 8, 4, 6, 15, 0, 0, time.UTC),
	}
	s.recordMissedTicks(context.Background(), ticks)

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM skipped_runs`).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	// Two slots, two models, one infer run each.
	if want := len(ticks) * 2; n != want {
		t.Errorf("skipped_runs = %d, want %d (one per model per dropped slot)", n, want)
	}

	// `wide` is conditional, so a dropped slot must not claim to have lost one.
	var wide int
	db.QueryRow(`SELECT count(*) FROM skipped_runs WHERE probe = ?`, probe.ProbeWide).Scan(&wide)
	if wide != 0 {
		t.Errorf("skipped_runs carrying wide = %d, want 0", wide)
	}
}

// Sequentially, a cycle costs the SUM of the models' latencies, so one model
// stalling spends the other's budget and the whole cadence breaks at roughly
// CycleInterval/len(models) — about 145 s per model with two of them. That is
// well inside what mimo-v2.5-pro does on a bad day.
func TestASlowModelDoesNotHoldUpTheOtherModelsProbe(t *testing.T) {
	prober := &fakeProber{block: make(chan struct{}), blockModel: "mimo-v2.5"}
	s, db := newTestScheduler(t, prober, &fakePinger{})
	seedWideProbe(t, db, time.Now().UTC()) // keep this cycle to one probe per model

	done := make(chan struct{})
	go func() { defer close(done); s.RunCycle(context.Background()) }()

	// The unblocked model must get all the way through while the other is still
	// stuck inside its probe.
	deadline := time.Now().Add(3 * time.Second)
	for {
		got := 0
		for _, r := range prober.requests() {
			if r.ModelID == "mimo-v2.5-pro" {
				got++
			}
		}
		if got > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the second model never ran while the first was blocked; the models are serialised")
		}
		time.Sleep(time.Millisecond)
	}

	close(prober.block)
	<-done
}

// A three-hour gap is not a cycle that ran long — no cycle can, the probe
// ladder bounds it at minutes. It is the wall clock moving: an NTP step, a
// suspended host, a restored snapshot. Claiming every slot in it would put
// dozens of "skipped" against a tooltip saying the previous cycle was still
// running, which is the deploy-gap misread this counter exists to prevent.
func TestAClockJumpIsNotClaimedAsAnOverrun(t *testing.T) {
	s, db := newTestScheduler(t, &fakeProber{}, &fakePinger{})

	var ticks []time.Time
	base := time.Date(2026, 8, 4, 6, 10, 0, 0, time.UTC)
	for i := 0; i < 36; i++ { // three hours of five-minute slots
		ticks = append(ticks, base.Add(time.Duration(i)*CycleInterval))
	}
	s.recordMissedTicks(context.Background(), ticks)

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM skipped_runs`).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if want := MaxRecordedMisses * 2; n != want {
		t.Errorf("skipped_runs = %d, want %d — a clock jump must not be claimed slot by slot", n, want)
	}

	// The ones kept are the most recent: those are the slots adjacent to the
	// cycle that actually ran, and the only ones a reader could act on.
	var first string
	if err := db.QueryRow(`SELECT min(occurred_at) FROM skipped_runs`).Scan(&first); err != nil {
		t.Fatalf("query: %v", err)
	}
	want := ticks[len(ticks)-MaxRecordedMisses].Format(time.RFC3339Nano)
	if first != want {
		t.Errorf("earliest recorded tick = %s, want %s", first, want)
	}
}

// Negative jitter must not fire the cycle in the past or immediately.
func TestNextDelayNeverFiresImmediately(t *testing.T) {
	s, _ := newTestScheduler(t, &fakeProber{}, &fakePinger{})

	// Finish just before an aligned tick, with maximum negative jitter.
	s.deps.Rand = func() float64 { return 0 }
	s.deps.Now = func() time.Time { return time.Date(2026, 8, 4, 6, 4, 59, 0, time.UTC) }

	if d, _ := s.nextDelay(); d < MinInterval {
		t.Errorf("delay = %v, below the %v floor; this could become a tight loop", d, MinInterval)
	}
}

// Over many cycles the mean must sit on the interval, or the day's cycle count
// and therefore the token bill are wrong.
func TestCadenceDoesNotDriftOverManyCycles(t *testing.T) {
	s, _ := newTestScheduler(t, &fakeProber{}, &fakePinger{})

	now := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	s.deps.Now = func() time.Time { return now }

	const cycles = 200
	const cycleDuration = 22 * time.Second // a realistic wide cycle
	start := now
	for i := 0; i < cycles; i++ {
		now = now.Add(cycleDuration) // the cycle runs
		d, _ := s.nextDelay()
		now = now.Add(d) // then we wait
	}

	elapsed := now.Sub(start)
	mean := elapsed / cycles
	drift := mean - CycleInterval
	if drift < -2*time.Second || drift > 2*time.Second {
		t.Errorf("mean interval %v drifts %v from %v over %d cycles; that is %.0f cycles/day instead of 288",
			mean, drift, CycleInterval, cycles, 86400/mean.Seconds())
	}
}
