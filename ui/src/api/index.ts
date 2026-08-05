import { api } from "./http";
import type { Dashboard } from "./types";

// One request for the whole page — every summary, series, the cost panel, the
// pulse strip's cycles and the raw rows.
//
// It used to be six endpoints called fifteen times per load, and the second
// half of that could not start until the first had answered: the models to fan
// out over came back inside the summary. Every one of those calls spent a
// token from a per-IP bucket of twenty, so clicking two range pills in a row
// was enough to make the page answer itself "rate limited".
//
// The same reasoning the cost panel was already built on, applied to the rest
// of the page: parts of one render belong in one response, or they end up
// describing different instants and costing a round trip each to say so.
export const getDashboard = (window: string, signal?: AbortSignal) =>
  api.get<Dashboard>(
    `/api/dashboard?window=${encodeURIComponent(window)}`,
    signal,
  );
