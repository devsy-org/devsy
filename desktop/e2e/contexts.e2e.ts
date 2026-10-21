import { readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import type { ElectronApplication, Page } from "@playwright/test"
import { expect, test } from "@playwright/test"
import { launchApp, resetMockState } from "./electron-app.js"

let app: ElectronApplication
let page: Page

test.beforeAll(async () => {
  resetMockState()
  ;({ app, page } = await launchApp())
  await page.click('[data-sidebar="sidebar"] a[href="#/contexts"]')
  await page
    .locator('[data-slot="sidebar-inset"] h1')
    .first()
    .waitFor({ timeout: 5000 })
})

test.afterAll(async () => {
  await app.close()
})

test.describe("Contexts Page", () => {
  test("should show contexts heading", async () => {
    const heading = page.locator('[data-slot="sidebar-inset"] h1').first()
    await expect(heading).toContainText(/contexts/i)
  })

  test("should list contexts from CLI data", async () => {
    const main = page.locator('[data-slot="sidebar-inset"] main')
    // Mock CLI returns "default" and "staging" contexts
    await expect(main).toContainText("default", { timeout: 10000 })
    await expect(main).toContainText("staging", { timeout: 10000 })
  })

  test("should show active badge on the default context", async () => {
    const main = page.locator('[data-slot="sidebar-inset"] main')
    await expect(main).toContainText("active", { timeout: 10000 })
  })

  test("should open context settings sheet with loaded options", async () => {
    const main = page.locator('[data-slot="sidebar-inset"] main')
    // Click on the default context to open settings
    await main.locator("button", { hasText: "default" }).first().click()

    const sheet = page.locator('[role="dialog"]')
    await expect(sheet).toBeVisible({ timeout: 5000 })

    // The context sheet should load options from the mock CLI
    // Wait for "Loading options..." to disappear (options loaded)
    await expect(sheet.getByText("Loading options...")).toBeHidden({
      timeout: 10000,
    })

    // Should show option sections
    await expect(sheet).toContainText("General", { timeout: 5000 })
    await expect(sheet).toContainText("SSH", { timeout: 5000 })
    await expect(sheet).toContainText("Credential Forwarding", {
      timeout: 5000,
    })
    await expect(sheet).toContainText("Dotfiles", { timeout: 5000 })
  })

  test("should show toggle switches for boolean options", async () => {
    const sheet = page.locator('[role="dialog"]')
    // Should have toggle switches for boolean options like Telemetry, SSH Agent Forwarding, etc.
    const switches = sheet.locator('button[role="switch"]')
    const switchCount = await switches.count()
    expect(switchCount).toBeGreaterThanOrEqual(5) // At least: telemetry, docker creds, git creds, ssh agent, etc.
  })
  test("saves and reloads independent GitHub forwarding and retains edits on failed save", async () => {
    const sheet = page.getByRole("dialog")
    const github = sheet.getByRole("switch", { name: "GitHub CLI Credentials" })
    await expect(github).toBeVisible()
    await expect(github).toHaveAttribute("aria-checked", "false")
    await github.click()
    await sheet.getByRole("button", { name: "Save", exact: true }).click()
    await expect(sheet.getByText("Unsaved changes")).toBeHidden()
    await page.keyboard.press("Escape")
    await page
      .locator('[data-slot="sidebar-inset"] main')
      .getByRole("button", { name: "default" })
      .first()
      .click()
    await expect(github).toHaveAttribute("aria-checked", "true")
    const statePath = join(tmpdir(), "devsy-mock-state.json")
    const state = JSON.parse(readFileSync(statePath, "utf8"))
    expect(state.contextOptions.default).toEqual({
      SSH_INJECT_GITHUB_CREDENTIALS: { value: "true" },
    })
    state.failContextSave = true
    writeFileSync(statePath, JSON.stringify(state))
    try {
      await github.click()
      await sheet.getByRole("button", { name: "Save", exact: true }).click()
      await expect(page.getByText(/Failed to save:/)).toBeVisible()
      await expect(sheet.getByText("Unsaved changes")).toBeVisible()
      expect(
        JSON.parse(readFileSync(statePath, "utf8")).contextOptions.default
          .SSH_INJECT_GITHUB_CREDENTIALS.value,
      ).toBe("true")
      await sheet.getByRole("button", { name: "Reset", exact: true }).click()
      await expect(github).toHaveAttribute("aria-checked", "true")
      await github.scrollIntoViewIfNeeded()
      await github
        .locator("..")
        .screenshot({ path: "test-results/github-cli-context-row.png" })
      await page.screenshot({
        path: "test-results/github-cli-context.png",
        fullPage: true,
      })
    } finally {
      const latest = JSON.parse(readFileSync(statePath, "utf8"))
      latest.failContextSave = false
      writeFileSync(statePath, JSON.stringify(latest))
    }
  })
})
