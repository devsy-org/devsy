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

test("main-process tray navigation reaches the renderer route", async () => {
  await page.evaluate(() =>
    window.electronAPI?.invoke("test_app_navigation", { route: "/settings" }),
  )
  await expect(page).toHaveURL(/#\/settings$/)
  await expect(
    page.locator('[data-slot="sidebar-inset"] h1').first(),
  ).toContainText(/settings/i)

  await page.evaluate(() =>
    window.electronAPI?.invoke("test_app_navigation", {
      hide: true,
      route: "/workspaces/test-workspace",
    }),
  )
  await expect(page).toHaveURL(/#\/workspaces\/test-workspace$/)
  await expect(page.locator('[data-slot="sidebar-inset"]')).toContainText(
    /test-workspace/i,
  )
  const visible = await app.evaluate(({ BrowserWindow }) =>
    BrowserWindow.getAllWindows()[0]?.isVisible(),
  )
  expect(visible).toBe(true)

  await page.keyboard.press(
    process.platform === "darwin" ? "Meta+n" : "Control+n",
  )
  await expect(page).toHaveURL(/#\/workspace\/new$/)
  await expect(
    page.locator('[data-slot="sidebar-inset"] h1').first(),
  ).toContainText(/workspaces/i)
})
