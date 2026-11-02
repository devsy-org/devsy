import type { ElectronApplication, Page } from "@playwright/test"
import { expect, test } from "@playwright/test"
import { launchApp, resetMockState } from "./electron-app.js"

const FAILED_RUN = "11111111-1111-4111-8111-111111111111"
const REMOVED_RUN = "22222222-2222-4222-8222-222222222222"
const INVOCATION = "a1111111-0000-4000-8000-000000000001"

let app: ElectronApplication
let page: Page

test.beforeAll(async () => {
  resetMockState()
  ;({ app, page } = await launchApp())
})
test.afterAll(async () => {
  await app?.close()
})

test("shows retained setup history in the Logs tab", async () => {
  await page.evaluate(() => {
    window.location.hash = "#/workspaces/test-workspace?tab=logs"
  })
  const history = page.getByTestId("workspace-run-logs")
  await expect(history).toBeVisible()
  const steps = history.getByRole("list", { name: "Build and lifecycle steps" })
  await expect(steps.getByText("postCreateCommand")).toBeVisible()
  await expect(steps.getByText("Failed")).toBeVisible()
  await expect(history.getByText("npm install")).toBeVisible()
  // Output that looks like a log line stays literal text.
  await expect(
    history.getByText('{"level":"error","msg":"literal"}'),
  ).toBeVisible()
  await expect(page.getByText("Desktop diagnostic files")).toBeVisible()
})

test("opens a pinned run and invocation from a View logs link", async () => {
  const route = `#/workspaces/test-workspace?tab=logs&context=default&run=${FAILED_RUN}&invocation=${INVOCATION}`
  await page.evaluate((hash) => {
    window.location.hash = hash
  }, route)
  const history = page.getByTestId("workspace-run-logs")
  await expect(history.getByText("npm install")).toBeVisible()
  await expect(history.getByLabel("Run")).toHaveValue(FAILED_RUN)
})

test("renders a removed workspace's run read-only", async () => {
  const route = `#/workspaces/deleted-project?tab=logs&context=default&run=${REMOVED_RUN}`
  await page.evaluate((hash) => {
    window.location.hash = hash
  }, route)
  await expect(page.getByText(/no longer exists/)).toBeVisible()
  await expect(page.getByText(/Read-only history/)).toBeVisible()
  await expect(page.getByRole("button", { name: "Start" })).toHaveCount(0)
})

test("keeps the plain not-found view when no run is linked", async () => {
  await page.evaluate(() => {
    window.location.hash = "#/workspaces/never-existed"
  })
  await expect(page.getByText("Workspace not found.")).toBeVisible()
})
