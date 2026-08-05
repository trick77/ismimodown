import type { Verdict } from "./verdict";
import { StateChip } from "./ui";

// The verdict sits under the masthead and says what is happening in words,
// before any number appears. "Is 996 ms good?" is unanswerable in the abstract.
export function VerdictBanner({
  verdict,
  loading,
}: {
  verdict: Verdict;
  loading: boolean;
}) {
  const tone =
    verdict.state === "degraded"
      ? "border-danger/40 bg-danger/8"
      : verdict.state === "elevated"
        ? "border-fault-edge/40 bg-fault-edge/8"
        : "border-border bg-panel/60";

  return (
    <div
      // mb-10, against the masthead's tighter pb above: the banner reads as a
      // verdict on the page, not as a label for the strip it happens to sit on
      // top of, and it needs the larger gap on the side it is NOT about.
      //
      // min-h holds two lines of headline, but only on a narrow screen and only
      // because that is where the headline needs two. "Loading…" is one line
      // everywhere; "Everything looks normal right now" is one line on a
      // desktop and two on a phone, so on a phone the banner grew by a line the
      // moment the fetch landed and carried the entire page down with it. Above
      // the sm breakpoint there is nothing to reserve, so nothing is: a fixed
      // floor there would be dead space under every headline that ever renders.
      className={`mb-10 min-h-[99px] rounded-xl border px-5 py-4 sm:min-h-0 ${tone}`}
      role="status"
      aria-live="polite"
      data-testid="verdict"
    >
      <div className="flex flex-wrap items-center gap-3">
        <StateChip state={verdict.state} />
        <p className="font-serif text-title text-ink">
          {loading && verdict.state === "unknown"
            ? "Loading…"
            : verdict.headline}
        </p>
      </div>
      {verdict.detail.length > 0 && (
        <ul className="mt-2 space-y-1 text-label text-muted">
          {verdict.detail.map((line) => (
            <li key={line}>{line}</li>
          ))}
        </ul>
      )}
    </div>
  );
}
