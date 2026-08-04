// The header says what the page measures. The scope shows itself — two model
// cards, one endpoint on the methodology panel — so it is not also spelled out
// in prose here.
export function Masthead() {
  return (
    <header className="py-10 sm:py-16">
      <p className="num text-micro uppercase tracking-[0.22em] text-faint">
        live → singapore · every 5 min
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
    </header>
  );
}
