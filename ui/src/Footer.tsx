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
const REPO_URL = "https://github.com/trick77/mimostats";

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
      <p className="mt-2 max-w-[64ch]">
        Every figure on this page is measured from a single European egress,
        every five minutes, against the public API. One vantage point sees one
        path: a fault it reports may be on the route rather than at the
        endpoint, and the network panel above is where that distinction is
        drawn.
      </p>
      <p className="mt-2">
        <a
          className="underline underline-offset-2 hover:text-ink-dim"
          href={REPO_URL}
          target="_blank"
          rel="noopener noreferrer"
        >
          Source on GitHub
        </a>
      </p>
    </footer>
  );
}
