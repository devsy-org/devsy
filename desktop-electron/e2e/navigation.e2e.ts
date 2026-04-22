import { test, expect } from "@playwright/test"
import type { ElectronApplication, Page } from "@playwright/test"
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
    const heading = page.locator("[data-slot=\"sidebar-inset\"] h1").first()
    await expect(heading).toContainText(/workspaces/i, { timeout: 5000 })
  })

  test("should navigate to Providers page", async () => {
    await page.click('[data-sidebar="sidebar"] a[href="#/providers"]')
    const heading = page.locator("[data-slot=\"sidebar-inset\"] h1").first()
    await expect(heading).toContainText(/providers/i, { timeout: 5000 })
  })

  test("should navigate to Machines page", async () => {
    await page.click('[data-sidebar="sidebar"] a[href="#/machines"]')
    const heading = page.locator("[data-slot=\"sidebar-inset\"] h1").first()
    await expect(heading).toContainText(/machines/i, { timeout: 5000 })
  })

  test("should navigate back to Dashboard", async () => {
    await page.click('[data-sidebar="sidebar"] a[href="#/"]')
    const heading = page.locator("[data-slot=\"sidebar-inset\"] h1").first()
    await expect(heading).toContainText(/dashboard/i, { timeout: 5000 })
  })
})
