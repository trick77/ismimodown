// Command ismimodown runs the probe daemon and serves the public dashboard.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/trick77/ismimodown/internal/ban"
	"github.com/trick77/ismimodown/internal/config"
	"github.com/trick77/ismimodown/internal/httpapi"
	"github.com/trick77/ismimodown/internal/probe"
	"github.com/trick77/ismimodown/internal/retention"
	"github.com/trick77/ismimodown/internal/samples"
	"github.com/trick77/ismimodown/internal/scheduler"
	"github.com/trick77/ismimodown/internal/store"
	"github.com/trick77/ismimodown/web"
	"github.com/trick77/ship/kit/env"
	"github.com/trick77/ship/kit/httpx"
	"github.com/trick77/ship/kit/logging"
	"github.com/trick77/ship/kit/ratelimit"
	"github.com/trick77/ship/kit/run"
	"github.com/trick77/ship/kit/sse"
	"github.com/trick77/ship/kit/version"
)

// healthcheckFlag makes the binary probe itself.
//
// The runtime image is distroless: no shell, no curl, no wget. A container
// healthcheck therefore has to be the binary, and the alternative — adding a
// shell to the image so it can run one — would undo the reason the image is
// distroless in the first place.
var healthcheckFlag = flag.Bool("healthcheck", false,
	"probe the local /readyz endpoint and exit 0 (ready) or 1 (not)")

func main() {
	flag.Parse()
	if *healthcheckFlag {
		os.Exit(healthcheck())
	}
	if err := daemon(); err != nil {
		slog.Error("fatal", logging.Err(err))
		os.Exit(1)
	}
}

// healthcheck probes /readyz over loopback: readiness pings the database, so a
// 0 here means the process can both listen and reach its store.
//
// It reads BACKEND_ADDR so a non-default port still works; kit rewrites a
// wildcard bind to loopback — dialling ":8080" verbatim does not resolve.
func healthcheck() int {
	addr := env.New(os.Getenv).String("BACKEND_ADDR", ":8080")
	return httpx.Healthcheck(httpx.LocalURL(addr, "/readyz"))
}

