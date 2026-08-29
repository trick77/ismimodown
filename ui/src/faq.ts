// The page's own answers to the questions people arrive with, in one place.
//
// Three copies read from this list and they are NOT independent prose:
//
//   - Faq.tsx renders them, so a reader who scrolls past the panels finds them
//     and so a crawler that runs JavaScript sees them in the rendered page.
//   - The static body of index.html carries the same text, so a crawler that
//     does NOT run JavaScript sees them too. That is most of what this file
//     exists for: everything else on this page is drawn by an 800 kB bundle
//     from an API response, and a search engine that stops before either sees
//     a heading and one sentence.
//   - The FAQPage JSON-LD in index.html carries them a third time. Structured
//     data is only allowed to state what the page visibly says; before this
//     list existed the markup promised three answers and the page showed one,
//     which is the kind of mismatch a search engine treats as a reason to
//     distrust the whole document rather than as a missing feature.
//
// Faq.test.tsx asserts all three agree, character for character. Reword one and
// the test names the other two — which is the only reason it is safe to keep
// them as three copies rather than templating index.html at build time.
//
// Every answer is STATE-INDEPENDENT and has to stay that way. index.html is
// built once by Vite with no serve-time substitution, so an answer that said
// "Xiaomi MiMo is up" would keep saying it through an outage. These say what
// the page reports, which is true at every moment.
//
// Cadence stays vague on purpose — "every few minutes", never the real
// interval; see AGENTS.md. The model IDs are spelled out because a reader
// searching for one of them is exactly who this text is for; they are
// hardcoded here as they are in Masthead.tsx, so a change to DefaultModels in
// backend/internal/config/config.go has to move both.

/** One question and the page's answer to it. */
export type FaqEntry = {
  question: string;
  answer: string;
};

export const FAQ: readonly FaqEntry[] = [
  {
    question: "Is Xiaomi MiMo down?",
    answer:
      "This page checks the Xiaomi MiMo API periodically and reports whether it is answering right now, alongside latency, throughput and answer correctness.",
  },
  {
    question: "What does this page measure?",
    answer:
      "Availability, time to first token, output throughput and answer correctness for mimo-v2.5 and mimo-v2.5-pro, alongside the network path to the endpoint, so the time spent reaching the API is separated from the time spent waiting on it.",
  },
  {
    question: "How often is Xiaomi MiMo checked?",
    answer:
      "Every few minutes. Each cycle probes both models one request at a time, never concurrently: they share one rate limit, and racing them turns a throttled request into something that publishes as an outage. A sample is recorded even when a request times out.",
  },
  {
    question: "Where is Xiaomi MiMo measured from?",
    answer:
      "A single European egress. Everything here is one vantage point's view of the API: a reader elsewhere can see different latency, and a fault on the path from here is not by itself a fault at the endpoint.",
  },
  {
    question: "What counts as an outage rather than a slow request?",
    answer:
      "A run that fails or is cut off by the timeout is recorded as unavailability, never as a latency reading, so a timed-out request cannot land in a percentile. Excluding those runs truncates the slow tail, so the number excluded is published beside every percentile.",
  },
  {
    question: "Is this site run by Xiaomi?",
    answer:
      "No. This is an independent project, not operated by, endorsed by or connected with Xiaomi. Xiaomi and MiMo are trademarks of their respective owner, named here only to identify what is measured.",
  },
];
