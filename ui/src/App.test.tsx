import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App, { STREAM_OPEN_DELAY_MS } from "./App";

// A clean hour of cycles. The verdict banner reads this rather than the
// window's fault counts, so every fixture needs one or the page has nothing to
// say about right now.
const cleanCycles = (n = 12) =>
  Array.from({ length: n }, (_, i) => ({
    at: new Date(
      Date.parse("2026-08-04T12:00:00Z") - i * 5 * 60 * 1000,
    ).toISOString(),
    fault: "ok",
    models: { "mimo-v2.5": { ok: true, answer_ok: true } },
  }));

const summary = (over: Record<string, unknown> = {}) => ({
  window: "24h",
  cycles: 288,
  models: [
    {
      model_id: "mimo-v2.5",
      probe: "short",
      ttft: { n: 288, sufficient: true, p50_ms: 916, p95_ms: 1400 },
      itl: { n: 288, sufficient: true, p50_ms: 24, p95_ms: 40 },
      tps: { n: 288, sufficient: true, p50_ms: 41, p95_ms: 60 },
      attempts: 288,
      succeeded: 288,
      available_pct: 100,
      answered: 288,
      correct: 288,
      correct_pct: 100,
      max_reasoning_tokens: 0,
      max_cached_tokens: 0,
    },
  ],
  net: [
    {
      target: "mimo_sgp",
      connect: { n: 288, sufficient: true, p50_ms: 170, p95_ms: 200 },
      attempts: 288,
      succeeded: 288,
      available_pct: 100,
    },
  ],
  faults: { ok: 288 },
  recent: cleanCycles(),
  skipped_runs: 0,
  generated_at: "2026-08-04T12:00:00Z",
  ...over,
});

const emptySeries = {
  window: "24h",
  bucket_s: 900,
  metric: "ttft",
  probe: "short",
  models: {},
};
const emptyNet = {
  window: "24h",
  bucket_s: 900,
  metric: "network",
  targets: {},
};

// A priced window. The panel refuses to render below ten runs, so a fixture
// under that floor silently tests nothing.
const cost = () => ({
  window: "24h",
  currency: "USD",
  offpeak_coefficient: 0.8,
  total: {
    runs: 624,
    tokens: { prompt: 213000, cached: 0, output: 55000 },
    usd: 0.1814,
    list_usd: 0.1944,
  },
  phases: [
    {
      phase: "offpeak",
      runs: 208,
      tokens: { prompt: 71000, cached: 0, output: 18300 },
      usd: 0.0518,
      list_usd: 0.0648,
    },
  ],
  probes: [
    {
      probe: "short",
      runs: 576,
      tokens: { prompt: 40320, cached: 0, output: 40320 },
      usd: 0.0908,
      list_usd: 0.0973,
    },
  ],
  series: [
    { t: Date.parse("2026-08-04T11:00:00Z") / 1000, usd: 0.008, runs: 26 },
  ],
  bucket_s: 3600,
  unpriced_runs: 0,
  offpeak_spans: [],
  offpeak_until: Date.parse("2026-08-04T16:00:00Z") / 1000,
  offpeak_active: false,
  generated_at: "2026-08-04T12:00:00Z",
});

// One group per model and probe, as the daemon composes them: model-major,
// short before wide. Spelled out rather than echoed off the query, because
// there is no query left to echo — the fan-out is the server's now, and a
// fixture that collapsed it would hide a merge that dropped a group.
const sampleGroups = (over: Record<string, unknown[]> = {}) => [
  { model_id: "mimo-v2.5", probe: "short", samples: over["short"] ?? [] },
  { model_id: "mimo-v2.5", probe: "wide", samples: over["wide"] ?? [] },
];

