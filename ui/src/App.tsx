import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { getNetSeries, getSamples, getSeries, getSummary } from "./api";
import { streamSSE } from "./api/stream";
import type { ModelSeries, NetSeries, Sample, Summary } from "./api/types";
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
import { MethodologyPanel } from "./MethodologyPanel";
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

// The rolling reference every higher-is-worse metric is scored against.
//
// Rolling rather than absolute, so the page keeps working if MiMo gets
// permanently faster or slower — a fixed threshold would either cry wolf
// forever or stop firing at all.
//
// The decision lives here because the client is the only thing that acts on
// it: the daemon serves whatever window it is asked for and has no opinion
// about which one is the baseline. It used to also carry a BaselineWindow
// constant, which nothing read — so changing it did nothing, and this line
// silently won. A constant whose value cannot affect behaviour is a trap, not
// documentation.
const BASELINE_WINDOW = "7d";

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
  const [summary, setSummary] = useState<Summary | null>(null);
  // The 7-day summary is the rolling baseline every higher-is-worse metric is
  // scored against, so it is fetched independently of the selected window.
  const [baseline, setBaseline] = useState<Summary | null>(null);
  const [ttft, setTtft] = useState<ModelSeries | null>(null);
  const [wideTtft, setWideTtft] = useState<ModelSeries | null>(null);
  const [tps, setTps] = useState<ModelSeries | null>(null);
  const [net, setNet] = useState<NetSeries | null>(null);
  const [samples, setSamples] = useState<Sample[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const models = useMemo(
    () => summary?.models.map((m) => m.model_id) ?? [],
    [summary],
  );

  const load = useCallback(async (key: string, signal?: AbortSignal) => {
    try {
      const [s, b, t, w, p, n] = await Promise.all([
        getSummary(key, "infer", signal),
        getSummary(BASELINE_WINDOW, "infer", signal),
        getSeries("ttft", key, "infer", signal),
        getSeries("ttft", key, "wide", signal),
        getSeries("tps", key, "infer", signal),
        getNetSeries(key, signal),
      ]);
      setSummary(s);
      setBaseline(b);
      setTtft(t);
      setWideTtft(w);
      setTps(p);
      setNet(n);
      setError(null);

      const first = s.models[0]?.model_id;
      if (first) {
        const raw = await getSamples(first, "infer", 288, signal);
        setSamples(raw.samples);
      }
    } catch (err) {
      if ((err as Error)?.name === "AbortError") return;
      setError((err as Error)?.message ?? "could not load");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    void load(windowKey, controller.signal);
    return () => controller.abort();
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
              void load(windowRef.current, controller.signal);
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
      void load(windowRef.current, controller.signal);
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

  const verdict = buildVerdict(summary, baseline);
  const mimoEdge =
    summary?.net.find((n) => n.target === TARGET_MIMO)?.connect.p50_ms ?? null;

  return (
    <>
      <div className="aura" aria-hidden="true" />
      <div className="relative z-10 mx-auto max-w-[1180px] px-5 pb-24 sm:px-8">
        <Masthead />
        <VerdictBanner verdict={verdict} loading={loading} />
        <div className="mb-6">
          <PulseStrip samples={samples} />
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
          <ModelCards summary={summary} baseline={baseline} />
          <SeriesPanel
            title="Time to first token"
            subtitle="P50 per bucket. Failed runs are excluded — an outage is counted as availability, not as latency. Lower is better."
            series={ttft}
            models={models}
            unit="ms"
          />
          <PrefillPanel infer={ttft} wide={wideTtft} models={models} />
          <SeriesPanel
            title="Throughput"
            subtitle="Output tokens per second over the decode window. This leads rather than inter-token latency, because MiMo batches tokens into chunks and delivers them in bursts — the median inter-chunk gap collapses toward zero on a perfectly healthy run. Higher is better."
            series={tps}
            models={models}
            unit="tok/s"
            forceLinear
          />
          {/* Everything from here down rests on the handshake, so the panel
              that measures it comes first. Above, both of these forward-
              referenced an edge RTT and a Singapore reference host the reader
              had not met yet — the decomposition subtracted a number the page
              had not yet shown, and the attribution appealed to a host it had
              not yet introduced. */}
          <NetworkPanel series={net} />
          <Decomposition summary={summary} edgeMs={mimoEdge} />
          <AvailabilityStrip summary={summary} />
          <SamplesTable samples={samples} />
          <MethodologyPanel />
        </div>
      </div>
    </>
  );
}
