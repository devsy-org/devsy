import type { ElectronApplication, Page } from "@playwright/test"
import { expect, test } from "@playwright/test"
import { launchApp, resetMockState } from "./electron-app.js"

let app: ElectronApplication
let page: Page

async function invoke(channel: string, args?: Record<string, unknown>) {
  return page.evaluate(
    ({ channel, args }) => window.electronAPI.invoke(channel, args),
    { channel, args },
  )
}

test.beforeAll(async () => {
  resetMockState()
  ;({ app, page } = await launchApp())
})

test.afterAll(async () => {
  await app?.close()
})

test("status and result protocols are independent of CLI diagnostic level", async () => {
  test.setTimeout(120000)
  for (const [index, level] of (["info", "warn", "error"] as const).entries()) {
    const workspaceId = `protocol-${index}`
    await invoke("set_app_settings", { patch: { cliCaptureLogLevel: level } })
    await invoke("workspace_up", {
      source: `https://example.com/${workspaceId}.git`,
      workspaceId,
    })
    await expect
      .poll(async () => {
        const snapshot = (await invoke("workspace_snapshot")) as {
          jobs: Record<string, { state: string }>
        }
        return snapshot.jobs[workspaceId]?.state
      }, { timeout: 30000 })
      .toBe("succeeded")
    const workspaces = (await invoke("workspace_list")) as Array<{ id: string; status: string }>
    expect(workspaces.find((workspace) => workspace.id === workspaceId)?.status).toBe("Running")
  }
})
