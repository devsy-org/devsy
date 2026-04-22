import { test, expect } from "@playwright/test"
import type { ElectronApplication, Page } from "@playwright/test"
import { launchApp } from "./electron-app.js"

let app: ElectronApplication
let page: Page

test.beforeAll(async () => {
  ;({ app, page } = await launchApp())
  await page.click('[data-sidebar="sidebar"] a[href="#/workspaces"]')
  await page.locator("[data-slot=\"sidebar-inset\"] h2").first().waitFor({ timeout: 5000 })
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
    const btn = page.locator('a[href="#/workspaces/new"]')
    await expect(btn).toBeVisible()
  })

  test("should navigate to the create workspace form", async () => {
    await page.click('a[href="#/workspaces/new"]')
    const main = page.locator("[data-slot=\"sidebar-inset\"] main")
    await expect(main).toBeVisible({ timeout: 5000 })
    const text = await main.textContent()
    expect(text).toMatch(/create|new|workspace/i)
  })
})
