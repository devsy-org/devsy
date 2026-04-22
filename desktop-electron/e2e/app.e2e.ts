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

test.describe("App Launch", () => {
  test("should render the sidebar", async () => {
    const sidebar = page.locator("[data-sidebar=\"sidebar\"]")
    await expect(sidebar).toBeVisible()
  })

  test("should show the Dashboard heading", async () => {
    const heading = page.locator("[data-slot=\"sidebar-inset\"] h2").first()
    await expect(heading).toContainText(/dashboard/i)
  })

  test("should display workspace count on the dashboard", async () => {
    const main = page.locator("[data-slot=\"sidebar-inset\"] main")
    await expect(main).toContainText("Workspaces")
  })
})
