import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Footer } from "./Footer";

describe("Footer", () => {
  // The site is named after Xiaomi's product and measures Xiaomi's API. The
  // denial of any connection is the reason this component exists, so it is
  // asserted rather than left to survive a future tidy-up of "filler" copy.
  it("denies any connection with Xiaomi", () => {
    // Given / When
    render(<Footer />);

    // Then
    expect(screen.getByText(/an independent project/i)).toBeInTheDocument();
    expect(
      screen.getByText(
        /not operated by, endorsed by or connected with Xiaomi/i,
      ),
    ).toBeInTheDocument();
  });

  // Naming the marks as someone else's is the other half of the disclaimer:
  // the first line says what this is not, this one says whose the words are.
  it("attributes the trademarks", () => {
    // Given / When
    render(<Footer />);

    // Then
    expect(
      screen.getByText(/trademarks of their respective owner/i),
    ).toBeInTheDocument();
  });

  // A single vantage point is a real limit on every number above, and the
  // footer is where it is stated plainly rather than inferred from a chart.
  it("says the measurements come from one vantage point", () => {
    // Given / When
    render(<Footer />);

    // Then
    expect(screen.getByText(/single European egress/i)).toBeInTheDocument();
  });

  it("links to the source in a new tab", () => {
    // Given / When
    render(<Footer />);

    // Then
    const link = screen.getByRole("link", { name: /source on github/i });
    expect(link).toHaveAttribute(
      "href",
      "https://github.com/trick77/mimostats",
    );
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", expect.stringContaining("noopener"));
  });
});
