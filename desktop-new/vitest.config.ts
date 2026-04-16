import path from "node:path"
import { svelte } from "@sveltejs/vite-plugin-svelte"
import { defineConfig } from "vitest/config"

export default defineConfig({
  plugins: [svelte({ hot: false })],
  test: {
    environment: "jsdom",
    include: ["src/**/*.test.ts"],
    globals: true,
    setupFiles: ["src/lib/__mocks__/setup.ts"],
    alias: {
      $lib: path.resolve("./src/lib"),
      "$app/environment": path.resolve(
        "./src/lib/__mocks__/app-environment.ts",
      ),
      "$app/stores": path.resolve("./src/lib/__mocks__/app-stores.ts"),
      "$app/navigation": path.resolve("./src/lib/__mocks__/app-navigation.ts"),
    },
  },
})
