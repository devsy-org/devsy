import type { ElectronApplication, Page } from "@playwright/test"
import { expect, test } from "@playwright/test"
import { launchApp, resetMockState } from "./electron-app.js"

let app: ElectronApplication
let page: Page

test.beforeAll(async () => {
  resetMockState()
  ;({ app, page } = await launchApp())
  // Navigate to workspaces and wait for data to load from mock CLI
  await page.click('[data-sidebar="sidebar"] a[href="#/workspaces"]')
  await page
    .locator('[data-slot="sidebar-inset"] h1')
    .first()
    .waitFor({ timeout: 5000 })
  // Wait for the mock CLI data to populate via the watcher
  await page.locator("table").waitFor({ timeout: 10000 })
})

test.afterAll(async () => {
  await app.close()
})

test.describe("Workspaces Page", () => {
  test("should list workspaces from CLI with correct names", async () => {
    const table = page.locator("table")
    await expect(table).toBeVisible()
    // Mock CLI returns "test-workspace" and "dev-env"
    await expect(table).toContainText("test-workspace")
    await expect(table).toContainText("dev-env")
  })

  test("should show provider names for each workspace", async () => {
    const table = page.locator("table")
    await expect(table).toContainText("docker")
    await expect(table).toContainText("kubernetes")
  })

  test("should show workspace status badges", async () => {
    const table = page.locator("table")
    await expect(table).toContainText("Running")
    await expect(table).toContainText("Stopped")
  })

  test("should open the create workspace sheet with templates", async () => {
    await page.getByRole("button", { name: /create workspace/i }).click()
    const sheet = page.locator('[role="dialog"]')
    await expect(sheet).toBeVisible({ timeout: 5000 })

    // Should show Quick Start Templates section
    await expect(sheet).toContainText("Quick Start Templates")
    // Should show language template buttons
    await expect(sheet).toContainText("Python")
    await expect(sheet).toContainText("Node.js")
    await expect(sheet).toContainText("Go")
    await expect(sheet).toContainText("Rust")
    await expect(sheet).toContainText("Java")
  })

  test("should show language icons in template buttons", async () => {
    const sheet = page.locator('[role="dialog"]')
    // Template buttons contain LanguageIcon components which render <img> tags
    const icons = sheet.locator("button img")
    const iconCount = await icons.count()
    expect(iconCount).toBeGreaterThan(0)

    // Verify at least one icon loaded successfully (naturalWidth > 0)
    const firstIcon = icons.first()
    await expect(firstIcon).toBeVisible()
    const naturalWidth = await firstIcon.evaluate(
      (el: HTMLImageElement) => el.naturalWidth,
    )
    expect(naturalWidth).toBeGreaterThan(0)
  })

  test("should select a template and populate the source field", async () => {
    const sheet = page.locator('[role="dialog"]')
    // Click the Python template
    await sheet.locator("button", { hasText: "Python" }).click()

    // Source input should be populated with the template URL
    const sourceInput = sheet.locator('input[placeholder*="github"]')
    await expect(sourceInput).toHaveValue(
      "https://github.com/microsoft/vscode-remote-try-python",
    )
  })

  test("should submit workspace creation and show output", async () => {
    const sheet = page.locator('[role="dialog"]')
    // Source should already be filled from previous test
    // Click Create Workspace button
    await sheet.getByRole("button", { name: /create workspace/i }).click()

    // The mock CLI handles "up" and streams output lines
    // Wait for output to appear
    await expect(sheet).toContainText("Output", { timeout: 10000 })
    // The mock outputs: "Resolving source...", "Pulling image...", etc.
    await expect(sheet).toContainText(/resolving|pulling|starting|ready/i, {
      timeout: 10000,
    })
  })
})
