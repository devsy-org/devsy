import type { ElectronApplication, Page } from "@playwright/test"
import { expect, test } from "@playwright/test"
import { launchApp, resetMockState } from "./electron-app.js"

// ---------------------------------------------------------------------------
// Flow 1 — Provider CRUD
// ---------------------------------------------------------------------------
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

      // Click Delete in the sheet
      await sheet.getByRole("button", { name: "Delete" }).click()

      // ConfirmDialog appears — click its destructive Delete button
      const confirmDialog = page.locator('[data-slot="dialog-content"]')
      await confirmDialog.waitFor({ timeout: 5000 })
      await confirmDialog.getByRole("button", { name: "Delete" }).click()

      // Wait for both dialogs to close
      await sheet.waitFor({ state: "hidden", timeout: 10000 })

      // Wait for watcher to poll updated state
      await page.waitForTimeout(4000)

      // Verify docker is gone
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

      // Wait for the Add Provider page heading
      await page
        .locator("h1", { hasText: "Add Provider" })
        .waitFor({ timeout: 5000 })

      // Click docker preset card
      await page
        .locator("button", { hasText: "docker" })
        .filter({ hasText: "Local Docker containers" })
        .click()

      // doAdd() calls providerAdd, providerUse, then goto('/providers?setup=docker')
      // Wait for redirect back to providers page
      await page
        .locator('[data-slot="sidebar-inset"] h1', { hasText: /providers/i })
        .waitFor({ timeout: 10000 })

      // Wait for the watcher to pick up the new provider
      await page.waitForTimeout(4000)

      // Verify docker appears in provider cards
      const main = page.locator('[data-slot="sidebar-inset"] main')
      await expect(main).toContainText("docker", { timeout: 10000 })

      // The setup flow auto-opens the ProviderSheet — close it if open
      const setupSheet = page.locator('[data-slot="sheet-content"]')
      if (await setupSheet.isVisible()) {
        await page.keyboard.press("Escape")
        await setupSheet.waitFor({ state: "hidden", timeout: 5000 })
      }
    })

    test("should rename docker provider to my-docker", async () => {
      const main = page.locator('[data-slot="sidebar-inset"] main')
      await main.locator("button", { hasText: "docker" }).first().click()

      const sheet = page.locator('[data-slot="sheet-content"]')
      await sheet.waitFor({ timeout: 5000 })

      // Click Rename button in the sheet header
      await sheet.getByRole("button", { name: "Rename" }).click()

      // Fill the rename input
      const renameInput = sheet.locator("input").first()
      await renameInput.fill("my-docker")

      // Click Save in the rename form (the first Save button in the sheet header)
      await sheet.getByRole("button", { name: "Save" }).first().click()

      // Sheet closes after successful rename
      await sheet.waitFor({ state: "hidden", timeout: 10000 })

      // Wait for watcher
      await page.waitForTimeout(4000)

      // Verify 'my-docker' appears
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

      await page
        .locator("h1", { hasText: "Add Provider" })
        .waitFor({ timeout: 5000 })

      await page
        .locator("button", { hasText: "docker" })
        .filter({ hasText: "Local Docker containers" })
        .click()

      await page
        .locator('[data-slot="sidebar-inset"] h1', { hasText: /providers/i })
        .waitFor({ timeout: 10000 })

      await page.waitForTimeout(4000)

      const main = page.locator('[data-slot="sidebar-inset"] main')
      await expect(main).toContainText("docker", { timeout: 10000 })

      // Close setup sheet if it auto-opened
      const setupSheet = page.locator('[data-slot="sheet-content"]')
      if (await setupSheet.isVisible()) {
        await page.keyboard.press("Escape")
        await setupSheet.waitFor({ state: "hidden", timeout: 5000 })
      }
    })
  })

