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

  // Times on this page follow the reader's browser, and this is the only place
  // that says which zone that resolved to. Without it the whole page is stamped
  // in a clock the reader has to guess at.
  it("names the zone its times are shown in", () => {
    // Given
    const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;

    // When
    render(<Footer />);

    // Then
    expect(
      screen.getByText(/times are shown in your browser/i),
    ).toHaveTextContent(zone);
  });

  // The disclaimer and the zone are the whole footer. Anything else that lands
  // here is restating the page above it, which is how a footer turns into
  // padding around the two lines that have to be in it.
  it("carries nothing but the disclaimer and the zone", () => {
    // Given / When
    const { container } = render(<Footer />);

    // Then
    expect(container.querySelectorAll("p")).toHaveLength(2);
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });
});
