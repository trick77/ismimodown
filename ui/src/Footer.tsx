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
// The disclaimer is ALL of it. A paragraph restating the vantage and the
// cadence was cut: the eyebrow says both, and the panels that depend on the
// single-vantage caveat already carry it where it applies. A footer that
// re-explains the page it sits under is padding around the one sentence that
// has to be here.
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
    </footer>
  );
}
