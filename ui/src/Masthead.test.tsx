import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Masthead } from "./Masthead";

describe("Masthead", () => {
  it("says what the page measures", () => {
    // Given / When
    render(<Masthead />);

    // Then
    expect(
      screen.getByRole("heading", { name: /mimostats/i }),
    ).toBeInTheDocument();
    expect(screen.getByText(/europe → singapore/i)).toBeInTheDocument();
  });

  // Both ends of the measured path, in the two places that name them: the
  // eyebrow says where we dial from, the subtitle says what answers and that it
  // is an endpoint rather than where the model runs. A latency figure with only
  // one end named is not interpretable, and the OG card's eyebrow (assets/og/
  // card.html) has to keep saying the same thing — re-run gen-og.sh if this
  // changes.
  it("names the vantage and the endpoint it measures", () => {
    // Given / When
    render(<Masthead />);

    // Then
    expect(
      screen.getByText(/the API endpoint in Singapore/i),
    ).toBeInTheDocument();
  });

  // The one link off this page, and the one place a reader can go do something
  // about a bad verdict. Asserted with target and rel because opening the
  // vendor's console over the dashboard is how a live page gets closed mid
  // outage — and rel="noopener" is what keeps the opened tab off window.opener.
  it("links out to the MiMo console in a new tab", () => {
    // Given / When
    render(<Masthead />);

    // Then
    const link = screen.getByRole("link", { name: /mimo console/i });
    expect(link).toHaveAttribute(
      "href",
      "https://platform.xiaomimimo.com/console",
    );
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", expect.stringContaining("noopener"));
  });
});
