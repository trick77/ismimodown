// The page's state-independent prose, in one place, because it is now rendered
// twice: by the components that import it, and into index.html at build time by
// build/static-copy.ts, for the crawlers that never run the bundle.
//
// That second reader is why this file exists. Stripped of its script tag the
// shell was 32 words — a heading and one sentence — and everything that makes
// the page worth indexing arrived only after React mounted. Google renders
// JavaScript as a matter of course; bingbot does not do it reliably, and
// Microsoft's own guidance is to serve prerendered HTML to it instead. The site
// was indexed by the one and not the other, which is exactly the shape that gap
// takes.
//
// Every string here must stay STATE-INDEPENDENT, for the same reason the shell's
// existing lede and its FAQPage answer are: this file is read at BUILD time, so
// a sentence naming a current latency or a current verdict would be baked into
// the HTML and keep asserting itself through an outage. These say what each
// panel MEASURES, never what it currently reads — true at every moment.
//
// A panel whose copy is not here is simply absent from what a crawler sees.
// That is the right default for anything data-dependent — the verdict banner,
// the model cards, the samples table — and the wrong one for a new explanatory
// subtitle, so put those here rather than inline.

/** Title and subtitle for one panel, as both the component and the shell use them. */
export type PanelCopy = { title: string; subtitle: string };

// Keyed rather than a bare array, so a component names the one it renders and a
// rename is a type error instead of a silently shifted index. Deliberately no
// `id` field: these objects are spread straight into the panel components as
// props, and a stray one would land on the DOM.
export const PANEL_COPY = {
  ttft: {
    title: "Time to first token",
    subtitle:
      "P50 per bucket. Failed runs are excluded — an outage is counted as availability, not as latency. Lower is better.",
  },
  tps: {
    title: "Throughput",
    subtitle:
      "Output tokens per second over the decode window. Higher is better.",
  },
  total: {
    title: "The whole wait",
    subtitle:
      "P50 end-to-end, request sent to last token. Most of the wait is getting to the first token; the gap between this plot and the time-to-first-token plot is what decoding adds. Output caps at 150 tokens, so check a step change here against the throughput plot before calling it a slowdown. Failed runs are excluded. Lower is better.",
  },
  network: {
    title: "The wire itself",
    subtitle:
      "Time to complete the TCP handshake on port 443 — no TLS, no HTTP, no auth, no tokens. Each Xiaomi MiMo edge is paired with an independent reference host in the same city, so a route problem, or an outage on our side, shows up as its own problem and not as MiMo's. Only Singapore serves the inference this page measures; Amsterdam is the same service from another region, for comparison. Lower is better.",
  },
  decomposition: {
    title: "Where the time goes",
    subtitle:
      "Time to first token, split into the measured TCP handshake to MiMo's edge and the remainder. Both halves come from the same 5-minute cycle.",
  },
  cost: {
    title: "What this dashboard costs to run",
    subtitle:
      "Every probe this page sends, priced from the usage MiMo reported on it — both models.",
  },
} as const satisfies Record<string, PanelCopy>;

// PAGE ORDER, not the object's. The shell lays its sections out from this, and a
// crawler reading them in an order the rendered page does not use would be
// describing a document nobody sees. Keep it in step with the panels in App.tsx.
export const PANELS: readonly PanelCopy[] = [
  PANEL_COPY.ttft,
  PANEL_COPY.tps,
  PANEL_COPY.total,
  PANEL_COPY.network,
  PANEL_COPY.decomposition,
  PANEL_COPY.cost,
];

// The footer's disclaimer, split where the component bolds it. It is here
// because it is the one piece of footer copy that says something about the site
// rather than about the reader's browser — the other line names their time zone,
// which is meaningless to something that is not a browser.
export const INDEPENDENCE_LEAD = "An independent project.";
export const INDEPENDENCE_BODY =
  "Not operated by, endorsed by or connected with Xiaomi. “Xiaomi” and “MiMo” are trademarks of their respective owner, named here only to identify what is measured.";
