import { resolve } from "path"
import { defineConfig } from "vitest/config"

export default defineConfig({
  resolve: {
    alias: {
      $lib: resolve(__dirname, "src/renderer/src/lib"),
    },
  },
  test: {
    include: ["src/renderer/src/**/*.test.ts", "src/main/__tests__/**/*.test.ts"],
    environment: "jsdom",
    setupFiles: ["src/renderer/src/lib/__mocks__/setup.ts"],
  },
})