func daemon() error {
	cfg, err := config.Load()
	if err != nil {
		// Logging is not configured yet, so this goes out on the default
		// handler. A config failure must still be legible.
		return err
	}
	if _, err := logging.Setup(logging.Options{Level: cfg.LogLevel, LevelVar: "BACKEND_LOG_LEVEL"}); err != nil {
		return err
	}

	slog.Info("starting ismimodown",
		"version", version.String(),
		"base_url", cfg.BaseURL,
		"db", cfg.DBPath,
	)

	// The container mounts /data as a volume; on a fresh host the directory may
	// exist but a nested path may not. Create it rather than failing to open.
	// 0750, not 0755: the directory holds the probe database, and nothing
	// outside the service's own user and group has any reason to read it.
	if dir := filepath.Dir(cfg.DBPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if err := store.Migrate(db); err != nil {
		return err
	}

	static, err := web.Handler()
	if err != nil {
		return err
	}

	sampleStore := samples.New(db)
	broker := sse.NewHub[[]byte](httpapi.HubOptions())
	// 2 requests/second sustained, 20 in a burst: generous for a browser
	// loading a dashboard (a handful of parallel fetches), tight enough that a
	// scraper cannot pin the process.
	limiter := ratelimit.New(ratelimit.Options{Rate: 2, Burst: 20})
	// Five 404s free, then one forgiven every 30 seconds. A real visitor spends
	// maybe one or two per visit (/favicon.ico, an apple-touch-icon) and never
	// notices the budget; a wordlist scanner is cut off within its first half
	// dozen paths and, because the bucket runs to -5 while it keeps trying, stays
	// cut off for three minutes rather than the thirty seconds a zero-floored
	// bucket would cost it.
	notFoundLimiter := ratelimit.New(ratelimit.Options{Rate: 1.0 / 30.0, Burst: 5})
	// Some requests are not a wrong guess to be metered but a scan to be
	// stopped: /wp-admin/install.php, /.env, anything ending .php. This binary
	// serves none of it, so there is no honest caller to protect and no budget
	// to spend — the caller is blocked outright for 48 hours. Nothing is
	// persisted, so restarting the container is the one way to clear a ban,
	// including your own.
	//
	// 10k addresses is the ceiling: the map is keyed by caller and each entry
	// lives two days, and the scanners this catches are exactly the ones that
	// rotate addresses. Well past any volume this site sees — the log runs to a
	// few dozen such requests a day.
	banStore := ban.New(48*time.Hour, 10_000)

	ctx, stop := run.Context(context.Background())
	defer stop()

	apiServer := httpapi.NewServer(httpapi.Deps{
		DB:              db,
		Samples:         sampleStore,
		Static:          static,
		Broker:          broker,
		Limiter:         limiter,
		NotFoundLimiter: notFoundLimiter,
		Ban:             banStore,
		Models:          cfg.Models,
		Prices:          cfg.Prices,
		ProbeUserAgent:  cfg.ProbeUserAgent,
	})

	sched := scheduler.New(scheduler.Deps{
		Store: sampleStore,
		Prober: probe.NewClient(probe.Config{
			BaseURL:       cfg.BaseURL,
			APIKey:        cfg.APIKey,
			UserAgent:     cfg.ProbeUserAgent,
			SystemPrompt:  cfg.ProbeSystemPrompt,
			DialTimeout:   cfg.DialTimeout,
			HeaderTimeout: cfg.HeaderTimeout,
			TTFTTimeout:   cfg.TTFTTimeout,
			IdleTimeout:   cfg.IdleTimeout,
			Timeout:       cfg.ProbeTimeout,
		}),
		Pinger:      probe.NewPinger(cfg.PingTimeout),
		Models:      cfg.Models,
		MimoSGPHost: cfg.MimoSGPHost,
		RefSGPHost:  cfg.RefSGPHost,
		MimoAMSHost: cfg.MimoAMSHost,
		RefAMSHost:  cfg.RefAMSHost,
		OnCycle: func(cycleID int64) {
			// Rebuild every window first, THEN notify. Every open tab refetches
			// the moment the event arrives, so the cache must already hold the
			// new cycle: dropping it here and letting the tabs fill it made
			// each of them a cold build, on one CPU, all at once — five
			// concurrent cold requests measured 19 s each on the live site.
			//
			// A failed warm-up has already dropped the cache, so the tabs still
			// see the new cycle, built on demand and coalesced; and the client
			// refetches on its own interval regardless of whether this event
			// ever goes out.
			warmCache(ctx, apiServer)
			broker.Publish([]byte(fmt.Sprintf(`{"cycle_id":%d}`, cycleID)))
		},
	})
	sweeper := retention.New(sampleStore, cfg.Retention)

	// Once at startup too, or the first visitor after a deploy is the builder.
	// Beside listening rather than before it: the sweep can run for several
	// seconds on a full retention window, and the container's healthcheck
	// start_period is 15 s. A visitor who arrives mid-sweep queues on the
	// build slot and is answered from the entry the sweep has just written,
	// never from a build of their own.
	go warmCache(ctx, apiServer)

	// No server WriteTimeout (kit sets none): /api/events is a long-lived SSE
	// stream and a whole-response deadline would sever it mid-connection.
	// kit's sse.Writer bounds each write instead (10 s per frame), so a reader
	// that stops draining is still cut off.
	srv := httpx.NewServer(cfg.Addr, apiServer)
	// End the streams the moment Shutdown starts, BEFORE it waits on them:
	// Shutdown never cancels a request's context, so an open dashboard would
	// hold it until the drain timeout. BaseContext would cancel every in-flight
	// request instead, failing a mid-flight /api/dashboard on a graceful
	// restart; closing the hub ends only the streams and lets the rest drain.
	srv.RegisterOnShutdown(broker.Close)

	// The probe loop and the sweeper share the signal context, so SIGTERM stops
	// them at the same moment it stops accepting requests. Waited on below via
	// the WaitGroup: a cycle mid-flight must finish its write or abandon it
	// cleanly, never be killed between the INSERT and the COMMIT.
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); sched.Run(ctx) }()
	go func() { defer wg.Done(); sweeper.Run(ctx) }()
	// The limiters bound and prune their own maps (kit keeps a bucket in debt,
	// so a scanner cannot clear its record by pausing). The ban store needs a
	// sweep: it drops entries whose 48 hours are up, and nothing else.
	go func() {
		defer wg.Done()
		for run.Sleep(ctx, 10*time.Minute) {
			banStore.Sweep()
		}
	}()

	slog.Info("listening", "addr", cfg.Addr)
	err = httpx.Serve(ctx, srv, 15*time.Second)
	// A failed listen returns with the signal context still live: stop the
	// loops too, or wg.Wait never returns.
	stop()
	wg.Wait()
	// Past the drain deadline Shutdown has cut the remaining connections; the
	// process still exits as asked, so that is a note rather than a failure.
	if errors.Is(err, context.DeadlineExceeded) {
		slog.Warn("shutdown deadline passed, open connections cut")
		return nil
	}
	return err
}

// warmCache rebuilds every dashboard window into the response cache and logs
// how long it took: the sweep runs on the scheduler's goroutine between one
// cycle and the next, and its duration is the one number that says whether
// the build cost is creeping toward the cycle interval.
func warmCache(ctx context.Context, apiServer *httpapi.Server) {
	// A cycle that lands during shutdown has nobody left to warm for.
	if ctx.Err() != nil {
		return
	}
	started := time.Now()
	if err := apiServer.Warm(ctx); err != nil {
		slog.Error("dashboard warm-up failed", logging.Err(err))
		return
	}
	slog.Info("dashboard warmed", "took", time.Since(started).Round(time.Millisecond))
}
