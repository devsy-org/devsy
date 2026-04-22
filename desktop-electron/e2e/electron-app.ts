import { _electron as electron } from "@playwright/test"
import type { ElectronApplication, Page } from "@playwright/test"
import { resolve, dirname } from "node:path"
import { fileURLToPath } from "node:url"
import { chmodSync, writeFileSync } from "node:fs"

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..")

function getMockBinary(): string {
  const cjsScript = resolve(ROOT, "e2e/fixtures/mock-devpod.cjs")
  if (process.platform === "win32") {
    // Generate .cmd wrapper at runtime with CRLF line endings
    // so cmd.exe parses it correctly regardless of git line-ending settings
    const cmdPath = resolve(ROOT, "e2e/fixtures/_mock-devpod.cmd")
    writeFileSync(cmdPath, `@echo off\r\nnode "${cjsScript}" %*\r\n`)
    return cmdPath
  }
  const bashWrapper = resolve(ROOT, "e2e/fixtures/mock-devpod")
  chmodSync(bashWrapper, 0o755)
  return bashWrapper
}

export async function launchApp(): Promise<{
  app: ElectronApplication
  page: Page
}> {
  const mockBinary = getMockBinary()

  const app = await electron.launch({
    args: [resolve(ROOT, "dist/main/index.js")],
    env: {
      ...process.env,
      NODE_ENV: "test",
      DEVPOD_CLI_PATH: mockBinary,
    },
  })

  const page = await app.firstWindow()
  await page.waitForLoadState("domcontentloaded")
  await page.locator("[data-sidebar=\"sidebar\"]").waitFor({ timeout: 10000 })

  // Wait for the watcher to poll the mock CLI and populate data
  // The watcher polls every 3 seconds — wait for at least one cycle
  await page.waitForTimeout(4000)

  return { app, page }
}
