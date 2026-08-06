import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Masthead } from "./Masthead";

describe("Masthead", () => {
  // The heading is the question the site is named after, and it names Xiaomi
  // rather than MiMo alone — "mimo" on its own belongs to an antenna technique
  // and to a learn-to-code app. The same string is in index.html's <title>, its
  // static body and the OG card; this asserts the one a reader sees first.
  it("asks the question the page answers", () => {
    // Given / When
    render(<Masthead />);

    // Then
    expect(
      screen.getByRole("heading", { name: /is xiaomi mimo down\?/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/inference and network monitoring/i),
    ).toBeInTheDocument();
  });

  // The two layers, in the two places that name them: the eyebrow says which
  // two things are watched, the subtitle says the endpoint is a place we dial
  // rather than where the model runs. The OG card's eyebrow (assets/og/
  // card.html) has to keep saying the same thing — re-run gen-og.sh if this
  // changes.
  it("names both layers and the endpoint it measures", () => {
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
