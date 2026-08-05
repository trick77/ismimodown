// The header says what the page measures. The scope shows itself — two model
// cards, one endpoint — so it is not also spelled out in prose here.
//
// The bottom padding is deliberately smaller than the top. The verdict banner
// directly below is a statement ABOUT the subtitle here, so it belongs nearer to
// it than to the pulse strip beneath it; with even padding and the banner's own
// margin below, it sat the other way round and read as a caption on the strip.
export function Masthead() {
  return (
    <header className="pt-10 pb-6 sm:pt-16 sm:pb-7">
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
