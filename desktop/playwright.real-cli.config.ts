import { isAbsolute, join } from "node:path"
import { defineConfig } from "@playwright/test"

const evidence = process.env.REAL_CLI_EVIDENCE
if (!evidence || !isAbsolute(evidence))
  throw new Error("REAL_CLI_EVIDENCE must be absolute")

export default defineConfig({
  testDir: "./e2e",
  testMatch: "**/context-options.real-cli.ts",
  timeout: 90_000,
  expect: { timeout: 15_000 },
  retries: 0,
  workers: 1,
  reporter: [["list"]],
  outputDir: join(evidence, "playwright"),
})
