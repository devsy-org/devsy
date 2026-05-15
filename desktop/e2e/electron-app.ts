import { chmodSync, unlinkSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join, resolve } from "node:path"
import { fileURLToPath } from "node:url"
import type { ElectronApplication, Page } from "@playwright/test"
import { _electron as electron } from "@playwright/test"

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..")

export async function launchApp(): Promise<{
  app: ElectronApplication
  page: Page
}> {
  // Point the app at the mock devpod script via environment variable.
  // CliRunner detects .cjs extension and runs it through node directly,
  // so this works on all platforms without platform-specific wrappers.
  const mockBinary = resolve(ROOT, "e2e/fixtures/mock-devpod.cjs")
  if (process.platform !== "win32") {
    chmodSync(mockBinary, 0o755)
  }

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
  await page.locator('[data-sidebar="sidebar"]').waitFor({ timeout: 10000 })

  // Wait for the watcher to poll the mock CLI and populate data
  // The watcher polls every 3 seconds — wait for at least one cycle
  await page.waitForTimeout(4000)

  return { app, page }
}

export function resetMockState(): void {
  try {
    unlinkSync(join(tmpdir(), "devsy-mock-state.json"))
  } catch {
    // File doesn't exist, that's fine
  }
}
