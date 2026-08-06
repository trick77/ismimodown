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
  fault: FAULT_EDGE,
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

    expect(screen.getByText(/not attributable to MiMo/i)).toBeInTheDocument();
  });

  // The ordinary case: both probes answered, so the failure is the endpoint's
  // and the row reads normally.
  it("leaves an attributable failure unlabelled", () => {
    render(<RecentErrors failures={[failure({ fault: FAULT_OK })]} />);

    expect(screen.queryByText(/not attributable/i)).not.toBeInTheDocument();
    expect(screen.getByText("a non-2xx response")).toBeInTheDocument();
  });

  // A card that vanishes when nothing failed is indistinguishable from a card
  // that broke — and the quiet state is the good news, worth stating.
  it("says so in words when nothing failed", () => {
    render(<RecentErrors failures={[]} />);

    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    expect(screen.getByText(/Nothing failed/)).toBeInTheDocument();
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
