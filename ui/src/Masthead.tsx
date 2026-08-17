// The header asks the question the site exists to answer, and the verdict
// banner directly below answers it. That pairing is the page's whole shape:
// question, answer, then the numbers the answer rests on.
//
// It names Xiaomi rather than MiMo alone. "mimo" is also the antenna term and
// also a learn-to-code app, and a reader who arrived by typing the question
// needs one word to know they are in the right place. The same sentence is in
// index.html's <title>, its static <h1>, and the og card — reword one, reword
// all four.
//
// The subtitle names the two models. The cards below carry the same two IDs, so
// this is a repeat — but a reader arriving from a search result decides in the
// first screenful whether this page is about the model they run, and the cards
// sit under the verdict banner and the pulse strip. The endpoint stays implicit:
// there is one, and the sentence already says "the API endpoint".
//
// The IDs are hardcoded, and DefaultModels in backend/internal/config/config.go
// is where they actually come from — add or rename a model there and this line
// has to move with it. Not read from the API: the header renders before the
// first fetch resolves, and a subtitle that pops a clause in half a second
// later is worse than one that can go stale on a release we control.
//
// The eyebrow names the two layers the page measures: the inference itself, and
// the network path the request takes to reach it. That is the page's whole
// structure — it is the split the subtitle spells out one line down, and the
// split the decomposition panel draws — so the first thing above the question
// says which two things are being watched.
//
// Inference first, though the subtitle below runs the other way. The subtitle is
// explaining a subtraction and takes the causal order: reach it, then wait on
// it. The eyebrow is naming the subject, and the subject is the model — the
// handshake is measured so it can be subtracted OUT of the inference figure. The
// page is built in this order too: the model cards and all three latency panels
// come before the network panel.
//
// It read "europe → singapore · every few minutes" before, and both halves went
// deliberately. The cadence is a fact about our method rather than about what
// the reader is looking at, and it had the shape of a freshness claim without
// being one — the verdict banner below is what states whether the data is
// current. It still gets said where it does work: beside the per-run price it
// drives (PROBE_CADENCE in CostPanel.tsx). The vantage went with it: an
// arrow needs two ends, and nothing on this page acts on where we measure
// from. The network panel names both ends of the path it draws, which is where
// a reader who wants the geography finds it.
//
// "inference and network", not "inference and API": the API being measured IS
// an inference API, so naming both would be one thing said twice. What actually
// separates is the model from the transport.
//
// The og card carries the same string (assets/og/card.html) and its width is
// load-bearing there — WhatsApp crops to the middle 630px. Re-run
// scripts/gen-og.sh and the measurement in that file's comment after any
// reword.
//
// The subtitle said "the API endpoint in Singapore" and now says only "the API
// endpoint". Once the eyebrow stopped naming the place, this was the last
// mention in the header — and between it, the availability strip and the
// verdict banner the location was said four times over before a reader reached
// a number, which made the page read as if it were about a place rather than
// about MiMo.
//
// Where the reference host sits is still discoverable: NetworkPanel's series
// labels name it, which is the one spot where the place identifies a line on a
// chart rather than colouring prose. Deliberate — do not put it back here.
//
// The bottom padding is deliberately smaller than the top. The verdict banner
// directly below is a statement ABOUT the subtitle here, so it belongs nearer to
// it than to the pulse strip beneath it; with even padding and the banner's own
// margin below, it sat the other way round and read as a caption on the strip.
// The only link off this page. It rides the eyebrow row rather than getting a
// row of its own: that line already says what is being watched, and where the
// numbers come from belongs with it. Aligned right, so it never competes with
// the h1 for the start of the page — and above the fold rather than in a
// footer, because a reader who has just read a bad verdict wants it then, not
// after eight panels.
const CONSOLE_URL = "https://platform.xiaomimimo.com/console";

export function Masthead() {
  return (
    <header className="pt-10 pb-6 sm:pt-16 sm:pb-7">
      {/* Wraps rather than shrinks: below a threshold the strapline and the
          link do not fit on one line, and squeezing them there clipped the
          link's label. Wrapped, it drops to its own line under the strapline
          and the title still starts the page.

          That threshold moves with the strapline's width — it was ~410px at
          "every 5 min" and ~505px once the cadence was spelled out. This string
          is a shade shorter than the one it replaced, so the threshold sits
          just under the old one. Measured, not estimated — the pill is 137px
          with a 24px gap. */}
      <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-3">
        <p className="num text-micro uppercase tracking-[0.22em] text-faint">
          inference and network monitoring
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
      {/* The clamp ceiling drops from 4rem to 3.4rem: the old wordmark was one
          nine-character word, this is four words, and at 4rem it wrapped to
          three ragged lines on a laptop. */}
      {/* The accent sits on the subject, not on the question around it: what a
          reader scanning for "did I land on the right page" needs is the NAME,
          and colouring it puts the answer to that in the first glance. The rest
          of the sentence is grammar.

          Not italic, unlike the accented spans in the prose below. At display
          size the serif italic reads as a citation rather than as emphasis. */}
      <h1 className="mt-3 font-serif text-[clamp(2.1rem,6vw,3.4rem)] font-normal leading-[0.95] tracking-tight text-balance text-ink">
        Is <span className="text-accent-strong">Xiaomi MiMo</span> down?
      </h1>
      {/* No longer "for Xiaomi MiMo": the heading above names it, and repeating
          it one line later was the first thing the eye caught. The "for" that
          is back names the two MODEL IDs, which the heading does not carry —
          the vendor is said once, the scope once, neither twice. Nothing here
          is accented either — the heading carries the one accent in this block,
          and a second one directly under it split the emphasis in two. */}
      {/* The IDs are mono, like every other identifier and figure on the page,
          and a shade smaller than the serif around them — the mono face runs
          wider at the same size and set flush it read as the loudest thing in
          the sentence, which is the heading's job.

          whitespace-nowrap because the hyphen is a break opportunity: at 1280px
          the line broke after it and the first ID read as "mimo-" / "v2.5"
          across two lines, which is a different string. */}
      <p className="mt-4 max-w-[52ch] font-serif text-[clamp(1rem,2vw,1.3rem)] leading-snug text-ink-dim">
        Latency, throughput, availability and answer correctness for{" "}
        <span className="num whitespace-nowrap text-[0.92em]">mimo-v2.5</span>{" "}
        and{" "}
        <span className="num whitespace-nowrap text-[0.92em]">
          mimo-v2.5-pro
        </span>{" "}
        — separating how long it takes to <em>reach</em> the API endpoint from
        what happens once you are there.
      </p>
    </header>
  );
}
