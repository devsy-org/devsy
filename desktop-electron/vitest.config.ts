import { resolve } from "path"
import { defineConfig } from "vitest/config"

export default defineConfig({
  resolve: {
    alias: {
      $lib: resolve(__dirname, "src/renderer/src/lib"),
      "$app/stores": resolve(
        __dirname,
        "src/renderer/src/lib/__mocks__/app-stores.ts",
      ),
      "$app/navigation": resolve(
        __dirname,
        "src/renderer/src/lib/__mocks__/app-navigation.ts",
      ),
      "$app/environment": resolve(
        __dirname,
        "src/renderer/src/lib/__mocks__/app-environment.ts",
      ),
    },
  },
  test: {
    include: ["src/renderer/src/**/*.test.ts"],
    environment: "jsdom",
    setupFiles: ["src/renderer/src/lib/__mocks__/setup.ts"],
  },
})
