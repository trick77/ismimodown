import { api } from "./http";
import type {
  ModelSeries,
  ModelsResponse,
  NetSeries,
  SamplesResponse,
  Summary,
} from "./types";

export const getModels = (signal?: AbortSignal) =>
  api.get<ModelsResponse>("/api/models", signal);

export const getSummary = (
  window: string,
  probe: string,
  signal?: AbortSignal,
) =>
  api.get<Summary>(
    `/api/summary?window=${encodeURIComponent(window)}&probe=${encodeURIComponent(probe)}`,
    signal,
  );

export const getSeries = (
  metric: string,
  window: string,
  probe: string,
  signal?: AbortSignal,
) =>
  api.get<ModelSeries>(
    `/api/series?metric=${encodeURIComponent(metric)}&window=${encodeURIComponent(window)}&probe=${encodeURIComponent(probe)}`,
    signal,
  );

export const getNetSeries = (window: string, signal?: AbortSignal) =>
  api.get<NetSeries>(
    `/api/series?metric=network&window=${encodeURIComponent(window)}`,
    signal,
  );

export const getSamples = (
  model: string,
  probe: string,
  limit: number,
  signal?: AbortSignal,
) =>
  api.get<SamplesResponse>(
    `/api/samples?model=${encodeURIComponent(model)}&probe=${encodeURIComponent(probe)}&limit=${limit}`,
    signal,
  );

export const getMethodology = (signal?: AbortSignal) =>
  api.get<Record<string, unknown>>("/api/methodology", signal);