// The whole page, in one body. Overrides stay per-part, so every call site
// still says which part it is about.
const dashboard = (overrides: Record<string, unknown> = {}) => ({
  window: "24h",
  generated_at: "2026-08-04T12:00:00Z",
  summary: overrides.summary ?? summary(),
  now: overrides.now ?? overrides.summary ?? summary(),
  baseline: overrides.baseline ?? overrides.summary ?? summary(),
  series: {
    ttft: overrides.ttft ?? emptySeries,
    ttft_wide: overrides.ttftWide ?? emptySeries,
    tps: overrides.tps ?? emptySeries,
    total: overrides.total ?? emptySeries,
    network: overrides.net ?? emptyNet,
  },
  cost: overrides.cost ?? cost(),
  pulse: overrides.pulse ?? [
    { model_id: "mimo-v2.5", probe: "short", cycles: [] },
  ],
  samples:
    overrides.samples ??
    sampleGroups(overrides.sampleRows as Record<string, unknown[]>),
});

function mockFetch(overrides: Record<string, unknown> = {}) {
  return vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    const body = dashboard(overrides);
    if (url.includes("/api/events")) {
      // eventsStatus makes the stream FAIL instead, which is what the reconnect
      // tests need — streamSSE throws on a non-OK response.
      if (overrides.eventsStatus) {
        return new Response("", { status: Number(overrides.eventsStatus) });
      }
      // A 200 whose body is already finished: the stream RESOLVES rather than
      // throwing, which is what a draining daemon or a proxy answering 200 with
      // no body looks like from here.
      if (overrides.eventsCloseImmediately) {
        return new Response("", { status: 200 });
      }
      // Never resolves during a test; the component aborts it on unmount.
      return new Promise<Response>(() => {});
    }
    return new Response(JSON.stringify(body), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  });
}

beforeEach(() => {
  window.history.replaceState(null, "", "/");
});
afterEach(() => {
  vi.unstubAllGlobals();
});

