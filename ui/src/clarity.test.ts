import { afterEach, describe, expect, it, vi } from "vitest";
import { installClarity } from "./clarity";

function tags(): HTMLScriptElement[] {
  return [...document.head.querySelectorAll("script")].filter((s) =>
    s.src.includes("clarity.ms"),
  );
}

afterEach(() => {
  vi.unstubAllEnvs();
  for (const tag of tags()) tag.remove();
});

describe("installClarity", () => {
  it("appends the tag in a production build", () => {
    vi.stubEnv("PROD", true);

    installClarity();

    const found = tags();
    expect(found).toHaveLength(1);
    const tag = found[0]!;
    // The project id is the whole install: a wrong one reports into someone
    // else's dashboard and looks exactly like a working setup from here.
    expect(tag.src).toBe("https://www.clarity.ms/tag/xy577xi4l2?ref=bwt");
    // Blocking the parser on a third-party host would put an analytics
    // vendor's latency in front of a page whose entire subject is latency.
    expect(tag.async).toBe(true);
  });

  it("stays out of a dev build", () => {
    vi.stubEnv("PROD", false);

    installClarity();

    expect(tags()).toHaveLength(0);
  });

  // The origin is the one exception the CSP carries (see
  // backend/internal/httpapi/middleware.go). A tag pointed anywhere else does
  // not load, so pin the host rather than trust the template string.
  it("loads from the origin the CSP names", () => {
    vi.stubEnv("PROD", true);

    installClarity();

    expect(new URL(tags()[0]!.src).origin).toBe("https://www.clarity.ms");
  });
});
