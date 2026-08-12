package samples

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/trick77/ismimodown/internal/probe"
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

func okNet() []probe.NetResult {
	return []probe.NetResult{
		{Target: probe.TargetMimoSGP, DNSMs: 3, ConnectMs: 166, OK: true},
		{Target: probe.TargetRefSGP, DNSMs: 4, ConnectMs: 265, OK: true},
	}
}

func okInfer(model string, ttft float64) probe.InferResult {
	yes := true
	return probe.InferResult{
		ModelID: model, TTFTMs: ttft, TTFATMs: ttft, TotalMs: ttft + 800,
		ITLP50Ms: 24, ITLP95Ms: 30, OutputTPS: 41,
		Usage: probe.TokenUsage{
			PromptTokens: 34, CompletionTokens: 59,
		},
		QuestionID: "capital-france", OK: true, AnswerOK: &yes,
	}
}

func TestSaveWritesTheWholeCycle(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	id, err := s.Save(context.Background(), Cycle{
		StartedAt: time.Date(2026, 8, 4, 6, 0, 0, 0, time.UTC),
		Net:       okNet(),
		Infer:     []probe.InferResult{okInfer("mimo-v2.5", 912)},
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if id == 0 {
		t.Fatal("Save returned cycle id 0")
	}

	var nNet, nInfer, nFault int
	db.QueryRow(`SELECT count(*) FROM net_probes WHERE cycle_id = ?`, id).Scan(&nNet)
	db.QueryRow(`SELECT count(*) FROM infer_probes WHERE cycle_id = ?`, id).Scan(&nInfer)
	db.QueryRow(`SELECT count(*) FROM cycle_fault WHERE cycle_id = ?`, id).Scan(&nFault)

	if nNet != 2 {
		t.Errorf("net_probes = %d, want 2", nNet)
	}
	if nInfer != 1 {
		t.Errorf("infer_probes = %d, want 1", nInfer)
	}
	if nFault != 1 {
		t.Errorf("cycle_fault = %d, want 1", nFault)
	}
}

// The alignment invariant: every infer row must have net rows in the SAME
// cycle, so the subtraction can never silently fall back to a
// nearest-neighbour guess.
func TestEveryInferRowHasNetworkReadingsInItsOwnCycle(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	for i := 0; i < 3; i++ {
		if _, err := s.Save(context.Background(), Cycle{
			StartedAt: time.Now().Add(time.Duration(i) * time.Minute),
			Net:       okNet(),
			Infer: []probe.InferResult{
				okInfer("mimo-v2.5", 900), okInfer("mimo-v2.5-pro", 1100),
			},
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	var orphans int
	err := db.QueryRow(`
		SELECT count(*) FROM infer_probes i
		WHERE NOT EXISTS (SELECT 1 FROM net_probes n WHERE n.cycle_id = i.cycle_id)
	`).Scan(&orphans)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if orphans != 0 {
		t.Errorf("%d infer rows have no network reading in their cycle", orphans)
	}
}

// A cycle with no network readings is not a degraded sample — it is a different
// measurement, and storing it would produce server-side times with nothing to
// subtract.
func TestSaveRejectsACycleWithNoNetworkReadings(t *testing.T) {
	s := New(openTestDB(t))

	_, err := s.Save(context.Background(), Cycle{
		StartedAt: time.Now(),
		Infer:     []probe.InferResult{okInfer("mimo-v2.5", 900)},
	})
	if err == nil {
		t.Fatal("expected Save to reject a cycle with no network readings")
	}
}

// The rule that makes an outage read as an outage rather than as catastrophic
// latency: a failed run stores NULL timings, never 0, so no percentile or
// average can ever absorb it as "instant".
func TestFailedRunStoresNullTimingsNotZeros(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	id, err := s.Save(context.Background(), Cycle{
		StartedAt: time.Now(), Net: okNet(),
		Infer: []probe.InferResult{{
			ModelID: "mimo-v2.5", TotalMs: 240000, OK: false, ErrorClass: probe.ErrClassTimeout,
			ErrorDetail: "context deadline exceeded",
		}},
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	var ttft, itl sql.NullFloat64
	var total sql.NullFloat64
	var okCol int
	var class sql.NullString
	err = db.QueryRow(
		`SELECT ttft_ms, itl_p50_ms, total_ms, ok, error_class FROM infer_probes WHERE cycle_id = ?`, id,
	).Scan(&ttft, &itl, &total, &okCol, &class)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if ttft.Valid {
		t.Errorf("ttft_ms = %v, must be NULL on a failed run", ttft.Float64)
	}
	if itl.Valid {
		t.Errorf("itl_p50_ms = %v, must be NULL on a failed run", itl.Float64)
	}
	// total_ms is the deliberate exception: how far it got is what separates an
	// instant refusal from a 240-second timeout.
	if !total.Valid || total.Float64 != 240000 {
		t.Errorf("total_ms = %v, want 240000 recorded even on failure", total)
	}
	if okCol != 0 {
		t.Errorf("ok = %d, want 0", okCol)
	}
	if class.String != probe.ErrClassTimeout {
		t.Errorf("error_class = %q", class.String)
	}
}

// The percentile-exclusion test the plan names: one 240s timeout among nine
// 900ms successes must leave P50 near 900ms and availability at 90% — never a
// P50 dragged toward the timeout.
func TestTimeoutDoesNotPoisonThePercentile(t *testing.T) {
	db := openTestDB(t)
	s := New(db)
	ctx := context.Background()

	for i := 0; i < 9; i++ {
		if _, err := s.Save(ctx, Cycle{
			StartedAt: time.Now().Add(time.Duration(i) * time.Minute),
			Net:       okNet(),
			Infer:     []probe.InferResult{okInfer("mimo-v2.5", 900)},
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	if _, err := s.Save(ctx, Cycle{
		StartedAt: time.Now().Add(10 * time.Minute), Net: okNet(),
		Infer: []probe.InferResult{{
			ModelID: "mimo-v2.5", TotalMs: 240000, OK: false, ErrorClass: probe.ErrClassTimeout,
		}},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The median over SUCCESSFUL rows. NULL timings are ignored by SQL
	// aggregates, which is exactly why they are stored as NULL.
	var median sql.NullFloat64
	if err := db.QueryRow(`
		SELECT avg(ttft_ms) FROM (
			SELECT ttft_ms FROM infer_probes
			WHERE ok = 1 AND ttft_ms IS NOT NULL
			ORDER BY ttft_ms
			LIMIT 2 - (SELECT count(*) FROM infer_probes WHERE ok = 1 AND ttft_ms IS NOT NULL) % 2
			OFFSET (SELECT (count(*) - 1) / 2 FROM infer_probes WHERE ok = 1 AND ttft_ms IS NOT NULL)
		)
	`).Scan(&median); err != nil {
		t.Fatalf("median query: %v", err)
	}
	if !median.Valid || median.Float64 != 900 {
		t.Errorf("P50 = %v, want 900 — the timeout must not drag it", median)
	}

	// Availability counts every row, successes and failures alike.
	var total, okCount int
	db.QueryRow(`SELECT count(*) FROM infer_probes`).Scan(&total)
	db.QueryRow(`SELECT count(*) FROM infer_probes WHERE ok = 1`).Scan(&okCount)
	if total != 10 || okCount != 9 {
		t.Fatalf("rows = %d, ok = %d, want 10 and 9", total, okCount)
	}
	if avail := 100 * float64(okCount) / float64(total); avail != 90 {
		t.Errorf("availability = %.1f%%, want 90%%", avail)
	}
}

// Fault attribution is stored, not recomputed, so the strip and the arithmetic
// cannot drift apart. One case per row of the table.
func TestCycleFaultIsStoredFromTheNetworkReadings(t *testing.T) {
	cases := []struct {
		name         string
		mimo, refSGP bool
		want         string
	}{
		{"all up", true, true, probe.FaultOK},
		{"mimo edge down", false, true, probe.FaultEdge},
		// Unattributable with one reference: excluded rather than blamed on MiMo.
		{"nothing reachable", false, false, probe.FaultUplink},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			s := New(db)

			id, err := s.Save(context.Background(), Cycle{
				StartedAt: time.Now(),
				Net: []probe.NetResult{
					{Target: probe.TargetMimoSGP, OK: tc.mimo},
					{Target: probe.TargetRefSGP, OK: tc.refSGP},
				},
			})
			if err != nil {
				t.Fatalf("Save: %v", err)
			}

			var fault string
			if err := db.QueryRow(
				`SELECT fault FROM cycle_fault WHERE cycle_id = ?`, id).Scan(&fault); err != nil {
				t.Fatalf("query: %v", err)
			}
			if fault != tc.want {
				t.Errorf("fault = %q, want %q", fault, tc.want)
			}
		})
	}
}

// The Amsterdam readings are stored and charted, and reach no verdict.
//
// Asserted on the STORED fault rather than by calling AttributeFault: that
// function is exhaustive over its two booleans, so no call to it can distinguish
// a correct four-target wiring from a wrong one. Only the path from Save's
// switch to cycle_fault can.
func TestAmsterdamReadingsNeverChangeTheFault(t *testing.T) {
	cases := []struct {
		name                             string
		mimoSGP, refSGP, mimoAMS, refAMS bool
		want                             string
	}{
		// Amsterdam collapsing entirely while Singapore is fine is not a MiMo
		// fault: nothing infers against Amsterdam, and the page's verdict is
		// about the endpoint the probes actually use.
		{"amsterdam down, singapore fine", true, true, false, false, probe.FaultOK},
		// The inverse, and the one that breaks first if someone folds the
		// Amsterdam edge into attribution: a healthy Amsterdam must not soften
		// a Singapore edge failure into something milder.
		{"singapore edge down, amsterdam fine", false, true, true, true, probe.FaultEdge},
		// Nor may a healthy Amsterdam prove the uplink alive and turn this into
		// a route fault. That distinction is genuinely restorable from these
		// readings and is deliberately NOT taken — see probe.AttributeFault.
		{"singapore unreachable, amsterdam fine", false, false, true, true, probe.FaultUplink},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			s := New(db)

			id, err := s.Save(context.Background(), Cycle{
				StartedAt: time.Now(),
				Net: []probe.NetResult{
					{Target: probe.TargetMimoSGP, OK: tc.mimoSGP},
					{Target: probe.TargetRefSGP, OK: tc.refSGP},
					{Target: probe.TargetMimoAMS, OK: tc.mimoAMS},
					{Target: probe.TargetRefAMS, OK: tc.refAMS},
				},
			})
			if err != nil {
				t.Fatalf("Save: %v", err)
			}

			var fault string
			if err := db.QueryRow(
				`SELECT fault FROM cycle_fault WHERE cycle_id = ?`, id).Scan(&fault); err != nil {
				t.Fatalf("query: %v", err)
			}
			if fault != tc.want {
				t.Errorf("fault = %q, want %q", fault, tc.want)
			}

			// Stored all the same. Display-only means unattributed, not undropped.
			var n int
			if err := db.QueryRow(
				`SELECT count(*) FROM net_probes WHERE cycle_id = ? AND target IN (?, ?)`,
				id, probe.TargetMimoAMS, probe.TargetRefAMS).Scan(&n); err != nil {
				t.Fatalf("query: %v", err)
			}
			if n != 2 {
				t.Errorf("amsterdam net_probes = %d, want 2", n)
			}
		})
	}
}

// A cycle without the Singapore pair is REFUSED, not attributed.
//
// len(Net) != 0 was a sufficient guard while Singapore was the only region.
// With Amsterdam added it is not: a cycle carrying only the Amsterdam rows
// passes it, both attribution flags stay false, and the cycle is recorded as an
// uplink outage that was never measured. The scheduler always sends all four, so
// this is the guard that makes correctness a property of Save rather than of one
// caller.
func TestSaveRefusesACycleMissingTheSingaporePair(t *testing.T) {
	cases := []struct {
		name string
		net  []probe.NetResult
	}{
		{"amsterdam only", []probe.NetResult{
			{Target: probe.TargetMimoAMS, OK: true},
			{Target: probe.TargetRefAMS, OK: true},
		}},
		{"missing the singapore reference", []probe.NetResult{
			{Target: probe.TargetMimoSGP, OK: true},
			{Target: probe.TargetMimoAMS, OK: true},
		}},
		{"missing the singapore edge", []probe.NetResult{
			{Target: probe.TargetRefSGP, OK: true},
			{Target: probe.TargetRefAMS, OK: true},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			s := New(db)

			if _, err := s.Save(context.Background(), Cycle{
				StartedAt: time.Now(), Net: tc.net,
			}); err == nil {
				t.Fatal("Save accepted a cycle it cannot attribute")
			}

			// Refused means nothing landed, not "landed without a verdict".
			var n int
			if err := db.QueryRow(`SELECT count(*) FROM cycles`).Scan(&n); err != nil {
				t.Fatalf("query: %v", err)
			}
			if n != 0 {
				t.Errorf("cycles = %d after a refused Save; the rollback did not hold", n)
			}
		})
	}
}

// A partially-written cycle would produce inference rows whose server-side time
// has no network reading to subtract — an unfounded number rather than a
// visibly missing one.
func TestSaveIsAtomic(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	// An invalid ping target trips the CHECK constraint partway through.
	//
	// This used to trip it on an invalid PROBE KIND, which failed inside the
	// infer loop — after the cycle row, the net rows and the fault row were
	// written — and so proved the rollback over the whole transaction. That
	// column is gone with the wide probe (migration 0006), and nothing reaching
	// insertInfer can fail any more: every remaining constraint on the table is
	// satisfied by construction from Go's own types. So the failure is forced
	// one step earlier, which still proves a refused Save leaves nothing behind
	// but no longer exercises a partially-filled infer loop. If a constraint is
	// ever added to infer_probes that a caller can violate, move this back.
	bad := okNet()
	bad = append(bad, probe.NetResult{Target: "not-a-target", OK: true})
	_, err := s.Save(context.Background(), Cycle{
		StartedAt: time.Now(), Net: bad,
		Infer: []probe.InferResult{
			okInfer("mimo-v2.5", 900),
			okInfer("mimo-v2.5-pro", 900),
		},
	})
	if err == nil {
		t.Fatal("expected Save to fail on an invalid ping target")
	}

	for _, table := range []string{"cycles", "net_probes", "infer_probes", "cycle_fault"} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%s has %d rows after a failed Save; the write was not atomic", table, n)
		}
	}
}

// TestRecordSkip is gone with RecordSkip and the skipped_runs table it wrote to
// (migration 0005). The overrun it recorded is now only logged; the scheduler
// tests cover that it still detects one.

// Retention: a sample older than the window goes, one inside it survives, and
// the children go with their cycle rather than accumulating as orphans that
// percentile queries would still find.
func TestSweepDeletesOnlyBeyondTheWindow(t *testing.T) {
	db := openTestDB(t)
	s := New(db)
	ctx := context.Background()
	now := time.Now()

	oldID, err := s.Save(ctx, Cycle{
		StartedAt: now.Add(-100 * 24 * time.Hour),
		Net:       okNet(), Infer: []probe.InferResult{okInfer("mimo-v2.5", 900)},
	})
	if err != nil {
		t.Fatalf("Save old: %v", err)
	}
	newID, err := s.Save(ctx, Cycle{
		StartedAt: now.Add(-1 * time.Hour),
		Net:       okNet(), Infer: []probe.InferResult{okInfer("mimo-v2.5", 900)},
	})
	if err != nil {
		t.Fatalf("Save new: %v", err)
	}
	// 3-month window.
	deleted, err := s.Sweep(ctx, now.Add(-2160*time.Hour))
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted %d cycles, want 1", deleted)
	}

	var n int
	db.QueryRow(`SELECT count(*) FROM cycles WHERE id = ?`, oldID).Scan(&n)
	if n != 0 {
		t.Error("the old cycle survived the sweep")
	}
	db.QueryRow(`SELECT count(*) FROM cycles WHERE id = ?`, newID).Scan(&n)
	if n != 1 {
		t.Error("the recent cycle was swept; it is inside the window")
	}
	// Children must cascade, or they linger and keep being counted.
	db.QueryRow(`SELECT count(*) FROM infer_probes WHERE cycle_id = ?`, oldID).Scan(&n)
	if n != 0 {
		t.Error("infer_probes outlived their swept cycle")
	}
	// skipped_runs used to be checked here too: it hung off no cycle, so the
	// cascade missed it and Sweep deleted it separately. The table is gone
	// (migration 0005) and the cascade now covers everything that remains.
}
