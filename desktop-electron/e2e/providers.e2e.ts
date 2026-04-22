import { test, expect } from "@playwright/test"
import type { ElectronApplication, Page } from "@playwright/test"
import { launchApp } from "./electron-app.js"

let app: ElectronApplication
let page: Page

test.beforeAll(async () => {
  ;({ app, page } = await launchApp())
  await page.click('[data-sidebar="sidebar"] a[href="#/providers"]')
  await page.locator("[data-slot=\"sidebar-inset\"] h2").first().waitFor({ timeout: 5000 })
})

test.afterAll(async () => {
  await app.close()
})

test.describe("Providers Page", () => {
  test("should show the providers heading", async () => {
    const heading = page.locator("[data-slot=\"sidebar-inset\"] h2").first()
    await expect(heading).toContainText(/providers/i)
  })

  test("should list providers or show empty state", async () => {
    const main = page.locator("[data-slot=\"sidebar-inset\"] main")
    const text = await main.textContent()
    expect(text?.length).toBeGreaterThan(0)
  })

  test("should have an Add Provider button", async () => {
    const btn = page.locator("[data-slot=\"sidebar-inset\"] main button").first()
    await expect(btn).toBeVisible()
  })
})