// ---------------------------------------------------------------------------
// Flow 2 — Workspace lifecycle (Node.js)
// ---------------------------------------------------------------------------
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

      const sheet = page.locator('[role="dialog"]')
      await sheet.waitFor({ timeout: 5000 })

      // Click Node.js template
      await sheet.locator("button", { hasText: "Node.js" }).click()

      // Verify source input is populated
      const sourceInput = sheet.locator('input[placeholder*="github"]')
      await expect(sourceInput).toHaveValue(
        "https://github.com/microsoft/vscode-remote-try-node",
        { timeout: 5000 },
      )

      // Select IDE = None
      await sheet.getByRole("button", { name: /select an ide/i }).click()
      await page.locator('[role="option"]', { hasText: "None" }).click()

      // Submit
      await sheet.getByRole("button", { name: /create workspace/i }).click()

      // Wait for output
      await expect(sheet).toContainText("Output", { timeout: 15000 })
      await expect(sheet).toContainText(/resolving|pulling|starting|ready/i, {
        timeout: 15000,
      })

      // Wait for "Open Workspace" success button
      await sheet
        .getByRole("button", { name: /open workspace/i })
        .waitFor({ timeout: 15000 })

      // Close the sheet
      await page.keyboard.press("Escape")
      await sheet.waitFor({ state: "hidden", timeout: 5000 })

      // Wait for watcher to pick up the new workspace
      await page.waitForTimeout(4000)
    })

    test("should show new workspace in table", async () => {
      // Workspace ID from template name: 'Node.js' → 'node-js'
      await expect(page.locator("table")).toContainText("node-js", {
        timeout: 10000,
      })
    })

    test("should navigate to workspace detail and stop it", async () => {
      // Click the workspace row
      await page.locator("table tr", { hasText: "node-js" }).click()

      // Wait for detail page
      await page
        .locator("h1", { hasText: "node-js" })
        .waitFor({ timeout: 10000 })

      // Click Stop
      await page.getByRole("button", { name: "Stop" }).click()

      // Wait for stop operation to complete
      await page.waitForTimeout(5000)

      // Verify the status badge in the header shows "Stopped"
      // The header has: h1, provider badge, status badge — target the status badge near h1
      const headerArea = page
        .locator("h1", { hasText: "node-js" })
        .locator("..")
      await expect(headerArea).toContainText("Stopped", { timeout: 10000 })
    })

    test("should delete workspace from detail page", async () => {
      // Open the More actions dropdown, then click Delete
      await page.getByRole("button", { name: "More actions" }).click()
      await page.getByRole("menuitem", { name: "Delete" }).click()

      // ConfirmDialog appears — click the confirm Delete in the dialog
      const confirmDialog = page.locator('[data-slot="dialog-content"]')
      await confirmDialog.waitFor({ timeout: 5000 })
      await confirmDialog.getByRole("button", { name: "Delete" }).click()

      // Navigates to /workspaces on success — wait for table
      await page.locator("table").waitFor({ timeout: 15000 })

      // Verify node-js is gone
      await expect(page.locator("table")).not.toContainText("node-js", {
        timeout: 10000,
      })
    })
  })

// ---------------------------------------------------------------------------
// Flow 3 — Workspace lifecycle (Python)
// ---------------------------------------------------------------------------
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

      const sheet = page.locator('[role="dialog"]')
      await sheet.waitFor({ timeout: 5000 })

      // Click Python template
      await sheet.locator("button", { hasText: "Python" }).click()

      // Verify source populated
      const sourceInput = sheet.locator('input[placeholder*="github"]')
      await expect(sourceInput).toHaveValue(
        "https://github.com/microsoft/vscode-remote-try-python",
        { timeout: 5000 },
      )

      // Select IDE = None
      await sheet.getByRole("button", { name: /select an ide/i }).click()
      await page.locator('[role="option"]', { hasText: "None" }).click()

      // Submit
      await sheet.getByRole("button", { name: /create workspace/i }).click()

      // Wait for success
      await sheet
        .getByRole("button", { name: /open workspace/i })
        .waitFor({ timeout: 15000 })

      // Close sheet
      await page.keyboard.press("Escape")
      await sheet.waitFor({ state: "hidden", timeout: 5000 })

      await page.waitForTimeout(4000)
    })

    test("should show python workspace in table", async () => {
      // Template name 'Python' → workspace id 'python'
      await expect(page.locator("table")).toContainText("python", {
        timeout: 10000,
      })
    })

    test("should delete python workspace", async () => {
      // Click workspace row
      await page.locator("table tr", { hasText: "python" }).click()

      // Wait for detail page
      await page
        .locator("h1", { hasText: "python" })
        .waitFor({ timeout: 10000 })

      // Open the More actions dropdown, then click Delete
      await page.getByRole("button", { name: "More actions" }).click()
      await page.getByRole("menuitem", { name: "Delete" }).click()

      // Confirm in the dialog
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
