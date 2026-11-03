import type { ElectronApplication } from "@playwright/test"
import { expect, test } from "@playwright/test"
import { launchApp, resetMockState } from "./electron-app.js"

for (const [kind, source] of [
  ["git", "https://example.com/Backend.git"],
  ["local", "/var/home/dev/Projects/Backend"],
]) {
  test(`validates explicit IDs and preserves the ${kind} source`, async () => {
    resetMockState()
    let app: ElectronApplication | undefined
    try {
      const launched = await launchApp()
      app = launched.app
      const page = launched.page
      await page.click('[data-sidebar="sidebar"] a[href="#/workspaces"]')
      await page
        .getByRole("button", { name: "Create Workspace", exact: true })
        .click()
      const dialog = page.getByRole("dialog")
      await dialog
        .getByRole("button", { name: "Continue", exact: true })
        .click()
      if (kind === "local")
        await dialog.getByText("Local Directory", { exact: true }).click()
      await dialog
        .getByLabel(
          kind === "local" ? "Local Directory *" : "Repository URL *",
          { exact: true },
        )
        .fill(source)
      await dialog
        .getByRole("button", { name: "Continue", exact: true })
        .click()
      await dialog
        .getByRole("button", { name: "Continue", exact: true })
        .click()
      const name = dialog.getByLabel("Workspace ID", { exact: true })
      await expect(name).toHaveValue("backend")
      await expect(dialog.getByText(source, { exact: true })).toBeVisible()
      await name.fill("Backend")
      await expect(name).toHaveValue("Backend")
      await expect(name).toHaveAttribute("aria-invalid", "true")
      await expect(name).toHaveAttribute(
        "aria-describedby",
        "workspace-name-invalid",
      )
      await expect(dialog.locator("#workspace-name-invalid")).toContainText(
        'Try "backend"',
      )
      const launch = dialog.getByRole("button", { name: "Launch", exact: true })
      await expect(launch).toBeDisabled()
      await name.fill("backend")
      await expect(launch).toBeEnabled()
      await launch.click()
      await expect
        .poll(async () =>
          page.evaluate(async () => {
            const snapshot = (await window.electronAPI.invoke(
              "workspace_snapshot",
            )) as {
              workspaces: Array<{
                id: string
                source?: { gitRepository?: string }
              }>
              jobs: Record<string, { state: string }>
            }
            const workspace = snapshot.workspaces.find(
              (entry) => entry.id === "backend",
            )
            return {
              state: snapshot.jobs.backend?.state,
              source: workspace?.source?.gitRepository,
            }
          }),
        )
        .toEqual({ state: "succeeded", source })
    } finally {
      await app?.close()
    }
  })
}
