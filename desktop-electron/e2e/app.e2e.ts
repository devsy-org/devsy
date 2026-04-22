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
    const sidebar = page.locator("nav")
    await expect(sidebar).toBeVisible()
  })

  test("should show the Dashboard heading", async () => {
    const heading = page.locator("h2")
    await expect(heading).toContainText(/dashboard/i)
  })

  test("should display workspace count on the dashboard", async () => {
    const main = page.locator("main")
    await expect(main).toContainText("Workspaces")
  })
})
