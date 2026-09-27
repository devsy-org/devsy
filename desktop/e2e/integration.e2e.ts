import type { ElectronApplication, Page } from "@playwright/test"
import { expect, test } from "@playwright/test"
import { launchApp, resetMockState } from "./electron-app.js"

test.describe
  .serial("Provider CRUD", () => {
    let app: ElectronApplication
    let page: Page

    test.beforeAll(async () => {
      resetMockState()
      ;({ app, page } = await launchApp())
    })

    test.afterAll(async () => {
      await app.close()
    })

    test("should show default providers", async () => {
      await page.click('[data-sidebar="sidebar"] a[href="#/providers"]')
      await page
        .locator('[data-slot="sidebar-inset"] h1')
        .first()
        .waitFor({ timeout: 5000 })
      await page
        .locator('[data-slot="sidebar-inset"] main button')
        .first()
        .waitFor({ timeout: 10000 })

      const main = page.locator('[data-slot="sidebar-inset"] main')
      await expect(main).toContainText("docker", { timeout: 10000 })
      await expect(main).toContainText("kubernetes", { timeout: 10000 })
    })

    test("should delete docker provider", async () => {
      const main = page.locator('[data-slot="sidebar-inset"] main')
      await main.locator("button", { hasText: "docker" }).first().click()

      // Wait for the ProviderSheet to open (use data-slot to target it specifically)
      const sheet = page.locator('[data-slot="sheet-content"]')
      await sheet.waitFor({ timeout: 5000 })

      await sheet.getByRole("button", { name: "Delete" }).click()

      const confirmDialog = page.locator('[data-slot="dialog-content"]')
      await confirmDialog.waitFor({ timeout: 5000 })
      await confirmDialog.getByRole("button", { name: "Delete" }).click()

      await sheet.waitFor({ state: "hidden", timeout: 10000 })

      // Wait for watcher to poll updated state
      await page.waitForTimeout(4000)

      await expect(main).not.toContainText("docker", { timeout: 10000 })
      await expect(main).toContainText("kubernetes", { timeout: 10000 })
    })

    test("should delete kubernetes provider", async () => {
      const main = page.locator('[data-slot="sidebar-inset"] main')
      await main.locator("button", { hasText: "kubernetes" }).first().click()

      const sheet = page.locator('[data-slot="sheet-content"]')
      await sheet.waitFor({ timeout: 5000 })

      await sheet.getByRole("button", { name: "Delete" }).click()

      const confirmDialog = page.locator('[data-slot="dialog-content"]')
      await confirmDialog.waitFor({ timeout: 5000 })
      await confirmDialog.getByRole("button", { name: "Delete" }).click()

      await sheet.waitFor({ state: "hidden", timeout: 10000 })
      await page.waitForTimeout(4000)

      // Empty state should appear
      await expect(page.locator('[data-slot="sidebar-inset"]')).toContainText(
        "No providers configured yet",
        { timeout: 10000 },
      )
    })

    test("should add docker provider from preset", async () => {
      // Click the empty-state "Add your first provider" button
      await page
        .getByRole("button", { name: /add your first provider/i })
        .click()

      const wizard = page.locator('[data-slot="dialog-content"]')
      await wizard.waitFor({ timeout: 5000 })
      await expect(wizard).toContainText("Select a Provider")

      await wizard
        .locator("button", { hasText: "docker" })
        .filter({ hasText: "Local Docker containers" })
        .click()

      await wizard.getByRole("button", { name: /^Continue$/ }).click()

      // The mock provider has no required options, so the wizard advances to Complete.
      await wizard
        .getByRole("button", { name: "Done" })
        .waitFor({ timeout: 15000 })
      await wizard.getByRole("button", { name: "Done" }).click()

      await wizard.waitFor({ state: "hidden", timeout: 10000 })
      await page.waitForTimeout(4000)

      const main = page.locator('[data-slot="sidebar-inset"] main')
      await expect(main).toContainText("docker", { timeout: 10000 })
    })

    test("should rename docker provider to my-docker", async () => {
      const main = page.locator('[data-slot="sidebar-inset"] main')
      await main.locator("button", { hasText: "docker" }).first().click()

      const sheet = page.locator('[data-slot="sheet-content"]')
      await sheet.waitFor({ timeout: 5000 })

      await sheet.getByRole("button", { name: "Rename" }).click()

      const renameInput = sheet.locator("input").first()
      await renameInput.fill("my-docker")

      await sheet.getByRole("button", { name: "Save" }).first().click()

      await sheet.waitFor({ state: "hidden", timeout: 10000 })

      // Wait for watcher
      await page.waitForTimeout(4000)

      await expect(main).toContainText("my-docker", { timeout: 10000 })
    })

    test("should delete renamed provider", async () => {
      const main = page.locator('[data-slot="sidebar-inset"] main')
      await main.locator("button", { hasText: "my-docker" }).first().click()

      const sheet = page.locator('[data-slot="sheet-content"]')
      await sheet.waitFor({ timeout: 5000 })

      await sheet.getByRole("button", { name: "Delete" }).click()

      const confirmDialog = page.locator('[data-slot="dialog-content"]')
      await confirmDialog.waitFor({ timeout: 5000 })
      await confirmDialog.getByRole("button", { name: "Delete" }).click()

      await sheet.waitFor({ state: "hidden", timeout: 10000 })
      await page.waitForTimeout(4000)

      await expect(main).not.toContainText("my-docker", { timeout: 10000 })
    })

    test("should re-add docker provider", async () => {
      // After deleting all, empty state should show
      await page
        .getByRole("button", { name: /add your first provider/i })
        .click()

      const wizard = page.locator('[data-slot="dialog-content"]')
      await wizard.waitFor({ timeout: 5000 })

      await wizard
        .locator("button", { hasText: "docker" })
        .filter({ hasText: "Local Docker containers" })
        .click()

      await wizard.getByRole("button", { name: /^Continue$/ }).click()

      await wizard
        .getByRole("button", { name: "Done" })
        .waitFor({ timeout: 15000 })
      await wizard.getByRole("button", { name: "Done" }).click()

      await wizard.waitFor({ state: "hidden", timeout: 10000 })
      await page.waitForTimeout(4000)

      const main = page.locator('[data-slot="sidebar-inset"] main')
      await expect(main).toContainText("docker", { timeout: 10000 })
    })
  })

