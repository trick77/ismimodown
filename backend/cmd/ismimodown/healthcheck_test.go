package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The container healthcheck IS this function — the runtime image is distroless
// and has no curl — so a bug here means either a permanently-unhealthy
// container or, worse, one reported healthy while it cannot serve.
func TestHealthcheckAgainstALiveServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("probed %q, want /healthz", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("BACKEND_ADDR", strings.TrimPrefix(srv.URL, "http://"))

	if err := healthcheck(); err != nil {
		t.Errorf("healthcheck failed against a healthy server: %v", err)
	}
}

// A non-200 must fail. /healthz already pings the database, so this is what
// makes "the process is up but cannot reach SQLite" an unhealthy container
// rather than a silently broken one.
func TestHealthcheckFailsOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	t.Setenv("BACKEND_ADDR", strings.TrimPrefix(srv.URL, "http://"))

	if err := healthcheck(); err == nil {
		t.Error("expected a failure for a 500 response")
	}
}

func TestHealthcheckFailsWhenNothingIsListening(t *testing.T) {
	// Port 1 on loopback: reserved and never served.
	t.Setenv("BACKEND_ADDR", "127.0.0.1:1")

	if err := healthcheck(); err == nil {
		t.Error("expected a failure when nothing is listening")
	}
}

// The server binds a wildcard in production (":8080"), and dialling that
// verbatim does not resolve — so the healthcheck would fail against a
// perfectly healthy container.
func TestHealthcheckRewritesAWildcardBindToLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	for _, addr := range []string{":" + port, "0.0.0.0:" + port} {
		t.Run(addr, func(t *testing.T) {
			t.Setenv("BACKEND_ADDR", addr)
			if err := healthcheck(); err != nil {
				t.Errorf("healthcheck failed for bind %q: %v", addr, err)
			}
		})
	}
}

func TestHealthcheckRejectsAMalformedAddress(t *testing.T) {
	t.Setenv("BACKEND_ADDR", "not-a-host-port")

	if err := healthcheck(); err == nil {
		t.Error("expected a failure for a malformed BACKEND_ADDR")
	}
}
