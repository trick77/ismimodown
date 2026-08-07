package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/trick77/ismimodown/internal/ban"
	"github.com/trick77/ismimodown/internal/samples"
	"github.com/trick77/ismimodown/web"
)

// allLogLines decodes every line in the buffer, not just the "request" ones
// logLines keeps — the ban line carries its own message.
func allLogLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line is not JSON: %s", line)
		}
		out = append(out, entry)
	}
	return out
}

// bannedServer is a server with a real ban store and no other limiter, so every
// status below is the ban gate's doing and nothing else's.
func bannedServer(t *testing.T) http.Handler {
	t.Helper()
	db := openTestDB(t)
	return NewServer(Deps{
		Version: "test",
		DB:      db,
		Samples: samples.New(db),
		Models:  []string{"mimo-v2.5"},
		Ban:     ban.New(48*time.Hour, 100),
	})
}

// The whole point: one exploit path and the caller is out, for everything.
func TestExploitPathBansTheCaller(t *testing.T) {
	h := bannedServer(t)

	if code := getFrom(t, h, "/api/dashboard", "9.9.9.9").Code; code == http.StatusForbidden {
		t.Fatal("caller was banned before doing anything")
	}
	if code := getFrom(t, h, "/wp-admin/install.php", "9.9.9.9").Code; code != http.StatusForbidden {
		t.Fatalf("the exploit request = %d, want 403", code)
	}
	// Banned means banned — not merely barred from more exploit paths.
	for _, path := range []string{"/", "/api/dashboard", "/assets/index-abc.js"} {
		if code := getFrom(t, h, path, "9.9.9.9").Code; code != http.StatusForbidden {
			t.Errorf("%s after the ban = %d, want 403", path, code)
		}
	}
}

// Bans are per caller. A shared block would let one scanner take the site down
// for everyone, which is the opposite of the point.
func TestBanDoesNotAffectOtherCallers(t *testing.T) {
	h := bannedServer(t)

	getFrom(t, h, "/.env", "9.9.9.9")

	if code := getFrom(t, h, "/api/dashboard", "10.10.10.10").Code; code == http.StatusForbidden {
		t.Errorf("an unrelated caller = %d, want anything but 403", code)
	}
}

// The gap this closes. web.spaHandler answers an unknown EXTENSIONLESS path
// with index.html and a 200, so /phpmyadmin and /wp-admin never produce the 404
// that notFoundPenalty needs to charge for — a scanner walking them paid
// nothing. Run against the real handler, because that 200 is the whole point.
func TestExtensionlessExploitPathIsCaughtDespiteThe200(t *testing.T) {
	static, err := web.Handler()
	if err != nil {
		t.Fatalf("web.Handler: %v", err)
	}
	h := NewServer(Deps{
		Version: "test",
		DB:      openTestDB(t),
		Static:  static,
		Ban:     ban.New(48*time.Hour, 100),
	})

	// Confirm the premise first: an extensionless unknown path really is a 200.
	if code := getFrom(t, h, "/admin", "9.9.9.9").Code; code != http.StatusOK {
		t.Fatalf("/admin = %d, want 200; the premise of this test has changed", code)
	}

	if code := getFrom(t, h, "/phpmyadmin", "8.8.8.8").Code; code != http.StatusForbidden {
		t.Fatalf("/phpmyadmin = %d, want 403 — the 200 path must still ban", code)
	}
	if code := getFrom(t, h, "/", "8.8.8.8").Code; code != http.StatusForbidden {
		t.Errorf("the caller = %d after the ban, want 403", code)
	}
}

// The expensive half of a false positive: a banned visitor sees nothing for two
// days. Every one of these is served by this binary on an ordinary page load.
func TestNormalTrafficIsNeverBanned(t *testing.T) {
	static, err := web.Handler()
	if err != nil {
		t.Fatalf("web.Handler: %v", err)
	}
	h := NewServer(Deps{
		Version: "test",
		DB:      openTestDB(t),
		Static:  static,
		Ban:     ban.New(48*time.Hour, 100),
	})

	for _, path := range []string{
		"/", "/api/dashboard", "/assets/index-abc123.js", "/favicon.ico",
		"/robots.txt", "/manifest.webmanifest", "/about",
	} {
		if code := getFrom(t, h, path, "9.9.9.9").Code; code == http.StatusForbidden {
			t.Errorf("%s = 403; this is traffic the site serves", path)
		}
	}
	// And none of it left a ban behind.
	if code := getFrom(t, h, "/", "9.9.9.9").Code; code == http.StatusForbidden {
		t.Error("a normal page load banned the visitor")
	}
}

