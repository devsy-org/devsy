import type { ElectronApplication, Page } from "@playwright/test"
import { expect, test } from "@playwright/test"
import { launchApp, resetMockState } from "./electron-app.js"
let app: ElectronApplication
let page: Page
const id = "lifecycleprobe"
async function invoke(channel: string, args?: Record<string, unknown>) {
  return page.evaluate(
    ({ channel, args }) => window.electronAPI.invoke(channel, args),
    { channel, args },
  )
}
async function finished() {
  await expect
    .poll(
      async () => {
        const snapshot = (await invoke("workspace_snapshot")) as {
          jobs: Record<string, { state: string }>
        }
        return snapshot.jobs[id]?.state
      },
      { timeout: 30000 },
    )
    .toBe("succeeded")
}
test.beforeAll(async () => {
  resetMockState()
  ;({ app, page } = await launchApp())
  await page.click('[data-sidebar="sidebar"] a[href="#/workspaces"]')
})
test.afterAll(async () => {
  await app?.close()
})
test("shares lifecycle progress across actions, navigation and window reload", async () => {
  test.setTimeout(120000)
  await invoke("workspace_up", {
    source: "https://example.com/lifecycleprobe.git",
    workspaceId: id,
  })
  await finished()
  const row = () => page.locator("tr").filter({ hasText: id })
  await expect(row()).toContainText("Running")
  for (const [action, label] of [
    ["stop", "Stopping"],
    ["rebuild", "Rebuilding"],
    ["reset", "Resetting"],
  ]) {
    await invoke(`workspace_${action}`, { workspaceId: id })
    await expect(row()).toContainText(label)
    await finished()
  }
  await invoke("workspace_up", { source: id })
  await finished()
  await invoke("workspace_delete", { workspaceId: id })
  await expect(row()).toContainText("Deleting")
  await page.click('[data-sidebar="sidebar"] a[href="#/machines"]')
  await page.click('[data-sidebar="sidebar"] a[href="#/workspaces"]')
  await expect(row()).toContainText("Deleting")
  await page.reload()
  await page.locator('[data-sidebar="sidebar"]').waitFor()
  await expect(row()).toContainText("Deleting")
  await finished()
  await expect(row()).toHaveCount(0)
})
