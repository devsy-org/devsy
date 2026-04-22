import { defineConfig, externalizeDepsPlugin } from "electron-vite"
import { sveltekit } from "@sveltejs/kit/vite"
import tailwindcss from "@tailwindcss/vite"

export default defineConfig({
  main: {
    plugins: [externalizeDepsPlugin()],
    build: {
      outDir: "dist/main",
      rollupOptions: {
        input: "src/main/index.ts",
      },
    },
  },
  preload: {
    plugins: [externalizeDepsPlugin()],
    build: {
      outDir: "dist/preload",
      rollupOptions: {
        input: "src/preload/index.ts",
      },
    },
  },
  renderer: {
    root: "src/renderer",
    plugins: [tailwindcss(), sveltekit()],
    build: {
      outDir: "dist/renderer",
    },
    server: {
      port: 1420,
      strictPort: true,
    },
  },
})
