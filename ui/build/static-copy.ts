// Renders the page's explanatory copy into index.html at build time, for the
// readers that never run the bundle.
//
// The shell serves two audiences. A browser sees this markup for the moment
// before React mounts and then never again; a crawler that does not execute
// JavaScript sees it and nothing else, forever. Before this plugin that second
// audience got 32 words — the <h1> and one sentence — while the verdict, the
// panels and every caption explaining what is measured lived only in the
// bundle. Google renders JS and indexed the site; bingbot renders it
// unreliably, and did not.
//
// Injected here rather than written into index.html by hand because the same
// sentences are already props on the components. src/copy.ts holds them once;
// this reads that module and lays it out. Writing them into the shell instead
// would make it a fifth place the page's copy has to be kept in sync — the
// failure index.html's own comments spend a paragraph warning about.
//
// React REPLACES this on mount: main.tsx uses createRoot, not hydrateRoot, so
// nothing here has to match what the app renders and no reader ever sees both.
// That is load-bearing — switching to hydrateRoot would render this markup and
// the app's as one tree, and they do not match.
//
// Deliberately NOT a <noscript>: a crawler that does not run scripts also does
// not honour <noscript>, and one that does run them would have replaced this
// already. Plain markup inside #root is what both read.
import type { Plugin } from "vite";

import {
  INDEPENDENCE_BODY,
  INDEPENDENCE_LEAD,
  PANELS,
  type PanelCopy,
} from "../src/copy.ts";

// The marker sits in index.html where the sections belong. A comment rather than
// an empty element: `npm run dev` does not run this plugin (apply: "build"), and
// a comment leaves nothing behind in the dev shell, where an unfilled <div>
// would be a stray box in the one view the author actually looks at.
//
// strip-comments.ts would remove it — that plugin is enforce: "post", this one
// is "pre", so the marker is consumed before it can be. If this ever stops
// firing, the symptom is a build that silently ships the old 32-word shell,
// which is why the plugin throws rather than skipping when the marker is gone.
export const STATIC_COPY_MARKER = "<!-- @static-copy -->";

// Only the three characters that are special in TEXT. Quotes and apostrophes
// need escaping inside an attribute value, and every string here goes into a
// text node — escaping them there is not safer, only wrong-looking: it turns
// "MiMo's edge" into "MiMo&#39;s edge" in the one view this markup exists for,
// which is a crawler's, and View Source.
//
// Nothing interpolates an attribute from copy.ts. If something ever does it
// needs its own escaper, rather than a wider version of this one.
/** Escapes the three characters that cannot appear literally in HTML text. */
export function escapeHtml(text: string): string {
  return text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

// Inline styles, like the <h1> and lede above the marker, and for the same
// reason: this markup only has to survive the moment before mount, when the
// stylesheet may not have landed. Muted and small — it is under a heading a
// reader is about to see replaced, so it must not flash as the loudest thing on
// the page.
const SECTION_STYLE = "margin: 2.5rem 0 0";
const HEADING_STYLE = [
  "font-family: Georgia, 'Times New Roman', serif",
  "font-weight: 400",
  "font-size: 1.35rem",
  "line-height: 1.2",
  "color: #faf9f5",
  "margin: 0",
].join("; ");
const BODY_STYLE = [
  "font-family: Georgia, 'Times New Roman', serif",
  "font-size: 1rem",
  "line-height: 1.45",
  "color: #9c9a92",
  "margin: 0.6rem 0 0",
].join("; ");

// <h2>, not <h3> or a <p> in bold: these are the page's second-level sections
// under the one <h1>, and a crawler reading the outline is the whole point of
// serving them. Skipping a level, or serving six headings with no rank at all,
// describes a flatter document than the page actually is.
function renderPanel(panel: PanelCopy): string {
  return [
    `<section style="${SECTION_STYLE}">`,
    `<h2 style="${HEADING_STYLE}">${escapeHtml(panel.title)}</h2>`,
    `<p style="${BODY_STYLE}">${escapeHtml(panel.subtitle)}</p>`,
    `</section>`,
  ].join("");
}

/** The markup that replaces the marker: one section per panel, then the disclaimer. */
export function renderStaticCopy(): string {
  const panels = PANELS.map(renderPanel).join("");
  const independence =
    `<p style="${BODY_STYLE}">` +
    `<strong style="font-weight: 400; color: #faf9f5">${escapeHtml(INDEPENDENCE_LEAD)}</strong> ` +
    `${escapeHtml(INDEPENDENCE_BODY)}</p>`;
  return `${panels}<section style="${SECTION_STYLE}">${independence}</section>`;
}

/** Replaces the marker in a shell with the rendered copy. Throws if it is absent. */
export function injectStaticCopy(html: string): string {
  if (!html.includes(STATIC_COPY_MARKER)) {
    throw new Error(
      `static-copy: ${STATIC_COPY_MARKER} not found in index.html — the shell would ship without the copy a non-rendering crawler reads`,
    );
  }
  return html.replace(STATIC_COPY_MARKER, renderStaticCopy());
}

export function staticCopyPlugin(): Plugin {
  return {
    name: "ismimodown:static-copy",
    // Dev serves the shell for a browser that is about to run the bundle
    // anyway, and the marker comment is more honest there than a wall of copy
    // the author has to scroll past in the elements panel.
    apply: "build",
    // Before strip-comments, which is "post" and would otherwise eat the marker
    // out from under this.
    enforce: "pre",
    transformIndexHtml: {
      order: "pre",
      handler: (html: string) => injectStaticCopy(html),
    },
  };
}
