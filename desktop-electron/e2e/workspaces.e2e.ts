import { test, expect } from "@playwright/test"
import type { ElectronApplication, Page } from "@playwright/test"
import { launchApp } from "./electron-app.js"

let app: ElectronApplication
let page: Page

test.beforeAll(async () => {
  ;({ app, page } = await launchApp())
  await page.click('[data-sidebar="sidebar"] a[href="#/workspaces"]')
  await page.locator("[data-slot=\"sidebar-inset\"] h1").first().waitFor({ timeout: 5000 })
})

test.afterAll(async () => {
  await app.close()
})

test.describe("Workspaces Page", () => {
  test("should list workspaces", async () => {
    const main = page.locator("[data-slot=\"sidebar-inset\"] main")
    await expect(main).toBeVisible()
    const text = await main.textContent()
    expect(text?.length).toBeGreaterThan(0)
  })

  test("should have a Create Workspace button", async () => {
    const btn = page.getByRole("button", { name: /create workspace/i })
    await expect(btn).toBeVisible()
  })

  test("should open the create workspace form", async () => {
    await page.getByRole("button", { name: /create workspace/i }).click()
    const dialog = page.locator('[role="dialog"]')
    await expect(dialog).toBeVisible({ timeout: 5000 })
  })
})
