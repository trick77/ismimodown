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
		ModelID: model, Probe: probe.ProbeShort,
		TTFTMs: ttft, TTFATMs: ttft, TotalMs: ttft + 800,
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
			ModelID: "mimo-v2.5", Probe: probe.ProbeShort,
			TotalMs: 240000, OK: false, ErrorClass: probe.ErrClassTimeout,
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
			ModelID: "mimo-v2.5", Probe: probe.ProbeShort,
			TotalMs: 240000, OK: false, ErrorClass: probe.ErrClassTimeout,
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

// A partially-written cycle would produce inference rows whose server-side time
// has no network reading to subtract — an unfounded number rather than a
// visibly missing one.
func TestSaveIsAtomic(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	// An invalid probe kind trips the CHECK constraint partway through.
	_, err := s.Save(context.Background(), Cycle{
		StartedAt: time.Now(), Net: okNet(),
		Infer: []probe.InferResult{
			okInfer("mimo-v2.5", 900),
			{ModelID: "mimo-v2.5-pro", Probe: "not-a-probe-kind", OK: true},
		},
	})
	if err == nil {
		t.Fatal("expected Save to fail on an invalid probe kind")
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

func TestRecordSkip(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.RecordSkip(context.Background(), time.Now(), "mimo-v2.5-pro", probe.ProbeShort); err != nil {
		t.Fatalf("RecordSkip: %v", err)
	}

	var n int
	var model string
	db.QueryRow(`SELECT count(*) FROM skipped_runs`).Scan(&n)
	db.QueryRow(`SELECT model_id FROM skipped_runs`).Scan(&model)
	if n != 1 || model != "mimo-v2.5-pro" {
		t.Errorf("skipped_runs = %d rows, model %q", n, model)
	}
}

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
	if err := s.RecordSkip(ctx, now.Add(-100*24*time.Hour), "m", probe.ProbeShort); err != nil {
		t.Fatalf("RecordSkip: %v", err)
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
	// skipped_runs hangs off no cycle and needs its own sweep.
	db.QueryRow(`SELECT count(*) FROM skipped_runs`).Scan(&n)
	if n != 0 {
		t.Error("skipped_runs grew without bound; it is not covered by the cascade")
	}
}
