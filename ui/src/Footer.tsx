import { zoneLabel } from "./format";

// The site is named after someone else's product and measures someone else's
// API, so it says plainly whose it is not. That belongs on the page rather than
// in a policy nobody opens, and it belongs near the numbers it qualifies —
// anyone who read a bad verdict above should be able to see, without leaving,
// that they did not read it from Xiaomi.
//
// Trademarks are named rather than avoided. Calling the thing being measured by
// its name is what makes the page comprehensible; what would misrepresent is
// implying a connection, which is exactly what the first line denies.
//
// Bottom of the page and quiet, because it is a qualifier and not a finding.
// The panels above are the content.
//
// The disclaimer and the zone, and nothing else. A paragraph restating the
// vantage and the cadence was cut and stays cut: the network panel names both
// ends of the path, the cost panel says the cadence, and the
// panels that depend on the single-vantage caveat already carry it where it
// applies. A footer that re-explains the page it sits under is padding.
//
// The zone line earns its place on a different footing. It is not a restatement
// — nothing anywhere else on the page says which clock its times are on — and
// it qualifies every stamp above it: both chart axes, the pulse strip's hover
// titles, the off-peak chip, and the exact instant the raw-cycles table hangs
// on each row's title (the column itself prints a distance, which needs no
// zone — the stamp behind it does). Times follow the reader's browser (see
// format.ts), so this line is the only thing that tells a reader which zone
// they are looking at, and it changes with whoever is reading.
//
// Second, under the disclaimer, and quieter still: whose page this is qualifies
// the whole thing, while this qualifies the numbers.
export function Footer() {
  return (
    <footer className="mt-14 border-t border-border pt-6 text-label text-muted">
      <p className="max-w-[64ch]">
        <strong className="font-medium text-ink-dim">
          An independent project.
        </strong>{" "}
        Not operated by, endorsed by or connected with Xiaomi. “Xiaomi” and
        “MiMo” are trademarks of their respective owner, named here only to
        identify what is measured.
      </p>
      <p className="mt-2 text-faint">
        Times are shown in your browser’s zone —{" "}
        <span className="num">{zoneLabel()}</span>.
      </p>
    </footer>
  );
}
