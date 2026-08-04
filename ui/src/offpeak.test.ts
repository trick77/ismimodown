import { describe, expect, it } from "vitest";
import {
  currentOffPeak,
  isOffPeak,
  offPeakBands,
  offPeakWindowFor,
  OFFPEAK_COEFFICIENT,
} from "./offpeak";
import { formatTime } from "./format";

const utc = (iso: string) => new Date(iso).getTime();
const HOUR = 3_600_000;

describe("isOffPeak", () => {
  // The window is 16:00–24:00 UTC, which is 00:00–08:00 in Beijing.
  it("is off-peak from 16:00 UTC to midnight", () => {
    expect(isOffPeak(utc("2026-08-04T15:59:59Z"))).toBe(false);
    expect(isOffPeak(utc("2026-08-04T16:00:00Z"))).toBe(true);
    expect(isOffPeak(utc("2026-08-04T23:59:59Z"))).toBe(true);
    expect(isOffPeak(utc("2026-08-05T00:00:00Z"))).toBe(false);
  });

  it("does not treat a bad timestamp as cheap", () => {
    expect(isOffPeak(Number.NaN)).toBe(false);
  });
});

describe("offPeakBands", () => {
  it("finds the band inside a range that contains one", () => {
    expect(
      offPeakBands(utc("2026-08-04T09:00:00Z"), utc("2026-08-05T09:00:00Z")),
    ).toEqual([[utc("2026-08-04T16:00:00Z"), utc("2026-08-05T00:00:00Z")]]);
  });

  // A band that runs past the edge of the plot has to STOP there. Drawn full
  // width it would shade hours the chart does not show.
  it("clips a band to the range rather than overhanging it", () => {
    expect(
      offPeakBands(utc("2026-08-04T20:00:00Z"), utc("2026-08-04T22:00:00Z")),
    ).toEqual([[utc("2026-08-04T20:00:00Z"), utc("2026-08-04T22:00:00Z")]]);
  });

  // The band opens the previous UTC day and runs past midnight, so a range
  // starting after midnight is still inside it.
  it("catches a band that opened before the range started", () => {
    const bands = offPeakBands(
      utc("2026-08-04T22:00:00Z"),
      utc("2026-08-05T02:00:00Z"),
    );
    expect(bands).toEqual([
      [utc("2026-08-04T22:00:00Z"), utc("2026-08-05T00:00:00Z")],
    ]);
  });

  it("emits one band per day across a two-day range", () => {
    expect(
      offPeakBands(utc("2026-08-04T00:00:00Z"), utc("2026-08-06T00:00:00Z")),
    ).toHaveLength(2);
  });

  it("returns nothing for an empty or inverted range", () => {
    const t = utc("2026-08-04T18:00:00Z");
    expect(offPeakBands(t, t)).toEqual([]);
    expect(offPeakBands(t, t - HOUR)).toEqual([]);
    expect(offPeakBands(Number.NaN, t)).toEqual([]);
  });

  // The whole reason the window is stored in UTC. Beijing has no DST and
  // Zurich does, so the SAME band reads 18:00–02:00 in August and 17:00–01:00
  // in January. A local-clock constant would be wrong for half the year.
  it("lands on different local hours either side of the DST changeover", () => {
    const summer = offPeakBands(
      utc("2026-08-04T00:00:00Z"),
      utc("2026-08-05T00:00:00Z"),
    )[0]!;
    const winter = offPeakBands(
      utc("2026-01-04T00:00:00Z"),
      utc("2026-01-05T00:00:00Z"),
    )[0]!;
    expect(formatTime(new Date(summer[0]))).toBe("18:00");
    expect(formatTime(new Date(winter[0]))).toBe("17:00");
  });
});

describe("offPeakWindowFor", () => {
  // The note names the window off this, never off the clipped span: a plot
  // whose left edge lands mid-band would otherwise report an opening hour that
  // is just where the chart happens to start.
  it("gives the whole band from any instant inside it", () => {
    expect(offPeakWindowFor(utc("2026-08-04T20:00:00Z"))).toEqual([
      utc("2026-08-04T16:00:00Z"),
      utc("2026-08-05T00:00:00Z"),
    ]);
    expect(offPeakWindowFor(utc("2026-08-04T16:00:00Z"))).toEqual([
      utc("2026-08-04T16:00:00Z"),
      utc("2026-08-05T00:00:00Z"),
    ]);
  });
});

describe("currentOffPeak", () => {
  it("reports the end of the window while it is running", () => {
    const { active, boundaryMs } = currentOffPeak(utc("2026-08-04T20:00:00Z"));
    expect(active).toBe(true);
    expect(boundaryMs).toBe(utc("2026-08-05T00:00:00Z"));
  });

  it("reports the start of the next window while it is not", () => {
    const { active, boundaryMs } = currentOffPeak(utc("2026-08-04T09:00:00Z"));
    expect(active).toBe(false);
    expect(boundaryMs).toBe(utc("2026-08-04T16:00:00Z"));
  });
});

describe("the rate itself", () => {
  // 0.8x is MiMo's published coefficient. If this ever moves it moves in one
  // place, and the chip, the tooltip and the note all follow.
  it("is the published 0.8x", () => {
    expect(OFFPEAK_COEFFICIENT).toBe(0.8);
  });
});
