package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trick77/mimostats/internal/store"
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

func TestHealthzReportsOKAndVersion(t *testing.T) {
	srv := New(Deps{Version: "1.2.3", DB: openTestDB(t)})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %q, want ok", body["status"])
	}
	if body["version"] != "1.2.3" {
		t.Errorf("version = %q, want 1.2.3", body["version"])
	}
}

// A process that is listening but cannot reach SQLite is not healthy: it would
// keep answering 200 to the container healthcheck while recording nothing.
func TestHealthzFailsWhenDatabaseIsGone(t *testing.T) {
	db := openTestDB(t)
	db.Close()

	srv := New(Deps{Version: "dev", DB: db})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestHealthzRejectsNonGET(t *testing.T) {
	srv := New(Deps{DB: openTestDB(t)})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/healthz", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestStaticHandlerServesSPA(t *testing.T) {
	static := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("spa"))
	})
	srv := New(Deps{DB: openTestDB(t), Static: static})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK || rec.Body.String() != "spa" {
		t.Errorf("got %d %q, want 200 \"spa\"", rec.Code, rec.Body.String())
	}
}

// Deps.Static is optional so tests need no embedded bundle; a nil one must 404
// rather than panic.
func TestNilStaticHandler404s(t *testing.T) {
	srv := New(Deps{DB: openTestDB(t)})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/anything", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestRecoveryTurnsPanicIntoA500(t *testing.T) {
	boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
	srv := New(Deps{DB: openTestDB(t), Static: boom})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panics", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// The API is public and unauthenticated, and MiMo transport errors can carry
// the bearer token or a query string. None of that may reach the logs, and none
// of it may reach the client.
func TestRedactErrStripsSecrets(t *testing.T) {
	cases := []struct {
		name     string
		in       error
		mustNot  string
		mustKeep string
	}{
		{
			name:     "query string",
			in:       errors.New(`Get "https://token-plan-sgp.xiaomimimo.com/v1/models?key=tp-secret": timeout`),
			mustNot:  "tp-secret",
			mustKeep: "/v1/models",
		},
		{
			name:    "bearer token",
			in:      errors.New(`request failed with header Authorization: Bearer tp-livekey123`),
			mustNot: "tp-livekey123",
		},
		{
			name:    "userinfo",
			in:      errors.New(`dial https://user:pass@example.com/v1 failed`),
			mustNot: "user:pass",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactErr(tc.in).Error()
			if strings.Contains(got, tc.mustNot) {
				t.Errorf("redacted message still contains %q: %s", tc.mustNot, got)
			}
			if tc.mustKeep != "" && !strings.Contains(got, tc.mustKeep) {
				t.Errorf("redaction removed the useful diagnostic %q: %s", tc.mustKeep, got)
			}
		})
	}
}

func TestRedactErrPassesNilAndCleanErrorsThrough(t *testing.T) {
	if redactErr(nil) != nil {
		t.Error("nil must stay nil")
	}
	// An error needing no redaction is returned unchanged, preserving its chain
	// for any caller that does inspect it.
	inner := errors.New("plain failure")
	wrapped := fmt.Errorf("context: %w", inner)
	got := redactErr(wrapped)
	if !errors.Is(got, inner) {
		t.Error("an unredacted error must keep its wrap chain")
	}
}

// serverError is the only 5xx path; it must never echo the underlying error to
// the client, since that error can carry a provider body or a credential.
func TestServerErrorDoesNotLeakTheCause(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)

	serverError(rec, req, errors.New("sqlite: no such table: tp-secret-detail"), "internal error")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "tp-secret-detail") || strings.Contains(body, "sqlite") {
		t.Errorf("response leaked the underlying cause: %s", body)
	}
	if !strings.Contains(body, "internal error") {
		t.Errorf("response should carry the client message, got: %s", body)
	}
}
