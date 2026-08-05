import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { Figure, OffPeakChip, Pill, StateChip } from "./ui";

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

  // Normal is the resting state of every figure on a card, and the card's own
  // header chip already says it once. Repeating it beside each number adds no
  // information and crowds out the states that do.
  it("does not repeat a normal state beside the number", () => {
    render(<Figure label="TTFT p50" value="916 ms" state="normal" />);
    expect(screen.queryByTestId("state-chip")).not.toBeInTheDocument();
  });

  it("still shows a state that is not normal", () => {
    render(<Figure label="TTFT p50" value="4.2 s" state="degraded" />);
    expect(screen.getByTestId("state-chip")).toHaveTextContent("degraded");
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
