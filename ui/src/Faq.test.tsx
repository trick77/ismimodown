import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { Faq } from "./Faq";
import { Masthead } from "./Masthead";
import { FAQ } from "./faq";

// The answers live in three places on purpose — faq.ts for the app, the static
// body of index.html for a crawler that does not run JavaScript, and the
// FAQPage JSON-LD for one that reads markup. Nothing in the build keeps them
// in step, so this does: reword one and the assertions below name the other
// two.
//
// It reads index.html off disk rather than a built artefact. The source is what
// an editor changes and what review sees; the build only strips comments from
// it, so anything true here is true of what ships.

// import.meta.url is not a file: URL under the jsdom environment, so the path
// is resolved from the package root instead — which is where every npm script
// and the Makefile run vitest from.
const indexHtml = readFileSync(resolve(process.cwd(), "index.html"), "utf8");

/** Collapses runs of whitespace, so prettier's line wrapping is not a diff. */
function normalise(text: string): string {
  return text.replace(/\s+/g, " ").trim();
}

/** The static markup React replaces on mount, as plain text. */
function staticBodyText(): string {
  const body = indexHtml.slice(indexHtml.indexOf("<body>"));
  return normalise(
    body
      // Comments first: they name the very strings asserted below, and left in
      // they would satisfy every check here without a word reaching a reader.
      .replace(/<!--[\s\S]*?-->/g, " ")
      .replace(/<[^>]*>/g, " "),
  );
}

/** The mainEntity of the FAQPage node in the page's JSON-LD. */
function markupEntries(): { question: string; answer: string }[] {
  const json = indexHtml.match(
    /<script type="application\/ld\+json">([\s\S]*?)<\/script>/,
  );
  const payload = json?.[1];
  expect(payload).toBeTypeOf("string");
  const graph = JSON.parse(payload as string) as {
    "@graph": { "@type": string; mainEntity?: unknown[] }[];
  };
  const faq = graph["@graph"].find((node) => node["@type"] === "FAQPage");
  expect(faq).toBeDefined();
  return (
    faq!.mainEntity as {
      name: string;
      acceptedAnswer: { text: string };
    }[]
  ).map((entry) => ({
    question: entry.name,
    answer: entry.acceptedAnswer.text,
  }));
}

describe("Faq", () => {
  it("renders every question as a heading with its answer", () => {
    render(<Faq />);
    for (const { question, answer } of FAQ) {
      expect(
        screen.getByRole("heading", { name: question, level: 3 }),
      ).toBeInTheDocument();
      expect(screen.getByText(answer)).toBeInTheDocument();
    }
  });

  it("says nothing about the current state", () => {
    render(<Faq />);
    // The verdict banner is the only surface allowed to state a state. An
    // answer baked into a static file cannot know one, so a word from that
    // vocabulary here is a claim that would keep being made through an outage.
    const text = document.body.textContent ?? "";
    for (const word of ["is up", "is down", "degraded", "operational"]) {
      expect(text.toLowerCase()).not.toContain(word);
    }
  });
});

describe("index.html", () => {
  it("carries the same answers in its static body", () => {
    const text = staticBodyText();
    for (const { question, answer } of FAQ) {
      expect(text).toContain(question);
      expect(text).toContain(answer);
    }
  });

  it("marks up the same answers, in the same order, as the FAQPage", () => {
    expect(markupEntries()).toEqual(
      FAQ.map(({ question, answer }) => ({ question, answer })),
    );
  });

  it("opens with the question the FAQ answers first", () => {
    // The site is named after one question, and it is written out in five
    // places: the <h1> Masthead renders, the <title>, the static <h1> in
    // index.html, the og card, and the first FAQ entry. Two of those are
    // pictures or prose no test can reach; these three are not.
    const [first] = FAQ;
    expect(first).toBeDefined();

    render(<Masthead />);
    const h1 = screen.getByRole("heading", { level: 1 });
    expect(normalise(h1.textContent ?? "")).toBe(first!.question);
    expect(indexHtml).toContain(`<title>${first!.question}`);
  });

  it("asks a search engine to index the page", () => {
    // A page that is crawled and left out of the index is the failure this
    // whole file guards; an accidental noindex would be the loudest possible
    // version of it.
    expect(indexHtml).toMatch(/<meta\s+name="robots"[\s\S]*?content="index,/);
    expect(indexHtml).not.toMatch(/content="[^"]*noindex/);
  });
});
