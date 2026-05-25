import type { ElectronApplication, Page } from "@playwright/test"
import { expect, test } from "@playwright/test"
import { launchApp, resetMockState } from "./electron-app.js"

let app: ElectronApplication
let page: Page

test.beforeAll(async () => {
  resetMockState()
  ;({ app, page } = await launchApp())
  // Navigate to workspaces and wait for data to load from mock CLI
  await page.click('[data-sidebar="sidebar"] a[href="#/workspaces"]')
  await page
    .locator('[data-slot="sidebar-inset"] h1')
    .first()
    .waitFor({ timeout: 5000 })
  // Wait for the mock CLI data to populate via the watcher
  await page.locator("table").waitFor({ timeout: 10000 })
})

test.afterAll(async () => {
  await app.close()
})

test.describe("Workspaces Page", () => {
  test("should list workspaces from CLI with correct names", async () => {
    const table = page.locator("table")
    await expect(table).toBeVisible()
    // Mock CLI returns "test-workspace" and "dev-env"
    await expect(table).toContainText("test-workspace")
    await expect(table).toContainText("dev-env")
  })

  test("should show provider names for each workspace", async () => {
    const table = page.locator("table")
    await expect(table).toContainText("docker")
    await expect(table).toContainText("kubernetes")
  })

  test("should show workspace status badges", async () => {
    const table = page.locator("table")
    await expect(table).toContainText("Running")
    await expect(table).toContainText("Stopped")
  })
})

test.describe.serial("Create Workspace Wizard", () => {
  test("should open the wizard and show step 1 (provider)", async () => {
    await page.getByRole("button", { name: /create workspace/i }).click()
    const dialog = page.locator('[role="dialog"]').first()
    await expect(dialog).toBeVisible({ timeout: 5000 })

    // Step indicator labels — all 5 steps present
    for (const label of ["Provider", "Source", "IDE", "Review", "Launch"]) {
      await expect(dialog).toContainText(label)
    }

    // Provider step heading visible
    await expect(
      dialog.getByRole("heading", { name: /choose a provider/i }),
    ).toBeVisible()

    // Mock CLI exposes "docker" as the only initialized provider; ensure it is listed
    await expect(dialog.locator("button", { hasText: "docker" })).toBeVisible({
      timeout: 10000,
    })

    // Continue is disabled until a provider is selected
    const continueBtn = dialog.getByRole("button", { name: /^continue$/i })
    await expect(continueBtn).toBeDisabled()
  })

  test("should advance to source step with templates", async () => {
    const dialog = page.locator('[role="dialog"]').first()
    // Select the docker provider (the initialized one from the mock)
    await dialog.locator("button", { hasText: "docker" }).first().click()

    const continueBtn = dialog.getByRole("button", { name: /^continue$/i })
    await expect(continueBtn).toBeEnabled()
    await continueBtn.click()

    await expect(
      dialog.getByRole("heading", { name: /choose a source/i }),
    ).toBeVisible()

    // Quick Start Templates section + 5 core templates
    await expect(dialog).toContainText("Quick Start Templates")
    for (const lang of ["Python", "Node.js", "Go", "Rust", "Java"]) {
      await expect(dialog.locator("button", { hasText: lang })).toBeVisible()
    }

    // Language icons render
    const icons = dialog.locator("button img")
    expect(await icons.count()).toBeGreaterThan(0)
    const firstIcon = icons.first()
    await expect(firstIcon).toBeVisible()
    const naturalWidth = await firstIcon.evaluate(
      (el: HTMLImageElement) => el.naturalWidth,
    )
    expect(naturalWidth).toBeGreaterThan(0)
  })

  test("should select a template and populate the source field", async () => {
    const dialog = page.locator('[role="dialog"]').first()
    await dialog.locator("button", { hasText: "Python" }).click()

    const sourceInput = dialog.locator('input[placeholder*="github"]')
    await expect(sourceInput).toHaveValue(
      "https://github.com/microsoft/vscode-remote-try-python",
    )
  })

  test("should walk through IDE step", async () => {
    const dialog = page.locator('[role="dialog"]').first()
    // Continue from source -> IDE
    const continueBtn = dialog.getByRole("button", { name: /^continue$/i })
    await expect(continueBtn).toBeEnabled()
    await continueBtn.click()

    await expect(
      dialog.getByRole("heading", { name: /choose an ide/i }),
    ).toBeVisible()

    // IDE combobox trigger (the popover button) defaults to "Select an IDE..." or "None"
    // Since the default state has selectedIde = "none", the label should be "None".
    await expect(dialog).toContainText("None")

    // Continue is always enabled here (IDE is optional); advance to Review
    await dialog.getByRole("button", { name: /^continue$/i }).click()
  })

  test("should show review summary", async () => {
    const dialog = page.locator('[role="dialog"]').first()
    await expect(
      dialog.getByRole("heading", { name: /^review$/i }),
    ).toBeVisible()

    // Summary card shows chosen provider, source, ide label, workspace id
    await expect(dialog).toContainText("docker")
    await expect(dialog).toContainText(
      "https://github.com/microsoft/vscode-remote-try-python",
    )
    await expect(dialog).toContainText("None")

    // Workspace name was populated by selectTemplate("Python") -> "python"
    const nameInput = dialog.locator(
      'input[placeholder*="derived from source"]',
    )
    await expect(nameInput).toHaveValue("python")
  })

  test("should launch workspace and stream output", async () => {
    const dialog = page.locator('[role="dialog"]').first()
    // The review step's primary button is labeled "Launch"
    await dialog.getByRole("button", { name: /^launch$/i }).click()

    // Mock CLI streams: "Resolving source...", "Pulling image...",
    // "Starting workspace...", "Workspace ready."
    await expect(dialog).toContainText(/resolving|pulling|starting|ready/i, {
      timeout: 10000,
    })

    // On success the "Open Workspace" button appears
    await expect(
      dialog.getByRole("button", { name: /open workspace/i }),
    ).toBeVisible({ timeout: 15000 })
  })
})