describe("App", () => {
  it("renders the verdict and the model card once data arrives", async () => {
    vi.stubGlobal("fetch", mockFetch());
    render(<App />);

    await waitFor(() =>
      expect(screen.getByTestId("verdict")).toHaveTextContent(/normal/i),
    );
    expect(screen.getByTestId("model-card-mimo-v2.5")).toBeInTheDocument();
    expect(screen.getByText("916 ms")).toBeInTheDocument();
  });

  // End-to-end latency is the one number a reader waits for, and it is a
  // SEPARATE series — total_ms, not ttft_ms and not derived from tps. The panel
  // renders identically off whichever series it is handed, so the heading alone
  // cannot tell the two apart.
  //
  // There is no URL to assert on any more, so the fixture does the telling: only
  // `total` carries points, so only the whole-wait panel may have something to
  // draw. Wiring series.ttft into it — the mistake a field-name payload makes
  // possible — puts the empty state in the wrong card and fails here.
  it("charts total response time off the total metric", async () => {
    vi.stubGlobal(
      "fetch",
      mockFetch({
        total: {
          ...emptySeries,
          metric: "total",
          models: { "mimo-v2.5": [{ t: 0, n: 1, censored: 0, p50: 1700 }] },
        },
      }),
    );
    render(<App />);

    const whole = (await screen.findByText("The whole wait")).closest(
      "section",
    );
    expect(whole).not.toHaveTextContent(/Not enough data yet/);
    const ttft = screen
      .getByText("Time to first token")
      .closest("section") as HTMLElement;
    expect(ttft).toHaveTextContent(/Not enough data yet/);
  });

  // The table promises the raw record, and the hourly wide run was missing
  // from it — stored against the same cycle, served by the same endpoint, never
  // asked for.
  //
  // Which groups get FETCHED is the daemon's business now, and its own test
  // asserts the cross product. What is still this page's business, and what
  // this checks, is that it draws every group it is handed rather than the
  // first: the merge is the half of the bug that lives here.
  it("asks for both probes and draws them in one table", async () => {
    const row = (over: Record<string, unknown>) => ({
      at: "2026-08-04T12:00:00Z",
      model_id: "mimo-v2.5",
      ttft_ms: 900,
      total_ms: 1700,
      itl_p50_ms: 24,
      output_tps: 41,
      ok: true,
      answer_ok: true,
      error_class: null,
      ...over,
    });
    vi.stubGlobal(
      "fetch",
      mockFetch({
        sampleRows: {
          short: [row({ probe: "short" })],
          // Ungraded, as every wide run is, and sharing the short run's cycle.
          wide: [row({ probe: "wide", answer_ok: null, ttft_ms: 4200 })],
        },
      }),
    );
    render(<App />);

    expect(await screen.findByText("short")).toBeInTheDocument();
    expect(screen.getByText("wide")).toBeInTheDocument();
  });

  // The other half of the same omission: every request named the FIRST model,
  // so the second model's runs were absent from the page's only raw record.
  // Once the wide probe alternates between models, half the fleet's wide runs
  // land on the model that was never asked about — the table looked like it was
  // missing runs that had happened.
  //
  // Same division as above: the daemon sends a group per model, and this is
  // where drawing only the first would still show up.
  it("asks for every model's samples, not just the first", async () => {
    const row = (model: string, ttft: number) => ({
      at: "2026-08-04T12:00:00Z",
      model_id: model,
      probe: "short",
      ttft_ms: ttft,
      total_ms: 1700,
      itl_p50_ms: 24,
      output_tps: 41,
      ok: true,
      answer_ok: true,
      error_class: null,
    });
    vi.stubGlobal(
      "fetch",
      mockFetch({
        samples: [
          {
            model_id: "mimo-v2.5",
            probe: "short",
            samples: [row("mimo-v2.5", 916)],
          },
          {
            model_id: "mimo-v2.5-pro",
            probe: "short",
            samples: [row("mimo-v2.5-pro", 242)],
          },
        ],
      }),
    );
    render(<App />);

    // Both models' rows inside the table, not just the group that happened to
    // come first. Scoped to the card, because the model ids appear in the
    // cards above it too.
    const table = (await screen.findByText("Raw cycles")).closest(
      "section",
    ) as HTMLElement;
    expect(table).toHaveTextContent("916 ms");
    expect(table).toHaveTextContent("242 ms");
  });

  // The residual must never be called model time anywhere on the page.
  it("labels the residual as server-side time", async () => {
    vi.stubGlobal("fetch", mockFetch());
    render(<App />);
    await waitFor(() =>
      expect(screen.getByText(/never .model time/i)).toBeInTheDocument(),
    );
  });

  it("puts the selected window in the query string so a view can be linked", async () => {
    vi.stubGlobal("fetch", mockFetch());
    render(<App />);

    await userEvent.click(screen.getByRole("button", { name: "7d" }));
    await waitFor(() =>
      expect(new URL(window.location.href).searchParams.get("window")).toBe(
        "7d",
      ),
    );
  });

  // The default is what a bare URL already means, so spelling it out only
  // makes the address bar uglier. Going back to it clears the parameter.
  it("drops the query string when the default window is selected", async () => {
    window.history.replaceState(null, "", "/?window=7d");
    vi.stubGlobal("fetch", mockFetch());
    render(<App />);

    await userEvent.click(screen.getByRole("button", { name: "24h" }));
    await waitFor(() => expect(new URL(window.location.href).search).toBe(""));
  });

  it("reads the initial window from the query string", async () => {
    window.history.replaceState(null, "", "/?window=48h");
    vi.stubGlobal("fetch", mockFetch());
    render(<App />);

    await waitFor(() =>
      expect(screen.getByRole("button", { name: "48h" })).toHaveAttribute(
        "aria-pressed",
        "true",
      ),
    );
  });

  // 1h was removed: at one cycle every five minutes it could hold at most 12
  // samples against a threshold of 20, so every card on it read
  // insufficient_data forever. Links carrying it still exist — every
  // non-default window writes itself into the URL — and they must land
  // somewhere useful rather than on a blank or a dead button.
  it("sends a stale ?window=1h link to the default rather than nowhere", () => {
    window.history.replaceState(null, "", "/?window=1h");
    vi.stubGlobal("fetch", mockFetch());
    render(<App />);
    expect(screen.getByRole("button", { name: "24h" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(
      screen.queryByRole("button", { name: "1h" }),
    ).not.toBeInTheDocument();
  });

  // An unknown window in a shared link must not blank the page.
  it("falls back to the default window for an unknown value", () => {
    window.history.replaceState(null, "", "/?window=6mo");
    vi.stubGlobal("fetch", mockFetch());
    render(<App />);
    expect(screen.getByRole("button", { name: "24h" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });

  // Public from minute one: the page must be readable before any sample
  // exists, and must not look broken.
  it("shows the empty state rather than an error before any data", async () => {
    vi.stubGlobal(
      "fetch",
      mockFetch({
        // An empty `recent` too, because that is what a cold database actually
        // serves: `cycles` is window-scoped and `recent` is not, so a block of
        // cycles with cycles = 0 is not "no data yet" — it is a daemon that
        // stopped longer ago than the window, which the stale branch owns.
        summary: summary({
          cycles: 0,
          models: [],
          net: [],
          faults: {},
          recent: [],
        }),
      }),
    );
    render(<App />);

    await waitFor(() =>
      expect(screen.getByTestId("verdict")).toHaveTextContent(
        /collecting data/i,
      ),
    );
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  // "Loading…" is one line at every width. The headline that replaces it is one
  // line on a desktop and two on a phone, so on a phone the banner grew by a
  // line the moment the fetch landed and carried the whole page down with it.
  // jsdom does no layout, so the reservation is asserted where it is declared.
  it("reserves the headline's second line on a narrow screen", async () => {
    vi.stubGlobal("fetch", mockFetch());
    render(<App />);

    await screen.findByTestId("model-card-mimo-v2.5");
    const banner = screen.getByTestId("verdict");
    expect(banner).toHaveClass("min-h-[99px]");
    // And gives it back where the headline fits on one line, rather than
    // leaving a floor under every desktop banner that ever renders.
    expect(banner).toHaveClass("sm:min-h-0");
  });

  // The page's shape for anyone not reading it with their eyes: one main
  // landmark to jump into, and an outline with no rung missing from it.
  describe("the document outline", () => {
    it("puts the findings in a main landmark, and the masthead outside it", async () => {
      vi.stubGlobal("fetch", mockFetch());
      render(<App />);

      await screen.findByTestId("model-card-mimo-v2.5");
      const main = screen.getByRole("main");
      expect(main).toContainElement(screen.getByTestId("verdict"));
      expect(main).toContainElement(screen.getByTestId("model-card-mimo-v2.5"));
      // A <header> nested in <main> is no longer a banner, which is the one
      // landmark a reader uses to find out what site they are on.
      expect(main).not.toContainElement(
        screen.getByRole("heading", { name: /is xiaomi mimo down\?/i }),
      );
      // And the footer is outside it for the same reason — the other half of
      // the claim, and the one nothing else here would catch.
      expect(main).not.toContainElement(
        screen.getByRole("contentinfo") as HTMLElement,
      );
    });

    it("skips no heading level", async () => {
      vi.stubGlobal("fetch", mockFetch());
      render(<App />);

      await screen.findByTestId("model-card-mimo-v2.5");
      const levels = screen
        .getAllByRole("heading")
        .map((h) => Number(h.tagName[1]));
      expect(levels[0]).toBe(1);
      // Every heading is either the h1 or a level already reached, or exactly
      // one deeper than the deepest so far. The model cards used to be h3 with
      // no h2 above them, which is this assertion's whole reason to exist.
      let deepest = 1;
      for (const level of levels) {
        expect(level).toBeLessThanOrEqual(deepest + 1);
        deepest = Math.max(deepest, level);
      }
    });
  });

  it("surfaces a load failure without blanking the page", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(JSON.stringify({ error: "rate limited" }), {
            status: 429,
          }),
      ),
    );
    render(<App />);

    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent(/rate limited/i),
    );
    // The masthead survives, so the page still explains what it is.
    expect(
      screen.getByRole("heading", { name: /is xiaomi mimo down\?/i }),
    ).toBeInTheDocument();
  });

  // A dashboard that has quietly stopped updating is worse than one that says
  // it cannot load: it shows numbers, and they are wrong. The stream ends for
  // reasons that are not errors — a sleeping laptop, a proxy capping connection
  // age, a daemon restart — so neither of these paths is redundant.
  describe("staying current", () => {
    afterEach(() => {
      vi.useRealTimers();
    });

    // A crawler that renders JavaScript runs the same effect a browser does, so
    // it subscribes too — and then holds an idle connection until its renderer
    // gives up, spending a broker slot and its own render budget on a stream
    // that says nothing. Waiting past the render window means it never opens
    // one. The dashboard still loads, and the interval still covers the
    // cadence, so nothing a reader can see depends on this delay.
    it("does not open the event stream while a crawler would still be looking", async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      const fetchMock = mockFetch();
      vi.stubGlobal("fetch", fetchMock);
      render(<App />);

      const opens = () =>
        fetchMock.mock.calls.filter((c) => String(c[0]).includes("/api/events"))
          .length;

      // The page is up — the dashboard was fetched — and no stream with it.
      await waitFor(() =>
        expect(
          fetchMock.mock.calls.filter((c) =>
            String(c[0]).includes("/api/dashboard"),
          ).length,
        ).toBeGreaterThan(0),
      );
      // Ten seconds of slack, not one: `shouldAdvanceTime` means the waitFor
      // above burns fake clock at real-time rate, and a loaded runner would eat
      // a one-second margin.
      await vi.advanceTimersByTimeAsync(STREAM_OPEN_DELAY_MS - 10000);
      expect(opens()).toBe(0);

      await vi.advanceTimersByTimeAsync(10000);
      await waitFor(() => expect(opens()).toBe(1));
    });

    // The broker replays nothing, so a cycle that completed during the wait is
    // never pushed. Without a refetch on open the page sits on it until the
    // interval's next tick — minutes, on the page that answers "right now".
    it("refetches when the stream finally opens, not just when it delivers", async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      const fetchMock = mockFetch();
      vi.stubGlobal("fetch", fetchMock);
      render(<App />);

      const loads = () =>
        fetchMock.mock.calls.filter((c) =>
          String(c[0]).includes("/api/dashboard"),
        ).length;

      await waitFor(() => expect(loads()).toBe(1));
      await vi.advanceTimersByTimeAsync(STREAM_OPEN_DELAY_MS);
      await waitFor(() => expect(loads()).toBe(2));
    });

    it("reopens the event stream after it drops", async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      const fetchMock = mockFetch({ eventsStatus: 503 });
      vi.stubGlobal("fetch", fetchMock);
      render(<App />);

      const opens = () =>
        fetchMock.mock.calls.filter((c) => String(c[0]).includes("/api/events"))
          .length;

      await vi.advanceTimersByTimeAsync(STREAM_OPEN_DELAY_MS);
      await waitFor(() => expect(opens()).toBe(1));
      // The backoff doubles: first retry at 1s, the next 2s after that. Asserted
      // as two separate advances so a constant-delay retry loop fails here.
      await vi.advanceTimersByTimeAsync(1000);
      expect(opens()).toBe(2);
      await vi.advanceTimersByTimeAsync(2000);
      expect(opens()).toBe(3);
    });

    // A stream that ends the instant it opens must not reset the backoff. It
    // resolves rather than throws, so treating "resolved" as "worked" pins the
    // client at one reconnect per second for as long as the tab is open —
    // invisible on screen, obvious in the server's access log.
    it("backs off even when the stream closes cleanly on open", async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      const fetchMock = mockFetch({ eventsCloseImmediately: true });
      vi.stubGlobal("fetch", fetchMock);
      render(<App />);

      const opens = () =>
        fetchMock.mock.calls.filter((c) => String(c[0]).includes("/api/events"))
          .length;

      await vi.advanceTimersByTimeAsync(STREAM_OPEN_DELAY_MS);
      await waitFor(() => expect(opens()).toBe(1));
      await vi.advanceTimersByTimeAsync(1000);
      expect(opens()).toBe(2);
      // The doubling has to survive a resolving stream too: a second 1s wait
      // must NOT produce a third open.
      await vi.advanceTimersByTimeAsync(1000);
      expect(opens()).toBe(2);
      await vi.advanceTimersByTimeAsync(1000);
      expect(opens()).toBe(3);
    });

    // Every /api/* call spends a token from the per-IP limiter, the bucket
    // holds twenty, and this runs on every cycle and every stream event as well
    // as on every pill click. A load used to cost fifteen — three summaries,
    // five series, the cost panel, a pulse per model and the raw rows for every
    // model-and-probe pair — so two clicks in a row answered "rate limited".
    //
    // An exact count rather than a set: it is the number that was wrong.
    it("asks the daemon once per load, not once per panel", async () => {
      const fetchMock = mockFetch();
      vi.stubGlobal("fetch", fetchMock);
      render(<App />);

      await waitFor(() =>
        expect(screen.getByTestId("verdict")).toHaveTextContent(/normal/i),
      );
      const asked = fetchMock.mock.calls
        .map((c) => String(c[0]))
        .filter((u) => !u.includes("/api/events"));
      expect(asked).toEqual(["/api/dashboard?window=24h"]);
    });

    it("refetches on the probe's cadence even while the stream stays open", async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      const fetchMock = mockFetch();
      vi.stubGlobal("fetch", fetchMock);
      render(<App />);

      const loads = () =>
        fetchMock.mock.calls.filter((c) =>
          String(c[0]).includes("/api/dashboard"),
        ).length;

      await waitFor(() => expect(loads()).toBeGreaterThan(0));
      const before = loads();
      // A stream that stays open but stops delivering looks exactly like one
      // with nothing to say, so the interval has to fire regardless.
      await vi.advanceTimersByTimeAsync(5 * 60 * 1000);
      expect(loads()).toBeGreaterThan(before);
    });
  });
});

