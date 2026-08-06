import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { RecentErrors } from "./RecentErrors";
import type { Failure } from "./api/types";

// Ages read as a distance from now, so the fixtures are stamped against a
// pinned instant, as in SamplesTable's tests.
const NOW = new Date("2026-08-04T12:00:00Z");

const failure = (over: Partial<Failure> = {}): Failure => ({
  at: "2026-08-04T11:58:00Z",
  model_id: "mimo-v2.5",
  probe: "short",
  error_class: "http_error",
  http_status: 503,
  error_detail: "upstream connect error",
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

  it("draws the class, the status and what the endpoint said", () => {
    render(<RecentErrors failures={[failure()]} />);

    const cells = cellsOfFirstRow();
    expect(cells[1]).toBe("mimo-v2.5");
    expect(cells[2]).toBe("short");
    expect(cells[3]).toContain("http_error");
    expect(cells[4]).toBe("503");
    expect(cells[5]).toBe("upstream connect error");
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
          failure({
            error_class: "connect_timeout",
            http_status: null,
            error_detail: "",
          }),
        ]}
      />,
    );

    const cells = cellsOfFirstRow();
    expect(cells[4]).toBe("—");
    expect(cells[5]).toBe("—");
  });

  // A card that vanishes when nothing failed is indistinguishable from a card
  // that broke — and the quiet state is the good news, worth stating.
  it("says so in words when nothing failed", () => {
    render(<RecentErrors failures={[]} />);

    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    expect(screen.getByText(/Nothing failed/)).toBeInTheDocument();
  });

  // The server orders the block; the card must not reorder it, or the row a
  // reader reads as "the latest" is not the latest.
  it("draws the failures in the order they arrive", () => {
    render(
      <RecentErrors
        failures={[
          failure({ at: "2026-08-04T11:59:00Z", error_detail: "newest" }),
          failure({ at: "2026-08-04T11:00:00Z", error_detail: "older" }),
        ]}
      />,
    );

    const details = [
      ...screen.getByRole("table").querySelectorAll("tbody tr td:last-child"),
    ].map((td) => td.textContent);
    expect(details).toEqual(["newest", "older"]);
  });
});