// A health probe that fails because someone else scanned the box restarts a
// healthy container. Same reasoning as the 404 penalty's exemption.
func TestHealthzIsNeverBanned(t *testing.T) {
	h := bannedServer(t)

	getFrom(t, h, "/wp-login.php", "9.9.9.9")
	if code := getFrom(t, h, "/", "9.9.9.9").Code; code != http.StatusForbidden {
		t.Fatalf("caller = %d, want 403; the test needs a banned caller", code)
	}
	if code := getFrom(t, h, "/healthz", "9.9.9.9").Code; code != http.StatusOK {
		t.Errorf("/healthz = %d, want 200 even for a banned caller", code)
	}
}

// A bare 403, not the limiter's 429. Retry-After would name the moment to come
// back, which is a negotiation this response is not having — and it would
// publish the ban's length to the one party with a use for it.
func TestBannedResponseIsAnEmpty403(t *testing.T) {
	h := bannedServer(t)

	rec := getFrom(t, h, "/.env", "9.9.9.9")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if ra := rec.Header().Get("Retry-After"); ra != "" {
		t.Errorf("Retry-After = %q, want none", ra)
	}
	if body := rec.Body.String(); body != "" {
		t.Errorf("body = %q, want empty", body)
	}
	// It is still a response like any other: the security headers ride along,
	// because banGate sits inside securityHeaders.
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("a 403 lost the security headers")
	}
}

// Someone reading this line has just locked themselves out. "banned" alone
// would not tell them which request did it.
func TestBanIsLoggedWithKeyAndPath(t *testing.T) {
	buf := captureLogs(t)
	h := bannedServer(t)

	getFrom(t, h, "/wp-admin/install.php", "9.9.9.9")

	var found map[string]any
	for _, entry := range allLogLines(t, buf) {
		if entry["msg"] == "banned caller for exploit path" {
			found = entry
		}
	}
	if found == nil {
		t.Fatalf("no ban line logged: %s", buf.String())
	}
	if found["level"] != "WARN" {
		t.Errorf("level = %v, want WARN", found["level"])
	}
	if found["client"] != "9.9.9.9" {
		t.Errorf("client = %v, want 9.9.9.9", found["client"])
	}
	if found["path"] != "/wp-admin/install.php" {
		t.Errorf("path = %v, want the triggering path", found["path"])
	}
	if found["ban"] != "new" {
		t.Errorf("ban = %v, want \"new\" for a first offence", found["ban"])
	}
	// Absolute, so the line still answers "is that caller still out?" when it
	// is read out of yesterday's logs.
	until, ok := found["until"].(string)
	if !ok || until == "" {
		t.Errorf("until = %v, want an RFC3339 expiry", found["until"])
	} else if _, err := time.Parse(time.RFC3339, until); err != nil {
		t.Errorf("until = %q, not RFC3339: %v", until, err)
	}
}

// A caller that comes back while banned has its block reset to a full fresh
// term, and that is logged as an extension rather than as a new ban.
func TestReturningBannedCallerIsLoggedAsAnExtension(t *testing.T) {
	buf := captureLogs(t)
	h := bannedServer(t)

	getFrom(t, h, "/.env", "9.9.9.9")         // the ban
	getFrom(t, h, "/wp-login.php", "9.9.9.9") // back to scanning

	var kinds []string
	for _, entry := range allLogLines(t, buf) {
		if entry["msg"] == "banned caller for exploit path" {
			kind, _ := entry["ban"].(string)
			kinds = append(kinds, kind)
		}
	}
	if len(kinds) != 2 {
		t.Fatalf("got %d ban lines, want 2: %s", len(kinds), buf.String())
	}
	if kinds[0] != "new" || kinds[1] != "extended" {
		t.Errorf("ban kinds = %v, want [new extended]", kinds)
	}
}

