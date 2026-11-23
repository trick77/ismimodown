package httpapi

import (
	"net/http"
	"testing"
	"time"

	"github.com/trick77/ismimodown/internal/ban"
	"github.com/trick77/ismimodown/internal/samples"
	"github.com/trick77/ship/kit/httpx"
)

type fed struct {
	path, route string
	status      int
}

// feedRecorder stands in for stakeoutfeed's middleware: it notes what that one
// reads after the handler ran.
func feedRecorder(out *[]fed) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := httpx.NewStatusWriter(w)
			next.ServeHTTP(sw, r)
			*out = append(*out, fed{r.URL.Path, r.Pattern, sw.Status()})
		})
	}
}

// The feed sits where it sees what the gates refuse and the route the mux
// matched: outside banGate, inside the middleware that clones the request.
func TestFeedSeesRoutesAndRefusals(t *testing.T) {
	db := openTestDB(t)
	var got []fed
	h := NewServer(Deps{
		DB:      db,
		Samples: samples.New(db),
		Models:  []string{"mimo-v2.6-flash"},
		Ban:     ban.New(48*time.Hour, 100),
		Feed:    feedRecorder(&got),
	})

	getFrom(t, h, "/api/dashboard", "9.9.9.9")
	getFrom(t, h, "/wp-admin/install.php", "9.9.9.9")

	if len(got) != 2 {
		t.Fatalf("fed %d requests, want 2: %+v", len(got), got)
	}
	if got[0].route != "GET /api/dashboard" {
		t.Errorf("route = %q, want the inner mux's pattern", got[0].route)
	}
	if got[1].status != http.StatusForbidden {
		t.Errorf("exploit path fed as %d, want banGate's 403", got[1].status)
	}
}
