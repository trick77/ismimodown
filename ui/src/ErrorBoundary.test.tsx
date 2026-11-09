import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ErrorBoundary } from "./ErrorBoundary";

// What an old bundle does with a response from a newer daemon: map over a key
// that is no longer there.
function StaleConsumer({ data }: { data: { phases?: string[] } }) {
  return (
    <ul>
      {data.phases!.map((p) => (
        <li key={p}>{p}</li>
      ))}
    </ul>
  );
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("ErrorBoundary", () => {
  it("renders its children while nothing throws", () => {
    render(
      <ErrorBoundary>
        <StaleConsumer data={{ phases: ["offpeak"] }} />
      </ErrorBoundary>,
    );
    expect(screen.getByText("offpeak")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("shows the fallback instead of a blank page when a child throws", () => {
    // React reports the caught error on console.error; it is expected here.
    vi.spyOn(console, "error").mockImplementation(() => {});
    render(
      <ErrorBoundary>
        <StaleConsumer data={{}} />
      </ErrorBoundary>,
    );
    expect(screen.getByRole("alert")).toHaveTextContent(
      /stopped drawing after an error/,
    );
    expect(screen.getByRole("button", { name: "Reload" })).toBeInTheDocument();
    // The page keeps its heading, and the error is there for a bug report.
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(
      "Is Xiaomi MiMo down?",
    );
    expect(screen.getByRole("alert")).toHaveTextContent(/reading 'map'/);
  });

  it("reloads the page from the fallback", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const onReload = vi.fn();
    render(
      <ErrorBoundary onReload={onReload}>
        <StaleConsumer data={{}} />
      </ErrorBoundary>,
    );
    await userEvent.click(screen.getByRole("button", { name: "Reload" }));
    expect(onReload).toHaveBeenCalledOnce();
  });
});
