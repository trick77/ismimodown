import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { RecentErrors } from "./RecentErrors";
import type { Failure } from "./api/types";
import { FAULT_EDGE, FAULT_OK, FAULT_ROUTE, FAULT_UPLINK } from "./api/types";

// Ages read as a distance from now, so the fixtures are stamped against a
// pinned instant, as in SamplesTable's tests.
const NOW = new Date("2026-08-04T12:00:00Z");

const failure = (over: Partial<Failure> = {}): Failure => ({
  at: "2026-08-04T11:58:00Z",
  model_id: "mimo-v2.5",
  probe: "short",
  error_class: "http_error",
  http_status: 503,
  // null, not false: the call never completed, so there was no answer to
  // grade. The graded-wrong fixture below is the one that carries false.
  answer_ok: null,
  fault: FAULT_EDGE,
  ...over,
});

// A call that came back and was graded wrong: 200, a body, no error class.
// This is the amber bar's row, and everything a failure fixture is not.
const wrongAnswer = (over: Partial<Failure> = {}): Failure =>
  failure({
    error_class: null,
    http_status: 200,
    answer_ok: false,
    fault: FAULT_OK,
    ...over,
  });

const cellsOfFirstRow = () =>
  [...screen.getByRole("table").querySelectorAll("tbody tr td")].map(
    (td) => td.textContent,
  );

