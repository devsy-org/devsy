import { resolve } from "node:path"
import { svelte } from "@sveltejs/vite-plugin-svelte"
import { defineConfig } from "vitest/config"

export default defineConfig({
  plugins: [svelte({ hot: false })],
  resolve: {
    alias: {
      $lib: resolve(__dirname, "src/renderer/src/lib"),
    },
    conditions: ["browser"],
  },
  test: {
    include: [
      "src/renderer/src/**/*.test.ts",
      "src/main/__tests__/**/*.test.ts",
    ],
    environment: "jsdom",
    setupFiles: ["src/renderer/src/lib/__mocks__/setup.ts"],
  },
})
