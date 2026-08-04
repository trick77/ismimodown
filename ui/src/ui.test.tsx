import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { Figure, OffPeakChip, OffPeakNote, Pill, StateChip } from "./ui";

describe("StateChip", () => {
  // Colour is never the only signal: the state word must be present so the
  // dashboard stays readable to a colour-blind reader and in a greyscale
  // screenshot.
  it("carries the state as a word, not only as a colour", () => {
    render(<StateChip state="degraded" />);
    expect(screen.getByTestId("state-chip")).toHaveTextContent("degraded");
  });

  it("names the unknown state rather than rendering blank", () => {
    render(<StateChip state="unknown" />);
    expect(screen.getByTestId("state-chip")).toHaveTextContent("no data");
  });
});

describe("Figure", () => {
  it("renders a sufficient value", () => {
    render(<Figure label="TTFT p50" value="916 ms" n={288} />);
    expect(screen.getByText("916 ms")).toBeInTheDocument();
  });

  // Below the sample threshold the UI must say so in words. Rendering 0 would
  // draw a floor that does not exist, and that is the figure that gets
  // screenshotted out of context.
  it("says insufficient data instead of showing a number", () => {
    render(<Figure label="TTFT p50" value="0 ms" sufficient={false} n={3} />);
    expect(screen.getByTestId("insufficient")).toHaveTextContent(
      /insufficient data/i,
    );
    expect(screen.queryByText("0 ms")).not.toBeInTheDocument();
    // The sample count is still shown, so a reader knows how close it is.
    expect(screen.getByTestId("insufficient")).toHaveTextContent("3");
  });
});

describe("OffPeakNote", () => {
  it("renders nothing when no band was drawn", () => {
    const { container } = render(<OffPeakNote spans={[]} />);
    expect(container.firstChild).toBeNull();
  });

  // The hours come off the band's OWN edges, so a window either side of the DST
  // changeover reports what it actually painted.
  it("quotes the local hours of the band it was given", () => {
    render(
      <OffPeakNote
        spans={[[Date.UTC(2026, 7, 4, 16), Date.UTC(2026, 7, 5, 0)]]}
      />,
    );
    expect(screen.getByText(/18:00/)).toBeInTheDocument();
    expect(screen.getByText(/02:00/)).toBeInTheDocument();
  });

  it("uses the winter hours for a winter band", () => {
    render(
      <OffPeakNote
        spans={[[Date.UTC(2026, 0, 4, 16), Date.UTC(2026, 0, 5, 0)]]}
      />,
    );
    expect(screen.getByText(/17:00/)).toBeInTheDocument();
  });

  // MiMo publishes a price and nothing about load. A note implying these are
  // the quiet hours would invent a claim this page exists to avoid.
  it("says it is a price and not a forecast", () => {
    const { container } = render(
      <OffPeakNote
        spans={[[Date.UTC(2026, 7, 4, 16), Date.UTC(2026, 7, 5, 0)]]}
      />,
    );
    expect(container.textContent).toMatch(/bills/i);
    expect(container.textContent).toMatch(/not a forecast/i);
  });
});

describe("OffPeakChip", () => {
  // A countdown would be stale for most of the five minutes it is on screen.
  // The boundary is an instant, and it is either past or it is not.
  it("names the end of the window while the rate is live", () => {
    render(<OffPeakChip now={Date.UTC(2026, 7, 4, 20)} />);
    expect(screen.getByText(/until/)).toHaveTextContent("02:00");
  });

  it("names the start of the next one while it is not", () => {
    render(<OffPeakChip now={Date.UTC(2026, 7, 4, 9)} />);
    expect(screen.getByText(/from/)).toHaveTextContent("18:00");
  });
});

describe("Pill", () => {
  it("reports its pressed state to assistive tech and fires on click", async () => {
    const onClick = vi.fn();
    render(
      <Pill active onClick={onClick}>
        24h
      </Pill>,
    );
    const button = screen.getByRole("button", { name: "24h" });
    expect(button).toHaveAttribute("aria-pressed", "true");
    await userEvent.click(button);
    expect(onClick).toHaveBeenCalledOnce();
  });
});
