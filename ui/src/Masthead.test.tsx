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
    expect(screen.getByText(/live → singapore/i)).toBeInTheDocument();
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