test.describe
  .serial("Workspace lifecycle - Node.js", () => {
    let app: ElectronApplication
    let page: Page

    test.beforeAll(async () => {
      resetMockState()
      ;({ app, page } = await launchApp())
    })

    test.afterAll(async () => {
      await app.close()
    })

    test("should show default workspaces", async () => {
      await page.click('[data-sidebar="sidebar"] a[href="#/workspaces"]')
      await page.locator("table").waitFor({ timeout: 10000 })

      const table = page.locator("table")
      await expect(table).toContainText("test-workspace", { timeout: 10000 })
      await expect(table).toContainText("dev-env", { timeout: 10000 })
    })

    test("should create Node.js workspace", async () => {
      await page.getByRole("button", { name: /create workspace/i }).click()

      const dialog = page.locator('[role="dialog"]').first()
      await dialog.waitFor({ timeout: 5000 })

      await dialog.locator("button", { hasText: "docker" }).first().click()
      await dialog.getByRole("button", { name: /^continue$/i }).click()

      // Scope to the Git panel because the image catalog also has a Node.js card.
      await dialog
        .getByTestId("source-panel")
        .locator("button", { hasText: "Node.js" })
        .click()
      const sourceInput = dialog.locator('input[placeholder*="github"]')
      await expect(sourceInput).toHaveValue(
        "https://github.com/microsoft/vscode-remote-try-node",
        { timeout: 5000 },
      )
      await dialog.getByRole("button", { name: /^continue$/i }).click()

      await dialog.getByRole("button", { name: /^continue$/i }).click()

      await dialog.getByRole("button", { name: /^launch$/i }).click()

      await expect(dialog).toContainText(/resolving|pulling|starting|ready/i, {
        timeout: 15000,
      })
      await dialog
        .getByRole("button", { name: /open workspace/i })
        .waitFor({ timeout: 15000 })

      await page.keyboard.press("Escape")
      await dialog.waitFor({ state: "hidden", timeout: 5000 })

      // Wait for watcher to pick up the new workspace
      await page.waitForTimeout(4000)
    })

    test("should show new workspace in table", async () => {
      // Workspace ID from template name: 'Node.js' -> 'node-js'
      await expect(page.locator("table")).toContainText("node-js", {
        timeout: 10000,
      })
    })

    test("should navigate to workspace detail and stop it", async () => {
      await page.locator("table tr", { hasText: "node-js" }).click()

      await page
        .locator("h1", { hasText: "node-js" })
        .waitFor({ timeout: 10000 })

      await page.getByRole("button", { name: "Stop" }).click()

      await page.waitForTimeout(5000)

      // The header has: h1, provider badge, status badge - target the status badge near h1
      const headerArea = page
        .locator("h1", { hasText: "node-js" })
        .locator("..")
      await expect(headerArea).toContainText("Stopped", { timeout: 10000 })
    })

    test("can rename a workspace", async () => {
      // We're on the node-js detail page after stopping it

      await page.locator('[data-slot="workspace-rename-btn"]').click()

      const renameInput = page.locator('[data-slot="workspace-rename-input"]')
      await renameInput.waitFor({ timeout: 5000 })
      await renameInput.fill("node-js-renamed")

      await page.locator('[data-slot="workspace-rename-save"]').click()

      // ConfirmDialog warns that the existing container will be reset.
      const confirmDialog = page.locator('[data-slot="dialog-content"]')
      await confirmDialog.waitFor({ timeout: 5000 })
      await confirmDialog.getByRole("button", { name: "Rename" }).click()

      // Wait for rename to complete and navigation to new URL
      await page.waitForTimeout(4000)

      const headerArea = page.locator("h1", { hasText: "node-js-renamed" })
      await expect(headerArea).toBeVisible({ timeout: 10000 })
    })

    test("should delete workspace from detail page", async () => {
      await page.getByRole("button", { name: "More actions" }).click()
      await page.getByRole("menuitem", { name: "Delete" }).click()

      const confirmDialog = page.locator('[data-slot="dialog-content"]')
      await confirmDialog.waitFor({ timeout: 5000 })
      await confirmDialog.getByRole("button", { name: "Delete" }).click()

      await page.locator("table").waitFor({ timeout: 15000 })

      await expect(page.locator("table")).not.toContainText(
        "node-js-renamed",
        { timeout: 10000 },
      )
    })
  })