// A scanner that goes back to its wordlist resets the clock rather than topping
// it up, so getting back in means actually stopping for a full term.
func TestRepeatExploitPathResetsTheFullTerm(t *testing.T) {
	store := ban.New(48*time.Hour, 100)
	db := openTestDB(t)
	h := NewServer(Deps{
		Version: "test", DB: db, Samples: samples.New(db),
		Models: []string{"mimo-v2.5"}, Ban: store,
	})

	getFrom(t, h, "/.env", "9.9.9.9")
	first, ok := store.Expires("9.9.9.9")
	if !ok {
		t.Fatal("caller is not banned after an exploit request")
	}

	if code := getFrom(t, h, "/wp-login.php", "9.9.9.9").Code; code != http.StatusForbidden {
		t.Fatalf("return request = %d, want 403", code)
	}

	second, ok := store.Expires("9.9.9.9")
	if !ok {
		t.Fatal("caller stopped being banned after coming back")
	}
	if !second.After(first) {
		t.Errorf("expiry %v did not move past %v; scanning again must reset the term", second, first)
	}
	// Reset to a full term, not extended by one: the new expiry is ~48h out,
	// not ~96h.
	if remaining := time.Until(second); remaining > 49*time.Hour {
		t.Errorf("remaining = %v, want about 48h — the term is reset, not accumulated", remaining)
	}
}

// The trap this avoids, and the reason only a repeat EXPLOIT path renews a ban.
//
// A key is an address, and an address can be a NAT or CGNAT pool: one device
// behind it asking for /wp-login.php bans every other. If renewing took ANY
// request, a bystander with the dashboard open would have their own tab renew
// the ban forever — the SPA reconnects its stream in an unbounded loop and
// refetches every five minutes — so the documented "wait 48 hours" escape would
// never arrive and only a container restart would help.
func TestOrdinaryRequestsFromABannedCallerDoNotRenewTheBan(t *testing.T) {
	store := ban.New(48*time.Hour, 100)
	db := openTestDB(t)
	h := NewServer(Deps{
		Version: "test", DB: db, Samples: samples.New(db),
		Models: []string{"mimo-v2.5"}, Ban: store,
	})

	getFrom(t, h, "/.env", "9.9.9.9")
	first, ok := store.Expires("9.9.9.9")
	if !ok {
		t.Fatal("caller is not banned after an exploit request")
	}

	// Exactly what an open dashboard on a shared address keeps doing.
	for range 20 {
		if code := getFrom(t, h, "/api/events", "9.9.9.9").Code; code != http.StatusForbidden {
			t.Fatalf("banned caller = %d, want 403", code)
		}
		getFrom(t, h, "/api/dashboard", "9.9.9.9")
		getFrom(t, h, "/", "9.9.9.9")
	}

	second, ok := store.Expires("9.9.9.9")
	if !ok {
		t.Fatal("caller stopped being banned")
	}
	if !second.Equal(first) {
		t.Errorf("expiry moved from %v to %v; a bystander's own page must not renew their ban", first, second)
	}
}

// The callers this catches do not stop knocking. One line per refusal would
// bury the ban that started it.
func TestRepeatedKnockingDoesNotFloodTheBanLog(t *testing.T) {
	buf := captureLogs(t)
	h := bannedServer(t)

	getFrom(t, h, "/.env", "9.9.9.9")
	for range 200 {
		getFrom(t, h, "/", "9.9.9.9")
	}

	lines := 0
	for _, entry := range allLogLines(t, buf) {
		if entry["msg"] == "banned caller for exploit path" {
			lines++
		}
	}
	// The initial ban, plus at most one extension line inside the interval.
	if lines > 2 {
		t.Errorf("got %d ban lines for 201 requests, want at most 2", lines)
	}
}

// Nil is the documented "no such block" case, and every other test in this
// package relies on it.
func TestNilBanStoreServesEverything(t *testing.T) {
	h := NewServer(Deps{Version: "test", DB: openTestDB(t)})

	for i := 0; i < 20; i++ {
		if code := getFrom(t, h, "/.env", "9.9.9.9").Code; code != http.StatusNotFound {
			t.Fatalf("request %d = %d, want 404 with no ban store wired", i, code)
		}
	}
}

// A banned caller must not reach the SSE handler either. /api/events is
// registered on the OUTER mux and so bypasses the request limiter entirely —
// banGate sits outside that mux precisely so this route is covered too.
func TestBannedCallerCannotOpenAnEventStream(t *testing.T) {
	h := bannedServer(t)

	getFrom(t, h, "/.env", "9.9.9.9")

	if code := getFrom(t, h, "/api/events", "9.9.9.9").Code; code != http.StatusForbidden {
		t.Errorf("/api/events = %d, want 403 for a banned caller", code)
	}
}
