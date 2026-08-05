import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  getCost,
  getNetSeries,
  getPulse,
  getSamples,
  getSeries,
  getSummary,
} from "./api";
import { streamSSE } from "./api/stream";
import type {
  CostBreakdown,
  Cycle,
  ModelSeries,
  NetSeries,
  Sample,
  Summary,
} from "./api/types";
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

// The window the verdict banner reads, fixed regardless of what the charts are
// showing. The banner answers "how is it right now"; the cards below answer
// "over the selected window". Tying the first to the second is how one failed
// cycle out of sixty published DEGRADED — and kept publishing it until the
// cycle aged out of the range, which on the 3-month view is three months.
//
// 24h rather than something shorter because a percentile still needs samples:
// the discrete failures the banner actually fires on come from summary.recent,
// which reaches back three hours whatever this says. The trade is that a
// latency spike twenty minutes ago is diluted in a day's P50 — but if it is bad
// enough to matter it produces timeouts, and those land in recent as failures.
const NOW_WINDOW = "24h";

// One day of cycles at the 5-minute cadence. The pulse strip is the only thing
// that wants this many, and it wants them narrow — see /api/pulse.
const PULSE_CYCLES = 288;

// How many rows to ask for PER model and probe. The table's own cap is what
// decides how many are drawn; this is the supply.
//
// Asking this many of each rather than a share of it: wide runs hourly per
// model, so it contributes a row every twelfth cycle, and a smaller ask would
// only shorten how far back the table reaches without saving a request.
const TABLE_ROWS = 20;

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
  // What the banner reads: a fixed window, so switching the charts to 3mo
  // cannot change the answer to "how is it right now".
  const [nowSummary, setNowSummary] = useState<Summary | null>(null);
  // The 7-day summary is the rolling baseline every higher-is-worse metric is
  // scored against, so it is fetched independently of the selected window.
  const [baseline, setBaseline] = useState<Summary | null>(null);
  const [ttft, setTtft] = useState<ModelSeries | null>(null);
  const [wideTtft, setWideTtft] = useState<ModelSeries | null>(null);
  const [tps, setTps] = useState<ModelSeries | null>(null);
  const [total, setTotal] = useState<ModelSeries | null>(null);
  const [net, setNet] = useState<NetSeries | null>(null);
  const [cost, setCost] = useState<CostBreakdown | null>(null);
  // One array per probed model; the strip merges them.
  const [cycles, setCycles] = useState<Cycle[][]>([]);
  // One array per model and probe kind — the full cross product; the table
  // merges them, as the strip does for models.
  const [samples, setSamples] = useState<Sample[][]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const models = useMemo(
    () => summary?.models.map((m) => m.model_id) ?? [],
    [summary],
  );

  const load = useCallback(async (key: string, signal?: AbortSignal) => {
    try {
      // Three summaries — the selected window, the banner's fixed one, the
      // baseline — but DEDUPLICATED, because on ?window=24h the first two are
      // the same request and on ?window=7d so are the first and third. The
      // daemon would serve the duplicate from its cache, but every /api/* call
      // still spends a token from the per-IP limiter, and this runs on every
      // cycle and every stream event.
      const wanted = [...new Set([key, NOW_WINDOW, BASELINE_WINDOW])];
      const [summaries, t, w, p, tot, n, c] = await Promise.all([
        Promise.all(wanted.map((k) => getSummary(k, "short", signal))),
        getSeries("ttft", key, "short", signal),
        getSeries("ttft", key, "wide", signal),
        getSeries("tps", key, "short", signal),
        getSeries("total", key, "short", signal),
        getNetSeries(key, signal),
        getCost(key, signal),
      ]);
      const byWindow = new Map(wanted.map((k, i) => [k, summaries[i]!]));
      const s = byWindow.get(key)!;
      setSummary(s);
      setNowSummary(byWindow.get(NOW_WINDOW)!);
      setBaseline(byWindow.get(BASELINE_WINDOW)!);
      setTtft(t);
      setWideTtft(w);
      setTps(p);
      setTotal(tot);
      setNet(n);
      setCost(c);
      setError(null);

      const probed = s.models.map((m) => m.model_id);
      if (probed.length > 0) {
        // Requests with two different shapes, because the two consumers want
        // two different things and neither should pay for the other.
        //
        // PULSE_CYCLES is a day at the 5-minute cadence — the strip draws one
        // bar per cycle and nothing less than the day makes its shape mean
        // anything. But a bar needs five fields, so /api/pulse serves five.
        //
        // Every model, not just the first: the strip draws the worse of them
        // per cycle, and drawing one model is how a failure on the other used
        // to be painted green. /api/pulse is per model by design — it is a
        // projection of one model's cycles — so the merge happens here.
        //
        // TABLE_ROWS is what the table shows, and only those rows carry every
        // measurement. Asking /api/samples for the day and rendering a score of
        // it is how a page ends up holding a detail series it never displays.
        //
        // EVERY model and both probes, which is the whole cross product: the
        // table calls itself the raw record, and it was showing one quarter of
        // one.
        //
        // Two separate omissions, with the same shape. The wide run was never
        // asked for at all — stored against the same cycle as the short one,
        // served by the same endpoint, simply never requested. And every
        // request named probed[0], so the second model's runs were absent from
        // the one surface that promises nothing is aggregated away, on a page
        // whose entire subject is the two of them side by side.
        //
        // The wide stagger made the second omission acute rather than merely
        // wrong: wide now alternates between models, so half the fleet's wide
        // runs landed on the model the table did not fetch, and the panel
        // looked like it was missing runs that had in fact happened.
        //
        // /api/samples filters on model and probe by design — mixing two TTFTs
        // inside one response is exactly what it exists to prevent — so, as
        // with the pulse, the merge happens here. Order matters: the table
        // sorts on the instant, and these arrive as one group per pair.
        const [pulses, ...raw] = await Promise.all([
          Promise.all(
            probed.map((id) => getPulse(id, "short", PULSE_CYCLES, signal)),
          ),
          ...probed.flatMap((id) => [
            getSamples(id, "short", TABLE_ROWS, signal),
            getSamples(id, "wide", TABLE_ROWS, signal),
          ]),
        ]);
        setCycles(pulses.map((p) => p.cycles));
        setSamples(raw.map((r) => r.samples));
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

  const verdict = buildVerdict(nowSummary, baseline);
  const mimoEdge =
    summary?.net.find((n) => n.target === TARGET_MIMO)?.connect.p50_ms ?? null;

  return (
    <>
      <div className="aura" aria-hidden="true" />
      <div className="relative z-10 mx-auto max-w-[1180px] px-5 pb-24 sm:px-8">
        <Masthead />
        <VerdictBanner verdict={verdict} loading={loading} />
        <div className="mb-6">
          <PulseStrip perModel={cycles} />
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
          <SeriesPanel
            title="Throughput"
            subtitle="Output tokens per second over the decode window. This leads rather than inter-token latency, because MiMo batches tokens into chunks and delivers them in bursts — the median inter-chunk gap collapses toward zero on a perfectly healthy run. Higher is better."
            series={tps}
            models={models}
            unit="tok/s"
            forceLinear
          />
          {/* The panels above measure the parts — how long until it starts, how
              fast it runs once it has. This is the sum, so it comes after them:
              it is only readable once its parts have been shown. */}
          <SeriesPanel
            title="The whole wait"
            subtitle="P50 end-to-end, request sent to last token. This one moves with answer LENGTH as well as with speed — output is capped at 150 tokens, and a short answer finishes sooner than a long one — so read a step change here against the throughput plot before calling it a slowdown. Failed runs are excluded. Lower is better."
            series={total}
            models={models}
            unit="ms"
          />
          {/* Below the sum rather than among the parts. This panel does not
              measure a share of the wait — it re-plots the first part against a
              second, larger prompt — and sitting between the parts and the sum
              it read as one more component the sum was made of. After it, the
              question it answers is the one a reader actually arrives with:
              given the wait above, what does a bigger prompt add to it? */}
          <PrefillPanel short={ttft} wide={wideTtft} models={models} />
          {/* Everything from here down rests on the handshake, so the panel
              that measures it comes first. Above, both of these forward-
              referenced an edge RTT and a Singapore reference host the reader
              had not met yet — the decomposition subtracted a number the page
              had not yet shown, and the attribution appealed to a host no panel
              had yet introduced. (The verdict banner can name that host: it is
              a summary, and a summary is allowed to state a conclusion the
              panels below then show the working for.) */}
          <NetworkPanel series={net} />
          <Decomposition summary={summary} edgeMs={mimoEdge} />
          <AvailabilityStrip summary={summary} />
          {/* Last of the panels, above the raw cycles. Everything over it
              measures the endpoint; this one measures what measuring it costs,
              which is a fact about us rather than about MiMo — so it reads as a
              footnote to the page rather than as one of its findings. */}
          <CostPanel cost={cost} />
          <SamplesTable perGroup={samples} />
        </div>
      </div>
    </>
  );
}
