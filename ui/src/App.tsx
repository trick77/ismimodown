import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { getDashboard } from "./api";
import { streamSSE } from "./api/stream";
import type { Dashboard } from "./api/types";
import { TARGET_MIMO } from "./api/types";
import { Masthead } from "./Masthead";
import { VerdictBanner } from "./VerdictBanner";
import { ModelCards } from "./ModelCards";
import { Decomposition } from "./Decomposition";
import { SeriesPanel } from "./SeriesPanel";
import { PrefillPanel } from "./PrefillPanel";
import { NetworkPanel } from "./NetworkPanel";
import { AvailabilityStrip } from "./AvailabilityStrip";
import { PulseStrip } from "./PulseStrip";
import { SamplesTable } from "./SamplesTable";
import { CostPanel } from "./CostPanel";
import { Footer } from "./Footer";
import { buildVerdict } from "./verdict";

// Mirrors samples.Windows on the daemon. 1h is absent for the reason documented
// there: at a 5-minute cadence it cannot hold enough samples to clear the
// percentile threshold, so every card on it read insufficient_data forever.
//
// A stale ?window=1h link is not a broken link — readWindow rejects any value
// not in this list and falls back to the default.
const WINDOWS = ["24h", "48h", "7d", "30d", "3mo"] as const;
const DEFAULT_WINDOW = "24h";

// One probe cycle. Nothing new can exist between two of them, so this is both
// the refetch interval and the ceiling on stream-reconnect backoff. It mirrors
// the daemon's cadence — if that moves, this moves with it.
const CYCLE_MS = 5 * 60 * 1000;

// The verdict's two reference windows — the rolling 7-day baseline every
// higher-is-worse metric is scored against, and the fixed 24h "right now" the
// banner reads regardless of what the charts show — are chosen by the daemon
// now, in dashboard_handler.go, and arrive as `baseline` and `now`.
//
// They moved because they were never a request parameter in spirit: they are
// what the page asks, and the page asked three times to say so. The reasoning
// went with them, and it is worth keeping in view from here:
//
//   Rolling rather than absolute, so the page keeps working if MiMo gets
//   permanently faster or slower — a fixed threshold would either cry wolf
//   forever or stop firing at all.
//
//   The banner's window is fixed regardless of the charts because tying the
//   first to the second is how one failed cycle out of sixty published
//   DEGRADED — and kept publishing it until the cycle aged out of the range,
//   which on the 3-month view is three months. 24h rather than something
//   shorter because a percentile still needs samples: the discrete failures
//   the banner actually fires on come from summary.recent, which reaches back
//   three hours regardless.
//
// The pulse strip's day of cycles and the raw table's row supply moved for the
// same reason and now live beside them as dashboardPulseLimit and
// dashboardSampleLimit. Nothing here reads them, and a constant whose value
// cannot affect behaviour is a trap rather than documentation.

// The window lives in the query string rather than in component state, so a
// view can be linked and reloaded. There is no router: one shell, one
// parameter.
function readWindow(): string {
  const raw = new URLSearchParams(window.location.search).get("window");
  return raw && (WINDOWS as readonly string[]).includes(raw)
    ? raw
    : DEFAULT_WINDOW;
}

