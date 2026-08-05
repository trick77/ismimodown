import { describe, expect, it } from "vitest";
import { stripMarkupComments, stripRobotsComments } from "./strip-comments";

describe("stripMarkupComments", () => {
  it("removes a comment and the line it sat on", () => {
    const out = stripMarkupComments(
      [
        '<meta charset="utf-8" />',
        "    <!-- a note -->",
        "<title>x</title>",
      ].join("\n"),
    );
    expect(out).toBe('<meta charset="utf-8" />\n<title>x</title>');
  });

  it("removes a multi-line comment", () => {
    expect(stripMarkupComments("<a />\n<!--\n  two\n  lines\n-->\n<b />")).toBe(
      "<a />\n<b />",
    );
  });

  // The greedy-regex trap: everything between two comments must survive.
  it("keeps the markup between two comments", () => {
    const out = stripMarkupComments(
      [
        "<!-- first -->",
        '<meta property="og:image" content="/og.png" />',
        "<!-- second -->",
      ].join("\n"),
    );
    expect(out).toBe('<meta property="og:image" content="/og.png" />\n');
  });

  it("leaves the doctype alone", () => {
    expect(stripMarkupComments("<!doctype html>\n<html></html>")).toBe(
      "<!doctype html>\n<html></html>",
    );
  });

  // The declaration has to stay on line 1 or the document is not well-formed.
  it("leaves an XML declaration first", () => {
    expect(
      stripMarkupComments('<?xml version="1.0"?>\n<!-- why -->\n<urlset />'),
    ).toBe('<?xml version="1.0"?>\n<urlset />');
  });

  it("strips an SVG's leading comment block", () => {
    expect(
      stripMarkupComments(
        "<!--\n  why this file exists\n-->\n<svg><rect /></svg>",
      ),
    ).toBe("<svg><rect /></svg>");
  });

  it("is a no-op on markup with no comments", () => {
    expect(stripMarkupComments("<svg><rect /></svg>")).toBe(
      "<svg><rect /></svg>",
    );
  });
});

describe("stripRobotsComments", () => {
  it("removes whole comment lines and keeps the directives", () => {
    const out = stripRobotsComments(
      [
        "# why this file exists",
        "# and a second line",
        "User-agent: *",
        "Allow: /",
      ].join("\n"),
    );
    expect(out).toBe("User-agent: *\nAllow: /");
  });

  it("leaves a # that is not at the start of a line", () => {
    expect(stripRobotsComments("Sitemap: https://example.com/s.xml#frag")).toBe(
      "Sitemap: https://example.com/s.xml#frag",
    );
  });
});
