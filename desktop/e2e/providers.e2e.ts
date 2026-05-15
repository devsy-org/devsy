import type { ElectronApplication, Page } from "@playwright/test"
import { expect, test } from "@playwright/test"
import { launchApp } from "./electron-app.js"

let app: ElectronApplication
let page: Page

test.beforeAll(async () => {
  ;({ app, page } = await launchApp())
  await page.click('[data-sidebar="sidebar"] a[href="#/providers"]')
  await page
    .locator('[data-slot="sidebar-inset"] h1')
    .first()
    .waitFor({ timeout: 5000 })
  // Wait for the mock CLI data to populate provider cards
  await page
    .locator('[data-slot="sidebar-inset"] main button')
    .first()
    .waitFor({ timeout: 10000 })
})

test.afterAll(async () => {
  await app.close()
})

test.describe("Providers Page", () => {
  test("should show providers heading", async () => {
    const heading = page.locator('[data-slot="sidebar-inset"] h1').first()
    await expect(heading).toContainText(/providers/i)
  })

  test("should list provider cards from CLI data", async () => {
    const main = page.locator('[data-slot="sidebar-inset"] main')
    // Mock CLI returns "docker" and "kubernetes" providers
    await expect(main).toContainText("docker", { timeout: 10000 })
    await expect(main).toContainText("kubernetes", { timeout: 10000 })
  })

  test("should show provider descriptions", async () => {
    const main = page.locator('[data-slot="sidebar-inset"] main')
    await expect(main).toContainText("Devsy on Docker")
    await expect(main).toContainText("Devsy on Kubernetes")
  })

  test("should show default badge on the default provider", async () => {
    const main = page.locator('[data-slot="sidebar-inset"] main')
    await expect(main).toContainText("default")
  })

  test("should show initialized badge on initialized providers", async () => {
    const main = page.locator('[data-slot="sidebar-inset"] main')
    await expect(main).toContainText("initialized")
  })

  test("should render provider icons that load successfully", async () => {
    const main = page.locator('[data-slot="sidebar-inset"] main')
    const icons = main.locator("img")
    const iconCount = await icons.count()
    expect(iconCount).toBeGreaterThan(0)

    // Verify each icon loaded (naturalWidth > 0 means the image loaded)
    for (let i = 0; i < iconCount; i++) {
      const icon = icons.nth(i)
      const naturalWidth = await icon.evaluate(
        (el: HTMLImageElement) => el.naturalWidth,
      )
      const src = await icon.getAttribute("src")
      expect(
        naturalWidth,
        `Provider icon ${src} should load successfully`,
      ).toBeGreaterThan(0)
    }
  })

  test("should open provider detail sheet when clicking a provider card", async () => {
    const main = page.locator('[data-slot="sidebar-inset"] main')
    // Click on the docker provider card
    await main.locator("button", { hasText: "docker" }).first().click()

    const sheet = page.locator('[role="dialog"]')
    await expect(sheet).toBeVisible({ timeout: 5000 })
    await expect(sheet).toContainText("docker")
  })
})
