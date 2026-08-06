// Microsoft Clarity — heatmaps and session replay for the one page this site
// has. What it answers: whether a visitor who lands on "is it down?" reads the
// verdict and leaves, or scrolls into the charts.
//
// Clarity ships its loader as an inline <script> to paste into <head>. That is
// exactly what `script-src 'self'` refuses, and adding 'unsafe-inline' to buy
// one analytics tag would undo the half of the CSP that matters (see
// backend/internal/httpapi/middleware.go). So the snippet lives here instead:
// a same-origin module, bundled into the app's own JS, doing the one thing the
// snippet did — append a <script> pointing at the tag. The tag's own origin is
// the single exception the policy now carries.
//
// The queue shim from the snippet (`window.clarity = () => q.push(...)`) is
// dropped: it exists so page code can call clarity() before the tag loads, and
// nothing here ever calls it.
const PROJECT_ID = "xy577xi4l2";

// Production builds only. A dev server, a `vite preview` and every local `make
// dev` run would otherwise post sessions into the same project as real
// visitors, and the recordings of a developer reloading a page eight times are
// the ones nobody wants to sift through.
export function installClarity(): void {
  if (!import.meta.env.PROD) return;

  const tag = document.createElement("script");
  tag.async = true;
  // ?ref=bwt is Clarity's own install-source marker, carried over verbatim from
  // the snippet the dashboard hands out.
  tag.src = `https://www.clarity.ms/tag/${PROJECT_ID}?ref=bwt`;
  document.head.append(tag);
}
