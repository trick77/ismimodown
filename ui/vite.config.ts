import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  // Straight into the Go binary's embed directory, so `go build` bakes the real
  // bundle in. Never the Vite default ui/dist.
  build: { outDir: "../backend/web/dist", emptyOutDir: true },
  server: { proxy: { "/api": "http://localhost:8080" } },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test-setup.ts"],
    coverage: {
      provider: "v8",
      // json-summary feeds hack/coverage-gate.sh, lcov feeds
      // hack/patch-coverage.sh, text-summary is for whoever reads the log.
      reporter: ["text-summary", "json-summary", "lcov"],
      // Both gates resolve coverage artifacts from the repo root, alongside the
      // backend's, so these reports land outside ui/.
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
