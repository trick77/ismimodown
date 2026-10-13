import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach, vi } from "vitest";

// Testing Library only auto-cleans when Vitest globals are enabled, and this
// project imports describe/it/expect explicitly. Without this, renders from one
// test are still in the DOM during the next and getByTestId finds several.
afterEach(cleanup);

// jsdom has no canvas, and ECharts renders to one. The charts' logic lives in
// pure option builders that are tested directly, so the renderer is stubbed
// once here rather than mocked in each test file.
vi.mock("echarts-for-react/esm/core", () => ({
  default: () => null,
}));
