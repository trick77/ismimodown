import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { PANELS, PANEL_COPY } from "../src/copy";
import {
  STATIC_COPY_MARKER,
  escapeHtml,
  injectStaticCopy,
  renderStaticCopy,
} from "./static-copy";
import { stripMarkupComments } from "./strip-comments";

const shell = readFileSync(resolve(__dirname, "../index.html"), "utf8");

describe("renderStaticCopy", () => {
  it("renders every panel's title and subtitle", () => {
    const out = renderStaticCopy();
    for (const panel of PANELS) {
      expect(out).toContain(panel.title);
      expect(out).toContain(panel.subtitle);
    }
  });

  it("gives each panel a second-level heading under the shell's one h1", () => {
    expect(renderStaticCopy().match(/<h2 /g)).toHaveLength(PANELS.length);
    expect(renderStaticCopy()).not.toContain("<h1");
  });

  // The whole point of the plugin. 32 words was the shell before it, and the
  // number is pinned rather than described so a future edit that guts the copy
  // fails here instead of in Bing's index three weeks later.
  it("is substantially more than the heading and lede it supplements", () => {
    const words = renderStaticCopy()
      .replace(/<[^>]*>/g, " ")
      .split(/\s+/)
      .filter(Boolean);
    expect(words.length).toBeGreaterThan(250);
  });

  // No panel copy carries a markup character today; this pins the behaviour for
  // the one that eventually does, since a bare < truncates the served page.
  it("escapes markup characters rather than emitting them raw", () => {
    expect(escapeHtml("a < b & c > d")).toBe("a &lt; b &amp; c &gt; d");
  });

  // The other half of that decision: an apostrophe is not special in a text
  // node, and escaping it would put &#39; in front of the only readers this
  // markup has.
  it("leaves quotes and apostrophes alone", () => {
    expect(escapeHtml(`MiMo's "edge"`)).toBe(`MiMo's "edge"`);
    expect(renderStaticCopy()).toContain("MiMo's edge");
  });
});

describe("injectStaticCopy", () => {
  it("replaces the marker with the copy", () => {
    const out = injectStaticCopy(`<div>${STATIC_COPY_MARKER}</div>`);
    expect(out).not.toContain(STATIC_COPY_MARKER);
    expect(out).toContain(PANEL_COPY.ttft.title);
  });

  // A build that silently shipped the short shell is the failure this whole
  // file exists to prevent, so its absence must be loud.
  it("throws rather than shipping a shell with no marker", () => {
    expect(() => injectStaticCopy("<div></div>")).toThrow(/@static-copy/);
  });

  // String.replace reads these as substitution directives in a replacement
  // STRING: `$&` becomes the marker itself, "$`" and "$'" the shell on either
  // side of it. No copy carries one today, so the bug would have sat inert
  // until some future subtitle mentioned a price or a shell variable — and it
  // would still have produced a plausible-looking page. Each token must land
  // byte for byte.
  it.each(["$&", "$`", "$'", "$$", "$1"])(
    "puts %s in the copy through literally",
    (token) => {
      const out = injectStaticCopy(
        `<p>before</p>${STATIC_COPY_MARKER}<p>after</p>`,
        `<p>a ${token} b</p>`,
      );
      expect(out).toBe(`<p>before</p><p>a ${token} b</p><p>after</p>`);
    },
  );
});

describe("the shell", () => {
  it("carries the marker the build depends on", () => {
    expect(shell).toContain(STATIC_COPY_MARKER);
  });

  // Order matters in vite.config.ts: strip-comments runs post and would eat the
  // marker if static-copy had not already consumed it. This pins the
  // consequence — inject first, and the copy survives the stripper.
  it("keeps the copy once comments are stripped after injection", () => {
    const out = stripMarkupComments(injectStaticCopy(shell));
    expect(out).toContain(PANEL_COPY.ttft.subtitle);
    expect(out).not.toContain("@static-copy");
  });

  // The reverse order, as a demonstration of what the enforce/order settings
  // are actually buying.
  it("loses the marker if comments are stripped first", () => {
    expect(() => injectStaticCopy(stripMarkupComments(shell))).toThrow();
  });
});