export default function App() {
  const [windowKey, setWindowKey] = useState(readWindow);
  // ONE piece of state for the whole page, because one response carries it.
  //
  // This is not tidiness. It used to be eleven, filled by a load that ran from
  // three places — the window effect, the 5-minute interval and every stream
  // event — with no ordering between them. Two overlapping loads each called
  // eleven setters, and whichever call landed last won PER SETTER, so the page
  // could hold one window's charts beside another's cards. One response and
  // one setState makes that unrepresentable rather than unlikely.
  const [data, setData] = useState<Dashboard | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const models = useMemo(
    () => data?.summary.models.map((m) => m.model_id) ?? [],
    [data],
  );

  // Its OWN controller, deliberately not the stream's.
  //
  // The three callers used to share the SSE effect's single controller, which
  // meant cancelling a superseded load would have torn down the event stream
  // with it. Keeping one here lets a newer load abort an older one — which is
  // the point, since the older one's answer is about a window the reader has
  // already left.
  const loadCtl = useRef<AbortController | null>(null);
  // Abort is not enough on its own: a fetch that has already resolved cannot
  // be cancelled, so the sequence number is what keeps a slow earlier load
  // from publishing over a fast later one.
  const loadSeq = useRef(0);

  const load = useCallback(async (key: string) => {
    loadCtl.current?.abort();
    const ctl = new AbortController();
    loadCtl.current = ctl;
    const seq = ++loadSeq.current;
    try {
      // One request. It used to be fifteen, in two waves — and the second wave
      // could not start until the first had answered, because the models to
      // fan out over came back inside the summary. The daemon has known that
      // list since it booted.
      const next = await getDashboard(key, ctl.signal);
      // A superseded load must not publish. Aborting handles the ones still in
      // flight; this handles the one that already resolved while a newer
      // request was being made.
      if (seq !== loadSeq.current) return;
      setData(next);
      setError(null);
    } catch (err) {
      if ((err as Error)?.name === "AbortError" || seq !== loadSeq.current) {
        return;
      }
      setError((err as Error)?.message ?? "could not load");
    } finally {
      // Guarded for the same reason: without it a superseded load clears the
      // loading state while the load that replaced it is still running.
      if (seq === loadSeq.current) setLoading(false);
    }
  }, []);

  useEffect(() => {
    setLoading(true);
    void load(windowKey);
    // load aborts its own predecessor, so the cleanup only has to cover the
    // case where nothing replaces it — unmount.
    return () => loadCtl.current?.abort();
  }, [load, windowKey]);

  // Live updates. The stream carries only a cycle notification, so the client
  // refetches rather than trusting a payload — the server has already dropped
  // its response cache by the time the event arrives.
  //
  // The stream is the FAST path, not the only one. It ends for reasons that are
  // not errors — a sleeping laptop, a proxy capping connection age, a daemon
  // restart — so it is reopened with backoff, and a plain interval refetches on
  // the probe's own 5-minute cadence regardless. Either alone leaves the page
  // silently stale: without the reconnect a dropped stream never comes back,
  // and without the interval a stream that stays open but stops delivering
  // looks identical to one with nothing to say.
  const windowRef = useRef(windowKey);
  windowRef.current = windowKey;
  useEffect(() => {
    const controller = new AbortController();

    // Backoff caps at the cycle length: reconnecting more slowly than the thing
    // being watched would make the stream pointless, since the interval below
    // already covers that rate.
    void (async () => {
      let backoffMs = 1000;
      while (!controller.signal.aborted) {
        const openedAt = Date.now();
        try {
          await streamSSE(
            "/api/events",
            () => {
              void load(windowRef.current);
            },
            controller.signal,
          );
          // Reset only if the stream actually STAYED open. Resolving is not
          // proof it worked: a draining daemon, or a proxy answering 200 with
          // an immediately-closed body, resolves in milliseconds — and
          // resetting on that pins the client at one reconnect per second
          // forever, with nothing on screen to say so.
          if (Date.now() - openedAt >= backoffMs) {
            backoffMs = 1000;
          }
        } catch {
          // A dropped stream is not an error worth showing — the interval keeps
          // the page current either way.
        }
        if (controller.signal.aborted) return;
        await new Promise((r) => setTimeout(r, backoffMs));
        backoffMs = Math.min(backoffMs * 2, CYCLE_MS);
      }
    })();

    const tick = setInterval(() => {
      void load(windowRef.current);
    }, CYCLE_MS);

    return () => {
      clearInterval(tick);
      controller.abort();
    };
  }, [load]);

  const selectWindow = (key: string) => {
    setWindowKey(key);
    const url = new URL(window.location.href);
    url.searchParams.set("window", key);
    window.history.replaceState(null, "", url);
  };

  // Nothing has arrived and something still might. This is what the reserved
  // heights below key on — NOT on the page being empty.
  //
  // Empty is the wrong question, because a load that FAILED is also empty: the
  // strip would go on telling a screen reader it was "still loading" beside a
  // visible error, and the model cards would hold 500 px of blank ground for a
  // response that is never coming. Reserving space is a promise that something
  // is about to fill it, and this is the only state in which that is true.
  //
  // False once a refetch is in flight over data already on screen, which is
  // correct: those cards are still rendered, so there is nothing to hold.
  const pending = loading && data === null && error === null;

  const verdict = buildVerdict(data?.now ?? null, data?.baseline ?? null);
  const mimoEdge =
    data?.summary.net.find((n) => n.target === TARGET_MIMO)?.connect.p50_ms ??
    null;

  return (
    <>
      <div className="aura" aria-hidden="true" />
      <div className="relative z-10 mx-auto max-w-[1180px] px-5 pb-24 sm:px-8">
        <Masthead />
        {/* Everything the page is FOR, in one landmark, so a screen reader can
            jump past the masthead in a keystroke instead of arrowing through
            it. It starts at the verdict rather than at the masthead because
            Masthead renders the page's <header>, and a banner nested inside
            <main> stops being a banner. The footer is outside it for the same
            reason. */}
        <main>
          <VerdictBanner verdict={verdict} loading={loading} />
          <div className="mb-6">
            <PulseStrip
              perModel={data?.pulse.map((p) => p.cycles) ?? []}
              pending={pending}
            />
          </div>

          <nav className="mb-6 flex flex-wrap gap-2" aria-label="Time window">
            {WINDOWS.map((key) => (
              <button
                key={key}
                type="button"
                className="pill"
                aria-pressed={key === windowKey}
                onClick={() => selectWindow(key)}
              >
                {key}
              </button>
            ))}
          </nav>

          {error && (
            <div
              role="alert"
              className="mb-6 rounded-ui border border-danger/40 bg-danger/10 px-4 py-3 text-label text-danger"
            >
              {error}
            </div>
          )}

          <div className="grid gap-6">
            <ModelCards
              summary={data?.summary ?? null}
              baseline={data?.baseline ?? null}
              pending={pending}
            />
            <SeriesPanel
              title="Time to first token"
              subtitle="P50 per bucket. Failed runs are excluded — an outage is counted as availability, not as latency. Lower is better."
              series={data?.series.ttft ?? null}
              models={models}
              unit="ms"
            />
            <SeriesPanel
              title="Throughput"
              subtitle="Output tokens per second over the decode window. This leads rather than inter-token latency, because MiMo batches tokens into chunks and delivers them in bursts — the median inter-chunk gap collapses toward zero on a perfectly healthy run. Higher is better."
              series={data?.series.tps ?? null}
              models={models}
              unit="tok/s"
              forceLinear
            />
            {/* The panels above measure the parts — how long until it starts, how
              fast it runs once it has. This is the sum, so it comes after them:
              it is only readable once its parts have been shown. */}
            <SeriesPanel
              title="The whole wait"
              subtitle="P50 end-to-end, request sent to last token. Its resemblance to the time-to-first-token plot is the finding: most of the wait is getting to the first token, and the gap between the two — read off the axis, not the shape — is what decoding adds. Length matters too, since output caps at 150 tokens, so check a step change here against the throughput plot before calling it a slowdown. Failed runs are excluded. Lower is better."
              series={data?.series.total ?? null}
              models={models}
              unit="ms"
            />
            {/* Below the sum rather than among the parts. This panel does not
              measure a share of the wait — it re-plots the first part against a
              second, larger prompt — and sitting between the parts and the sum
              it read as one more component the sum was made of. After it, the
              question it answers is the one a reader actually arrives with:
              given the wait above, what does a bigger prompt add to it? */}
            <PrefillPanel
              short={data?.series.ttft ?? null}
              wide={data?.series.ttft_wide ?? null}
              models={models}
            />
            {/* Everything from here down rests on the handshake, so the panel
              that measures it comes first. Above, both of these forward-
              referenced an edge RTT and a Singapore reference host the reader
              had not met yet — the decomposition subtracted a number the page
              had not yet shown, and the attribution appealed to a host no panel
              had yet introduced. (The verdict banner can name that host: it is
              a summary, and a summary is allowed to state a conclusion the
              panels below then show the working for.) */}
            <NetworkPanel series={data?.series.network ?? null} />
            <Decomposition summary={data?.summary ?? null} edgeMs={mimoEdge} />
            <AvailabilityStrip summary={data?.summary ?? null} />
            {/* Last of the panels, above the raw cycles. Everything over it
              measures the endpoint; this one measures what measuring it costs,
              which is a fact about us rather than about MiMo — so it reads as a
              footnote to the page rather than as one of its findings. */}
            <CostPanel cost={data?.cost ?? null} />
            <SamplesTable
              perGroup={data?.samples.map((g) => g.samples) ?? []}
            />
          </div>
        </main>
        {/* Outside the panel grid: it is not a panel, and inside the grid it
            picked up the gap-6 rhythm and read as one more finding. */}
        <Footer />
      </div>
    </>
  );
}
