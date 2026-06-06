import { svelte } from "@sveltejs/vite-plugin-svelte"
import { defineConfig } from "vitest/config"

export default defineConfig({
  plugins: [svelte({ hot: false })],
  resolve: { conditions: ["browser"] },
  test: {
    include: ["src/**/*.test.ts", "scripts/**/*.test.ts"],
    environment: "jsdom",
  },
})
