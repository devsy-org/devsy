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

test.describe("Sidebar Navigation", () => {
  test("should navigate to Workspaces page", async () => {
    await page.click('[data-sidebar="sidebar"] a[href="#/workspaces"]')
    const heading = page.locator('[data-slot="sidebar-inset"] h1').first()
    await expect(heading).toContainText(/workspaces/i, { timeout: 5000 })
  })

  test("should navigate to Providers page", async () => {
    await page.click('[data-sidebar="sidebar"] a[href="#/providers"]')
    const heading = page.locator('[data-slot="sidebar-inset"] h1').first()
    await expect(heading).toContainText(/providers/i, { timeout: 5000 })
  })

  test("should navigate to Machines page", async () => {
    await page.click('[data-sidebar="sidebar"] a[href="#/machines"]')
    const heading = page.locator('[data-slot="sidebar-inset"] h1').first()
    await expect(heading).toContainText(/machines/i, { timeout: 5000 })
  })

  test("should navigate to Contexts page", async () => {
    await page.click('[data-sidebar="sidebar"] a[href="#/contexts"]')
    const heading = page.locator('[data-slot="sidebar-inset"] h1').first()
    await expect(heading).toContainText(/contexts/i, { timeout: 5000 })
  })

  test("should navigate to Terminals page", async () => {
    await page.click('[data-sidebar="sidebar"] a[href="#/terminals"]')
    // Terminals page doesn't have an h1 — it shows "No active terminals" or the terminal tabs
    const main = page.locator('[data-slot="sidebar-inset"]')
    await expect(main).toContainText(/no active terminals|new shell/i, {
      timeout: 5000,
    })
  })

  test("should navigate to SSH Keys page", async () => {
    await page.click('[data-sidebar="sidebar"] a[href="#/ssh-keys"]')
    const heading = page.locator('[data-slot="sidebar-inset"] h1').first()
    await expect(heading).toContainText(/ssh keys/i, { timeout: 5000 })
  })

  test("should navigate to Settings page", async () => {
    await page.click('[data-sidebar="sidebar"] a[href="#/settings"]')
    const heading = page.locator('[data-slot="sidebar-inset"] h1').first()
    await expect(heading).toContainText(/settings/i, { timeout: 5000 })
  })

  test("should navigate back to Dashboard", async () => {
    await page.click('[data-sidebar="sidebar"] a[href="#/"]')
    const heading = page.locator('[data-slot="sidebar-inset"] h1').first()
    await expect(heading).toContainText(/dashboard/i, { timeout: 5000 })
  })
})