test.describe
  .serial("Workspace lifecycle - Python", () => {
    let app: ElectronApplication
    let page: Page

    test.beforeAll(async () => {
      resetMockState()
      ;({ app, page } = await launchApp())
    })

    test.afterAll(async () => {
      await app.close()
    })

    test("should create Python workspace", async () => {
      await page.click('[data-sidebar="sidebar"] a[href="#/workspaces"]')
      await page.locator("table").waitFor({ timeout: 10000 })

      await page.getByRole("button", { name: /create workspace/i }).click()

      const dialog = page.locator('[role="dialog"]').first()
      await dialog.waitFor({ timeout: 5000 })

      await dialog.locator("button", { hasText: "docker" }).first().click()
      await dialog.getByRole("button", { name: /^continue$/i }).click()

      // Scope to the Git panel because the image catalog also has a Python card.
      await dialog
        .getByTestId("source-panel")
        .locator("button", { hasText: "Python" })
        .click()
      const sourceInput = dialog.locator('input[placeholder*="github"]')
      await expect(sourceInput).toHaveValue(
        "https://github.com/microsoft/vscode-remote-try-python",
        { timeout: 5000 },
      )
      await dialog.getByRole("button", { name: /^continue$/i }).click()

      await dialog.getByRole("button", { name: /^continue$/i }).click()

      await dialog.getByRole("button", { name: /^launch$/i }).click()

      await dialog
        .getByRole("button", { name: /open workspace/i })
        .waitFor({ timeout: 15000 })

      await page.keyboard.press("Escape")
      await dialog.waitFor({ state: "hidden", timeout: 5000 })

      await page.waitForTimeout(4000)
    })

    test("should show python workspace in table", async () => {
      // Template name 'Python' -> workspace id 'python'
      await expect(page.locator("table")).toContainText("python", {
        timeout: 10000,
      })
    })

    test("should delete python workspace", async () => {
      await page.locator("table tr", { hasText: "python" }).click()

      await page
        .locator("h1", { hasText: "python" })
        .waitFor({ timeout: 10000 })

      await page.getByRole("button", { name: "More actions" }).click()
      await page.getByRole("menuitem", { name: "Delete" }).click()

      const confirmDialog = page.locator('[data-slot="dialog-content"]')
      await confirmDialog.waitFor({ timeout: 5000 })
      await confirmDialog.getByRole("button", { name: "Delete" }).click()

      // Wait for redirect to workspaces list
      await page.locator("table").waitFor({ timeout: 15000 })

      // Verify python workspace is gone
      await expect(page.locator("table")).not.toContainText("python", {
        timeout: 10000,
      })
    })
  })