describe("the cost panel", () => {
  it("sits last, above the raw cycles", async () => {
    vi.stubGlobal("fetch", mockFetch());
    render(<App />);

    const panel = await screen.findByText("What this dashboard costs to run");
    const raw = screen.getByText("Raw cycles");
    // Node.compareDocumentPosition: FOLLOWING means raw comes after the panel.
    expect(
      panel.compareDocumentPosition(raw) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });
});

describe("the pulse strip", () => {
  const cycle = (model: string, over: Record<string, unknown> = {}) => ({
    model_id: model,
    probe: "short",
    cycles: [
      {
        at: "2026-08-04T11:55:00Z",
        ttft_ms: 900,
        ok: true,
        answer_ok: true,
        error_class: null,
        ...over,
      },
    ],
  });

  // The reason the strip merges both models at all: it is the page's loudest
  // "is anything wrong" surface, and drawing one model let a failure on the
  // other be painted green.
  it("shows a failure on either model", async () => {
    vi.stubGlobal(
      "fetch",
      mockFetch({
        // One group per model, as the daemon sends them. The strip merges
        // them; a page that drew only the first would paint this cycle green.
        pulse: [
          cycle("mimo-v2.5"),
          cycle("mimo-v2.5-pro", {
            ok: false,
            ttft_ms: null,
            error_class: "timeout",
          }),
        ],
      }),
    );
    render(<App />);

    // Waited on a card, not on the strip: the strip is in the document from
    // the first render now — it holds its frame while the fetch is in flight —
    // so awaiting it would no longer gate on the response having landed.
    await screen.findByTestId("model-card-mimo-v2.5");
    const strip = screen.getByTestId("pulse-strip");
    // Both models reported the same cycle, so it is one bar — and the failure
    // on the second model is what it shows.
    expect(strip.children).toHaveLength(1);
    expect(strip).toHaveAttribute(
      "aria-label",
      expect.stringContaining("0 succeeded, 1 failed"),
    );
  });
});
