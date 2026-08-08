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

test.describe("Workspace lifecycle badges", () => {
  const api = async (channel: string, args: Record<string, unknown>) =>
    page.evaluate(
      ([c, a]) =>
        (
          window as unknown as {
            electronAPI: {
              invoke: (
                c: string,
                a?: Record<string, unknown>,
              ) => Promise<unknown>
            }
          }
        ).electronAPI.invoke(c as string, a as Record<string, unknown>),
      [channel, args] as const,
    )

  test("shows a Deleting badge while removal is in flight, then removes the row", async () => {
    const main = page.locator('[data-slot="sidebar-inset"] main')

    try {
      await api("workspace_up", {
        source: "https://example.com/deleteprobe.git",
        workspaceId: "deleteprobe",
      })
      await expect(main.locator("text=deleteprobe")).toBeVisible({
        timeout: 5000,
      })

      // Not awaited: the assertions below run while the delete is in flight.
      void api("workspace_delete", { workspaceId: "deleteprobe" }).catch(
        () => undefined,
      )

      await expect(main).toContainText("Deleting", { timeout: 3000 })
      await expect(main.locator("text=deleteprobe")).not.toBeVisible({
        timeout: 5000,
      })
    } finally {
      // Mock CLI state is shared across specs, so don't leave this behind.
      await api("workspace_delete", { workspaceId: "deleteprobe" })
    }
  })
})

test.describe("Workspace detail flow", () => {
  test.beforeEach(async () => {
    await page.click('[data-sidebar="sidebar"] a[href="#/workspaces"]')
    await page.locator("table").waitFor({ timeout: 10000 })
  })

  const api = async (channel: string, args: Record<string, unknown>) =>
    page.evaluate(
      ([c, a]) =>
        (
          window as unknown as {
            electronAPI: {
              invoke: (
                c: string,
                a?: Record<string, unknown>,
              ) => Promise<unknown>
            }
          }
        ).electronAPI.invoke(c as string, a as Record<string, unknown>),
      [channel, args] as const,
    )

  test("supports a start/stop playthrough from the workspaces list", async () => {
    const workspaceName = "flow-playthrough"
    await api("workspace_up", {
      source: "https://example.com/flow-playthrough.git",
      workspaceId: workspaceName,
    })

    await expect(page.locator("table")).toContainText(workspaceName, {
      timeout: 10000,
    })

    await page
      .locator("tbody tr")
      .filter({ hasText: workspaceName })
      .first()
      .click()

    await expect(
      page.getByRole("heading", { name: workspaceName }),
    ).toBeVisible({ timeout: 10000 })

    const main = page.locator('[data-slot="sidebar-inset"] main')
    await expect(main).toContainText("Running")

    await page.getByRole("button", { name: /^stop$/i }).click()
    await expect(main).toContainText("Stopping", { timeout: 3000 })
    await expect(main).toContainText("Stopped", { timeout: 10000 })

    await page.getByRole("button", { name: /^start$/i }).click()
    await expect(main).toContainText("Starting", { timeout: 3000 })
    await expect(main).toContainText("Running", { timeout: 10000 })
  })

  test("shows rebuild and delete confirmation flows from the detail page", async () => {
    const workspaceName = "flow-delete-rebuild"
    await api("workspace_up", {
      source: "https://example.com/flow-delete-rebuild.git",
      workspaceId: workspaceName,
    })

    await page.locator("table").waitFor({ timeout: 10000 })
    await expect(
      page.locator("tbody tr").filter({ hasText: workspaceName }).first(),
    ).toBeVisible({ timeout: 10000 })

    await page
      .locator("tbody tr")
      .filter({ hasText: workspaceName })
      .first()
      .click()

    await expect(
      page.getByRole("heading", { name: workspaceName }),
    ).toBeVisible({ timeout: 10000 })

    await page.getByRole("button", { name: /more actions/i }).click()
    await page.getByRole("menuitem", { name: /rebuild/i }).click()

    const rebuildDialog = page.locator('[role="dialog"]').filter({ hasText: /rebuild workspace/i }).first()
    await expect(rebuildDialog).toBeVisible({ timeout: 5000 })
    await expect(rebuildDialog).toContainText("Rebuild workspace")
    await rebuildDialog.getByRole("button", { name: /^cancel$/i }).click()

    await page.getByRole("button", { name: /more actions/i }).click()
    await page.getByRole("menuitem", { name: /delete/i }).click()

    const deleteDialog = page.locator('[role="dialog"]').filter({ hasText: /delete workspace/i }).first()
    await expect(deleteDialog).toBeVisible({ timeout: 5000 })
    await expect(deleteDialog).toContainText("Delete workspace")
    await deleteDialog.getByRole("button", { name: /^cancel$/i }).click()
  })
})

test.describe.serial("Create Workspace Wizard", () => {
  async function openCreateWorkspaceWizard(page: Page) {
    await page.click('[data-sidebar="sidebar"] a[href="#/workspaces"]')
    await page.locator('[data-slot="sidebar-inset"] main').first().waitFor({
      timeout: 30_000,
    })

    // Support both old/new CTA labels.
    const createWorkspaceButton = page
      .getByRole("button", { name: /create workspace|new workspace/i })
      .first()

    await expect(createWorkspaceButton).toBeVisible({ timeout: 30_000 })
    await expect(createWorkspaceButton).toBeEnabled({ timeout: 30_000 })
    await createWorkspaceButton.click()

    const dialog = page.locator('[role="dialog"]').first()
    await expect(dialog).toBeVisible({ timeout: 10_000 })
    return dialog
  }

  test("should open the wizard and show step 1 (provider)", async () => {
    const dialog = await openCreateWorkspaceWizard(page)

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

    // The default provider is pre-selected, so Continue is enabled
    const continueBtn = dialog.getByRole("button", { name: /^continue$/i })
    await expect(continueBtn).toBeEnabled()
  })

  test("should advance to source step with templates", async () => {
    const dialog = page.locator('[role="dialog"]').first()
    // Default provider (docker) is already pre-selected; just continue
    const continueBtn = dialog.getByRole("button", { name: /^continue$/i })
    await expect(continueBtn).toBeEnabled()
    await continueBtn.click()

    await expect(
      dialog.getByRole("heading", { name: /choose a source/i }),
    ).toBeVisible()

    // Quick Start Templates section + 5 core templates. Scope to the active
    // source panel (Git) so the template buttons don't collide with the Image
    // source's catalog entries (e.g. "Python 3.12"), which share language names.
    await expect(dialog).toContainText("Quick Start Templates")
    const gitPanel = dialog.getByTestId("source-panel")
    for (const lang of ["Python", "Node.js", "Go", "Rust", "Java"]) {
      await expect(gitPanel.locator("button", { hasText: lang })).toBeVisible()
    }

    // Language icons render
    const icons = gitPanel.locator("button img")
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
    await dialog.getByTestId("source-panel").locator("button", { hasText: "Python" }).click()

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

    // Mock CLI streams: "Resolving source", "Pulling image",
    // "Starting workspace", "Workspace ready."
    await expect(dialog).toContainText(/resolving|pulling|starting|ready/i, {
      timeout: 10000,
    })

    // On success the "Open Workspace" button appears
    await expect(
      dialog.getByRole("button", { name: /open workspace/i }),
    ).toBeVisible({ timeout: 15000 })
  })
})