describe("RecentErrors", () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.setSystemTime(NOW);
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("draws the model, the probe, the class and the status", () => {
    render(<RecentErrors failures={[failure()]} />);

    const cells = cellsOfFirstRow();
    expect(cells[1]).toBe("mimo-v2.5");
    expect(cells[2]).toBe("short");
    expect(cells[3]).toContain("http_error");
    expect(cells[4]).toBe("503");
  });

  // "ttft_timeout" does not say "accepted, then queued", and that distinction
  // is why the probe classifies at this granularity at all.
  it("glosses the class in the words a reader would use", () => {
    render(
      <RecentErrors failures={[failure({ error_class: "ttft_timeout" })]} />,
    );

    expect(screen.getByText(/queueing/)).toBeInTheDocument();
  });

  // A class this build has not heard of still has to render — the vocabulary
  // is the server's, and a missing gloss is not a missing row.
  it("renders an unknown class without a gloss", () => {
    render(
      <RecentErrors failures={[failure({ error_class: "quantum_flux" })]} />,
    );

    expect(screen.getByText("quantum_flux")).toBeInTheDocument();
  });

  // A transport failure never reached a status. Printing 0 would read as one.
  it("dashes the status when the run never got one", () => {
    render(
      <RecentErrors
        failures={[
          failure({ error_class: "connect_timeout", http_status: null }),
        ]}
      />,
    );

    const cells = cellsOfFirstRow();
    expect(cells[4]).toBe("—");
  });

  // The upstream's own words never reach this card — the type has no field for
  // them, and this is what fails if one is ever added.
  it("has no column for what the endpoint said", () => {
    render(<RecentErrors failures={[failure()]} />);

    const headers = [...screen.getAllByRole("columnheader")].map(
      (th) => th.textContent,
    );
    expect(headers).toEqual(["When", "Model", "Probe", "Error", "Status"]);
  });

  // The availability arithmetic drops these rows because it is computing a
  // claim about MiMo. This card is reporting what happened, so it keeps them —
  // going quiet during a network outage would hide the incident.
  it("lists a failure from an unattributable cycle rather than dropping it", () => {
    render(
      <RecentErrors
        failures={[failure({ fault: FAULT_UPLINK, error_class: "timeout" })]}
      />,
    );

    expect(screen.getByRole("table")).toBeInTheDocument();
    expect(screen.getByText("timeout")).toBeInTheDocument();
  });

  // ...but it must never read as MiMo's failure.
  it("labels an unattributable failure as ours", () => {
    render(<RecentErrors failures={[failure({ fault: FAULT_UPLINK })]} />);

    expect(screen.getByText(/not attributable to MiMo/i)).toBeInTheDocument();
    // The gloss belongs to the error class and would claim the endpoint did
    // something; the attribution replaces it.
    expect(screen.queryByText(/a non-2xx response/)).not.toBeInTheDocument();
  });

  // route is no longer produced, but stored cycles carry it, and handling only
  // uplink would silently misread them as MiMo's.
  it("treats the historical route fault the same way", () => {
    render(<RecentErrors failures={[failure({ fault: FAULT_ROUTE })]} />);

    // Named as the layer it was attributed to: a route cycle is the path
    // between here and the far end, which is neither our uplink nor MiMo's.
    // The same words the verdict banner uses — never "the endpoint", which is
    // MiMo's API everywhere else on the page.
    expect(screen.getByText(/route to the far end/i)).toBeInTheDocument();
    expect(screen.getByText(/not attributable to MiMo/i)).toBeInTheDocument();
  });

  // The ordinary case: both probes answered, so the failure is the endpoint's
  // and the row reads normally.
  it("leaves an attributable failure unlabelled", () => {
    render(<RecentErrors failures={[failure({ fault: FAULT_OK })]} />);

    expect(screen.queryByText(/not attributable/i)).not.toBeInTheDocument();
    expect(screen.getByText("a non-2xx response")).toBeInTheDocument();
  });

  // The amber bar's destination. A graded-wrong run is the other way a run
  // goes wrong, and before this the card had nothing for it.
  it("lists a graded-wrong answer and names it", () => {
    render(<RecentErrors failures={[wrongAnswer()]} />);

    const cells = cellsOfFirstRow();
    expect(cells[3]).toContain("wrong_answer");
    expect(screen.getByText(/the answer did not/)).toBeInTheDocument();
  });

  // Red on this column means "MiMo failed", and that is the one thing a run
  // that returned 200 cannot claim. Amber is the token the pulse strip paints
  // the same cycle with, so the bar and the row read as one event.
  it("draws a graded-wrong answer amber, never in the failure colour", () => {
    render(<RecentErrors failures={[wrongAnswer()]} />);

    const label = screen.getByText("wrong_answer");
    expect(label).toHaveClass("text-fault-edge");
    expect(label).not.toHaveClass("text-danger");
  });

  // It worked, and it was still wrong — which is the whole row in one cell. A
  // dash here would claim it never reached the endpoint.
  it("prints the real status on a graded-wrong answer", () => {
    render(<RecentErrors failures={[wrongAnswer()]} />);

    const cells = cellsOfFirstRow();
    expect(cells[4]).toBe("200");
  });

  // One block, one timeline: the two kinds interleave in the order the server
  // sent them rather than sorting into groups.
  it("carries failures and wrong answers in the same list", () => {
    render(
      <RecentErrors
        failures={[
          wrongAnswer({ at: "2026-08-04T11:59:00Z" }),
          failure({ at: "2026-08-04T11:00:00Z" }),
        ]}
      />,
    );

    expect(screen.getByText("wrong_answer")).toBeInTheDocument();
    expect(screen.getByText("http_error")).toBeInTheDocument();
  });

  // A run that failed during our own outage is still not MiMo's, and that
  // claim outranks anything the grader would have said.
  it("keeps the attribution label ahead of the wrong-answer label", () => {
    render(<RecentErrors failures={[wrongAnswer({ fault: FAULT_UPLINK })]} />);

    expect(screen.getByText(/not attributable to MiMo/i)).toBeInTheDocument();
    expect(screen.queryByText("wrong_answer")).not.toBeInTheDocument();
  });

  // A card that vanishes when nothing went wrong is indistinguishable from a
  // card that broke — and the quiet state is the good news, worth stating. It
  // has to cover both kinds, or it claims a clean day on a day with three
  // wrong answers in it.
  it("says so in words when nothing went wrong", () => {
    render(<RecentErrors failures={[]} />);

    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    expect(screen.getByText(/Nothing failed/)).toBeInTheDocument();
    expect(screen.getByText(/nothing was answered wrong/)).toBeInTheDocument();
  });

  // Before the first response lands there is no evidence of anything, and a
  // card claiming a clean day on a page that has not asked yet is the one
  // sentence this must never print.
  it("does not claim a clean day before anything has answered", () => {
    render(<RecentErrors failures={null} />);

    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    expect(screen.queryByText(/Nothing failed/)).not.toBeInTheDocument();
    expect(screen.getByText(/Not enough data yet/)).toBeInTheDocument();
  });

  // The server orders the block; the card must not reorder it, or the row a
  // reader reads as "the latest" is not the latest.
  it("draws the failures in the order they arrive", () => {
    render(
      <RecentErrors
        failures={[
          failure({ at: "2026-08-04T11:59:00Z", http_status: 503 }),
          failure({ at: "2026-08-04T11:00:00Z", http_status: 429 }),
        ]}
      />,
    );

    const statuses = [
      ...screen.getByRole("table").querySelectorAll("tbody tr td:last-child"),
    ].map((td) => td.textContent);
    expect(statuses).toEqual(["503", "429"]);
  });
});
