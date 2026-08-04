import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";

const summary = (over: Record<string, unknown> = {}) => ({
  window: "24h",
  cycles: 288,
  models: [
    {
      model_id: "mimo-v2.5",
      probe: "infer",
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
  skipped_runs: 0,
  generated_at: "2026-08-04T12:00:00Z",
  ...over,
});

const emptySeries = {
  window: "24h",
  bucket_s: 900,
  metric: "ttft",
  probe: "infer",
  models: {},
};
const emptyNet = {
  window: "24h",
  bucket_s: 900,
  metric: "network",
  targets: {},
};

function mockFetch(overrides: Record<string, unknown> = {}) {
  return vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    const body = url.includes("/api/summary")
      ? (overrides.summary ?? summary())
      : url.includes("metric=network")
        ? emptyNet
        : url.includes("/api/series")
          ? emptySeries
          : url.includes("/api/samples")
            ? { model_id: "mimo-v2.5", probe: "infer", samples: [] }
            : url.includes("/api/methodology")
              ? {
                  scope:
                    "Latency of mimo-v2.5 and mimo-v2.5-pro, measured from one host.",
                }
              : {};
    if (url.includes("/api/events")) {
      // eventsStatus makes the stream FAIL instead, which is what the reconnect
      // tests need — streamSSE throws on a non-OK response.
      if (overrides.eventsStatus) {
        return new Response("", { status: Number(overrides.eventsStatus) });
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

  // The scope is published, not implied: it comes from the daemon via
  // /api/methodology, so the page cannot claim a scope the backend is not
  // actually measuring. Async because it is fetched, not hardcoded.
  it("publishes the scope on the methodology panel", async () => {
    vi.stubGlobal("fetch", mockFetch());
    render(<App />);
    expect(
      await screen.findByText(/measured from one host/i),
    ).toBeInTheDocument();
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

  // Public from minute one: the methodology must be readable before any sample
  // exists, and the page must not look broken.
  it("shows the empty state rather than an error before any data", async () => {
    vi.stubGlobal(
      "fetch",
      mockFetch({
        summary: summary({ cycles: 0, models: [], net: [], faults: {} }),
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
    expect(screen.getByText(/mimostats/i)).toBeInTheDocument();
  });

  // A dashboard that has quietly stopped updating is worse than one that says
  // it cannot load: it shows numbers, and they are wrong. The stream ends for
  // reasons that are not errors — a sleeping laptop, a proxy capping connection
  // age, a daemon restart — so neither of these paths is redundant.
  describe("staying current", () => {
    afterEach(() => {
      vi.useRealTimers();
    });

    it("reopens the event stream after it drops", async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      const fetchMock = mockFetch({ eventsStatus: 503 });
      vi.stubGlobal("fetch", fetchMock);
      render(<App />);

      const opens = () =>
        fetchMock.mock.calls.filter((c) => String(c[0]).includes("/api/events"))
          .length;

      await waitFor(() => expect(opens()).toBe(1));
      // The backoff doubles: first retry at 1s, the next 2s after that. Asserted
      // as two separate advances so a constant-delay retry loop fails here.
      await vi.advanceTimersByTimeAsync(1000);
      expect(opens()).toBe(2);
      await vi.advanceTimersByTimeAsync(2000);
      expect(opens()).toBe(3);
    });

    it("refetches on the probe's cadence even while the stream stays open", async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      const fetchMock = mockFetch();
      vi.stubGlobal("fetch", fetchMock);
      render(<App />);

      const loads = () =>
        fetchMock.mock.calls.filter((c) => String(c[0]).includes("/api/summary"))
          .length;

      await waitFor(() => expect(loads()).toBeGreaterThan(0));
      const before = loads();
      // A stream that stays open but stops delivering looks exactly like one
      // with nothing to say, so the interval has to fire regardless.
      await vi.advanceTimersByTimeAsync(5 * 60 * 1000);
      expect(loads()).toBeGreaterThan(before);
    });
  });
});
