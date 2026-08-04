// Package samples persists one probe cycle and everything measured in it.
//
// The whole cycle is written in ONE transaction. That is not incidental: the
// network-vs-inference subtraction is a join on cycle_id, and a partially
// written cycle would produce inference rows whose "server-side time" has no
// network reading to subtract — a silently unfounded number rather than a
// visibly missing one.
package samples

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/trick77/mimostats/internal/probe"
)

// Store writes and reads probe samples.
type Store struct {
	db *sql.DB
}

// New builds a Store.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Cycle is one complete aligned measurement.
type Cycle struct {
	StartedAt time.Time
	Net       []probe.NetResult
	Infer     []probe.InferResult
}

// Save writes a whole cycle atomically and returns the new cycle id.
//
// A cycle with no network readings is REJECTED rather than stored. Every
// published figure depends on the subtraction, and a cycle that cannot support
// it is not a degraded sample — it is a different measurement wearing the same
// name.
func (s *Store) Save(ctx context.Context, c Cycle) (int64, error) {
	if len(c.Net) == 0 {
		return 0, fmt.Errorf("cycle has no network readings; the subtraction would be unfounded")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO cycles (started_at) VALUES (?)`,
		c.StartedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("insert cycle: %w", err)
	}
	cycleID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	var mimoOK, refSGPOK, refEUOK bool
	for _, n := range c.Net {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO net_probes (cycle_id, target, dns_ms, connect_ms, ok, error_class)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			cycleID, n.Target, nullFloat(n.DNSMs, n.OK), nullFloat(n.ConnectMs, n.OK),
			boolToInt(n.OK), nullString(n.ErrorClass),
		); err != nil {
			return 0, fmt.Errorf("insert net_probe %s: %w", n.Target, err)
		}
		switch n.Target {
		case probe.TargetMimoSGP:
			mimoOK = n.OK
		case probe.TargetRefSGP:
			refSGPOK = n.OK
		case probe.TargetRefEU:
			refEUOK = n.OK
		}
	}

	// Stored rather than recomputed per query, so the availability strip and the
	// availability arithmetic can never disagree and the rule is testable in
	// exactly one place.
	fault := probe.AttributeFault(mimoOK, refSGPOK, refEUOK)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO cycle_fault (cycle_id, fault) VALUES (?, ?)`, cycleID, fault,
	); err != nil {
		return 0, fmt.Errorf("insert cycle_fault: %w", err)
	}

	for _, in := range c.Infer {
		if err := insertInfer(ctx, tx, cycleID, in); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return cycleID, nil
}

func insertInfer(ctx context.Context, tx *sql.Tx, cycleID int64, in probe.InferResult) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO infer_probes (
			cycle_id, model_id, probe,
			ttft_ms, ttfat_ms, total_ms, itl_p50_ms, itl_p95_ms, output_tps,
			prompt_tokens, output_tokens, cached_tokens, reasoning_tokens,
			question_id, ok, answer_ok, http_status, error_class, error_detail
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cycleID, in.ModelID, in.Probe,
		// Timings are NULL on a failed run, never 0. A zero would be read as
		// "instant" by every percentile and average downstream; NULL is
		// correctly ignored. This is the storage half of the rule that failed
		// rows stay out of latency percentiles and count only in availability.
		nullFloat(in.TTFTMs, in.OK),
		nullFloat(in.TTFATMs, in.OK),
		// total_ms is the exception: it is recorded even on failure, because
		// "how far did it get" is exactly what distinguishes an instant refusal
		// from a 240-second timeout.
		nullFloatAlways(in.TotalMs),
		nullFloat(in.ITLP50Ms, in.OK),
		nullFloat(in.ITLP95Ms, in.OK),
		nullFloat(in.OutputTPS, in.OK),
		nullInt(in.Usage.PromptTokens, in.OK),
		nullInt(in.Usage.CompletionTokens, in.OK),
		nullInt(in.Usage.PromptTokensDetails.CachedTokens, in.OK),
		nullInt(in.Usage.CompletionTokenDetails.ReasoningTokens, in.OK),
		nullString(in.QuestionID),
		boolToInt(in.OK),
		nullBool(in.AnswerOK),
		nullInt(in.HTTPStatus, in.HTTPStatus != 0),
		nullString(in.ErrorClass),
		// OPERATOR-ONLY. Never served by any public endpoint; a test asserts it.
		nullString(in.ErrorDetail),
	)
	if err != nil {
		return fmt.Errorf("insert infer_probe %s/%s: %w", in.ModelID, in.Probe, err)
	}
	return nil
}

// RecordSkip increments the overrun counter.
//
// Surfaced rather than swallowed: a silently skipped run makes the availability
// strip lie by omission — the cycle simply is not there, which reads as "no
// data" rather than "we were still busy".
func (s *Store) RecordSkip(ctx context.Context, at time.Time, modelID, probeKind string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO skipped_runs (occurred_at, model_id, probe) VALUES (?, ?, ?)`,
		at.UTC().Format(time.RFC3339Nano), modelID, probeKind)
	return err
}

// CountCycles reports how many cycles exist, for tests and /healthz.
func (s *Store) CountCycles(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM cycles`).Scan(&n)
	return n, err
}

// Sweep deletes cycles older than the retention window and returns how many
// went. Children cascade (see the schema's ON DELETE CASCADE), so this is the
// only delete needed.
func (s *Store) Sweep(ctx context.Context, olderThan time.Time) (int64, error) {
	cutoff := olderThan.UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `DELETE FROM cycles WHERE started_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	cycles, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	// skipped_runs hangs off no cycle, so it needs its own sweep or it grows
	// without bound.
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM skipped_runs WHERE occurred_at < ?`, cutoff); err != nil {
		return cycles, err
	}
	return cycles, nil
}

// nullFloat stores v only when the run succeeded; a failed run stores NULL so
// the value can never be averaged as a zero.
func nullFloat(v float64, ok bool) any {
	if !ok {
		return nil
	}
	return v
}

func nullFloatAlways(v float64) any {
	if v <= 0 {
		return nil
	}
	return v
}

func nullInt(v int, ok bool) any {
	if !ok {
		return nil
	}
	return v
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullBool(b *bool) any {
	if b == nil {
		return nil
	}
	return boolToInt(*b)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
