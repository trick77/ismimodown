// The header says what the page measures. The scope shows itself — two model
// cards, one endpoint — so it is not also spelled out in prose here.
//
// The bottom padding is deliberately smaller than the top. The verdict banner
// directly below is a statement ABOUT the subtitle here, so it belongs nearer to
// it than to the pulse strip beneath it; with even padding and the banner's own
// margin below, it sat the other way round and read as a caption on the strip.
// The only link off this page. It rides the eyebrow row rather than getting a
// row of its own: that line already carries where and how often, and where the
// numbers come from belongs with it. Aligned right, so it never competes with
// the h1 for the start of the page — and above the fold rather than in a
// footer, because a reader who has just read a bad verdict wants it then, not
// after eight panels.
const CONSOLE_URL = "https://platform.xiaomimimo.com/console";

export function Masthead() {
  return (
    <header className="pt-10 pb-6 sm:pt-16 sm:pb-7">
      {/* Wraps rather than shrinks: below ~380px the strapline and the link do
          not fit on one line, and squeezing them there clipped the link's
          label. Wrapped, it drops to its own line under the strapline and the
          title still starts the page. */}
      <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-3">
        <p className="num text-micro uppercase tracking-[0.22em] text-faint">
          live → singapore · every 5 min
        </p>
        <a
          className="pill pill-link"
          href={CONSOLE_URL}
          target="_blank"
          rel="noopener noreferrer"
        >
          MiMo console
          <span aria-hidden="true">↗</span>
        </a>
      </div>
      <h1 className="mt-3 font-serif text-[clamp(2.4rem,7vw,4rem)] font-normal leading-[0.95] tracking-tight text-ink">
        mimostats
      </h1>
      <p className="mt-4 max-w-[52ch] font-serif text-[clamp(1rem,2vw,1.3rem)] leading-snug text-ink-dim">
        Latency, throughput, availability and answer correctness for{" "}
        <em className="text-accent-strong">Xiaomi MiMo</em> — separating how
        long it takes to <em>reach</em> the endpoint from what happens once you
        are there.
      </p>
    </header>
  );
}
