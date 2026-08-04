// The ESM build, not lib/core.
//
// lib/ is CommonJS, and its default export survives bundling as an object
// rather than a component: the page died with React error #130 ("expected a
// string but got: object") while every unit test passed, because the tests mock
// this module. Caught only by loading the built bundle in a real browser.
import ReactEChartsCore from "echarts-for-react/esm/core";
import * as echarts from "echarts/core";
import { BarChart, LineChart } from "echarts/charts";
import {
  GridComponent,
  LegendComponent,
  TooltipComponent,
} from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";

// Only the pieces this dashboard actually draws are registered.
//
// The full `echarts` barrel import pulls every chart type, every renderer and
// the whole component set: measured at 1.35 MB raw / 446 kB gzipped, for a page
// that draws lines and one stacked bar. Registering explicitly brings that down
// by roughly two thirds. It also means adding a new chart type is a deliberate
// act rather than something that silently arrives in the bundle.
echarts.use([
  LineChart,
  BarChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
  CanvasRenderer,
]);

// EChart is a thin render wrapper. Everything with logic lives in options.ts,
// which is why this file is excluded from coverage: it renders to a canvas that
// jsdom cannot exercise, so a test here would assert nothing real.
export function EChart({
  option,
  height = 240,
  ariaLabel,
}: {
  option: Record<string, unknown>;
  height?: number;
  ariaLabel: string;
}) {
  return (
    <div role="img" aria-label={ariaLabel}>
      <ReactEChartsCore
        echarts={echarts}
        option={option}
        style={{ height, width: "100%" }}
        notMerge
      />
    </div>
  );
}
