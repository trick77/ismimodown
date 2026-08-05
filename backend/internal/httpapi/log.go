package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/trick77/mimostats/internal/redact"
)

// redactErr strips query strings, userinfo and bearer tokens from an error's
// rendered message before it reaches the logs.
//
// The rule itself now lives in internal/redact, because the scheduler needs the
// same one for the provider error body it quotes on an inference call's log
// line, and a security regex kept in two files drifts. See that package for why
// it works on the rendered string, and for what the returned error does to an
// errors.Is/As chain — its result is a log attribute, never control flow.
func redactErr(err error) error {
	return redact.Err(err)
}

// writeJSONError writes a JSON error body. The message is the CLIENT-facing
// one: callers must never pass a provider error body or a wrapped internal
// error through here.
func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// serverError logs the underlying cause of a 5xx with request context and
// returns a generic JSON error to the client, so internal details never leak to
// the browser. Every 500 path should go through here so failures are never
// silent.
func serverError(w http.ResponseWriter, r *http.Request, err error, clientMessage string) {
	slog.Error("request failed",
		"method", r.Method,
		"path", r.URL.Path,
		"client_message", clientMessage,
		"err", redactErr(err),
	)
	writeJSONError(w, http.StatusInternalServerError, clientMessage)
}
