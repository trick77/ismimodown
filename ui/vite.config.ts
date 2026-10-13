import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
// Extension included: Vite's native config loader does not resolve it
// otherwise, and warns on every build without it.
import { stripCommentsPlugin } from "./build/strip-comments.ts";
import { staticCopyPlugin } from "./build/static-copy.ts";

// The tests format dates in a fixed zone, whatever the machine's: vitest's
// workers inherit it from here.
process.env.TZ = "Europe/Zurich";

export default defineConfig({
  // static-copy before strip-comments: the first consumes the marker comment
  // the second would otherwise delete. Their enforce/order settings say the
  // same thing, so this line is belt to that; keep both.
  plugins: [react(), tailwindcss(), staticCopyPlugin(), stripCommentsPlugin()],
  // Straight into the Go binary's embed directory, so `go build` bakes the real
  // bundle in. Never the Vite default ui/dist.
  //
  // emptyOutDir off: it would delete the tracked dist/.gitkeep go:embed needs.
  // ship check (ui.embed) and make fe-build clear the stale bundle instead.
  build: { outDir: "../backend/web/dist", emptyOutDir: false },
  server: { proxy: { "/api": "http://localhost:8080" } },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    coverage: {
      provider: "v8",
      // lcov feeds ship's UI floor and patch gate, text-summary is for
      // whoever reads the log.
      reporter: ["text-summary", "lcov"],
      // ship resolves the lcov from the repo root (ui.lcov), so it lands
      // outside ui/.
      reportsDirectory: "../coverage/ui",
      include: ["src/**/*.{ts,tsx}"],
      exclude: [
        // Entrypoint: mounts the app and does nothing else.
        "src/main.tsx",
        "src/vite-env.d.ts",
        // Charts are ECharts option objects rendered by a canvas library jsdom
        // cannot exercise. The option BUILDERS are pure functions and are
        // tested directly; the thin render wrappers are not.
        "src/charts/EChart.tsx",
      ],
    },
  },
});
