// The header states the scope honestly, because the name does not.
//
// Two models from one vendor, measured from one place. Anyone screenshotting a
// number off this page should be able to see both limits without scrolling.
export function Masthead({ origin }: { origin: string }) {
  return (
    <header className="py-10 sm:py-16">
      <p className="num text-micro uppercase tracking-[0.22em] text-faint">
        live · {origin} → singapore · every 5 min
      </p>
      <h1 className="mt-3 font-serif text-[clamp(2.4rem,7vw,4rem)] font-normal leading-[0.95] tracking-tight text-ink">
        mimostats
      </h1>
      <p className="mt-4 max-w-[52ch] font-serif text-[clamp(1rem,2vw,1.3rem)] leading-snug text-ink-dim">
        A latency monitor for{" "}
        <em className="text-accent-strong">Xiaomi MiMo</em>, separating how long
        it takes to <em>reach</em> the endpoint from what happens once you are
        there.
      </p>
      <p className="mt-3 max-w-[62ch] text-label text-muted">
        Two models, one vendor — this is a MiMo monitor, not a cross-vendor
        benchmark. mimo-v2.5 and mimo-v2.5-pro are different weight classes, so
        latency between them is comparable and quality is not.
      </p>
    </header>
  );
}
