import type { ElectronApplication, Page } from "@playwright/test"
import { expect, test } from "@playwright/test"
import { launchApp } from "./electron-app.js"

let app: ElectronApplication
let page: Page

test.beforeAll(async () => {
  ;({ app, page } = await launchApp())
})

test.afterAll(async () => {
  await app.close()
})

test.describe("App Launch", () => {
  test("should render the sidebar with navigation links", async () => {
    const sidebar = page.locator('[data-sidebar="sidebar"]')
    await expect(sidebar).toBeVisible()

    // Verify all expected nav items exist
    await expect(sidebar.locator('a[href="#/"]')).toBeVisible()
    await expect(sidebar.locator('a[href="#/workspaces"]')).toBeVisible()
    await expect(sidebar.locator('a[href="#/providers"]')).toBeVisible()
    await expect(sidebar.locator('a[href="#/machines"]')).toBeVisible()
    await expect(sidebar.locator('a[href="#/contexts"]')).toBeVisible()
    await expect(sidebar.locator('a[href="#/terminals"]')).toBeVisible()
    await expect(sidebar.locator('a[href="#/ssh-keys"]')).toBeVisible()
    await expect(sidebar.locator('a[href="#/settings"]')).toBeVisible()
  })

  test("should show the Dashboard with summary data from CLI", async () => {
    await page.click('[data-sidebar="sidebar"] a[href="#/"]')
    const main = page.locator('[data-slot="sidebar-inset"]')
    await expect(main.locator("h1").first()).toContainText(/dashboard/i)

    // Dashboard should show workspace and provider counts from the mock CLI
    // Wait for the watcher to poll and populate data
    await expect(main).toContainText("Workspaces", { timeout: 10000 })
    await expect(main).toContainText("Providers", { timeout: 10000 })
  })

  test("should show sidebar badges with counts from CLI data", async () => {
    const sidebar = page.locator('[data-sidebar="sidebar"]')
    // The watcher polls the mock CLI which returns 2 workspaces and 2 providers
    // Wait for badges to appear with non-zero counts
    const workspaceBadge = sidebar.locator(
      '[data-sidebar="menu-item"]:has(a[href="#/workspaces"]) [data-sidebar="menu-badge"]',
    )
    await expect(workspaceBadge).toContainText("2", { timeout: 10000 })

    const providerBadge = sidebar.locator(
      '[data-sidebar="menu-item"]:has(a[href="#/providers"]) [data-sidebar="menu-badge"]',
    )
    await expect(providerBadge).toContainText("2", { timeout: 10000 })
  })
})
