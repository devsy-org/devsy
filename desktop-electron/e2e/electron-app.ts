import { _electron as electron } from "@playwright/test"
import type { ElectronApplication, Page } from "@playwright/test"
import { resolve, dirname } from "node:path"
import { fileURLToPath } from "node:url"
import { copyFileSync, mkdirSync } from "node:fs"

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..")

export async function launchApp(): Promise<{
  app: ElectronApplication
  page: Page
}> {
  // Place mock binary in resources/bin/ so CliRunner.resolveBinaryPath finds it
  const mockSrc = resolve(ROOT, "e2e/fixtures/mock-devpod")
  const binDir = resolve(ROOT, "resources/bin")
  mkdirSync(binDir, { recursive: true })
  copyFileSync(mockSrc, resolve(binDir, "devpod"))

  const app = await electron.launch({
    args: [resolve(ROOT, "dist/main/index.js")],
    env: {
      ...process.env,
      NODE_ENV: "test",
    },
  })

  const page = await app.firstWindow()
  await page.waitForLoadState("domcontentloaded")

  return { app, page }
}
