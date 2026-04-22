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
    await page.click('nav a[href="/workspaces"]')
    const heading = page.locator("h2")
    await expect(heading).toContainText(/workspaces/i, { timeout: 5000 })
  })

  test("should navigate to Providers page", async () => {
    await page.click('nav a[href="/providers"]')
    const heading = page.locator("h2")
    await expect(heading).toContainText(/providers/i, { timeout: 5000 })
  })

  test("should navigate to Machines page", async () => {
    await page.click('nav a[href="/machines"]')
    const heading = page.locator("h2")
    await expect(heading).toContainText(/machines/i, { timeout: 5000 })
  })

  test("should navigate back to Dashboard", async () => {
    await page.click('nav a[href="/"]')
    const heading = page.locator("h2")
    await expect(heading).toContainText(/dashboard/i, { timeout: 5000 })
  })
})
