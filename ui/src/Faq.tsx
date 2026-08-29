import { FAQ } from "./faq";
import { Card } from "./ui";

// The questions, answered in the page rather than only in its markup.
//
// It sits after every panel and before the footer, which is where a reader who
// still has a question has run out of numbers to read. It is deliberately NOT
// inside the panel grid: everything in there is a measurement, and a card of
// prose set in that rhythm reads as one more finding.
//
// It states nothing about the current state — see faq.ts. The verdict banner is
// the only surface on this page that says what is happening now, and a second
// one that could disagree with it is the failure mode the "one claim per page"
// rule exists to prevent.
//
// Headings rather than a <dl>. A description list is defensible markup for a
// question and its answer, but the questions are what a reader scans for and
// what a search result quotes, and only a heading puts them in the document
// outline where both go looking. h3 under the Card's own h2, so that outline
// stays ordered.
//
// The same six questions are in the static body of index.html, in the same
// order and the same words. That copy is the one a crawler which does not run
// JavaScript sees, this one is what everybody else sees, and Faq.test.tsx holds
// them together.
export function Faq() {
  return (
    <div className="mt-6">
      <Card title="Questions">
        <div className="grid gap-5">
          {FAQ.map((entry) => (
            <div key={entry.question}>
              <h3 className="font-serif text-body font-normal text-ink">
                {entry.question}
              </h3>
              <p className="mt-1 max-w-[68ch] text-label text-muted">
                {entry.answer}
              </p>
            </div>
          ))}
        </div>
      </Card>
    </div>
  );
}
