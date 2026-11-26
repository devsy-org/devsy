import type { ElectronApplication, Page } from "@playwright/test"
import { expect, test } from "@playwright/test"
import { launchApp } from "./electron-app.js"

let app: ElectronApplication
let page: Page

async function navigate(hash: string) {
  await page.evaluate((route) => {
    window.location.hash = route
  }, hash)
  await expect(
    page.getByRole("heading", { name: "Workspace Variables", exact: true }),
  ).toBeVisible()
}

test.beforeAll(async () => {
  ;({ app, page } = await launchApp())
})
test.afterAll(async () => {
  await app.close()
})

test("unified navigation uses URL tabs and replaces legacy routes", async () => {
  const sidebar = page.locator('[data-sidebar="sidebar"]')
  await sidebar.locator('a[href="#/variables"]').click()
  await expect(
    page.getByRole("tab", { name: "Secrets", exact: true }),
  ).toHaveAttribute("aria-selected", "true")
  await expect(
    sidebar.locator('a[href="#/secrets"], a[href="#/env"]'),
  ).toHaveCount(0)
  await page
    .getByRole("tab", { name: "Environment Variables", exact: true })
    .click()
  await expect(page).toHaveURL(/#\/variables\?tab=env$/)
  await navigate("/secrets")
  await expect(page).toHaveURL(/#\/variables\?tab=secrets$/)
  await navigate("/env")
  await expect(page).toHaveURL(/#\/variables\?tab=env$/)
})

test("legacy keyboard shortcuts select both variables tabs", async () => {
  await navigate("/variables?tab=env")
  const modifier = process.platform === "darwin" ? "Meta" : "Control"
  await page.keyboard.press(`${modifier}+6`)
  await expect(page).toHaveURL(/#\/variables\?tab=secrets$/)
  await page.keyboard.press(`${modifier}+7`)
  await expect(page).toHaveURL(/#\/variables\?tab=env$/)
})

test("environment values start masked and details allow explicit editing", async () => {
  await navigate("/variables?tab=env")
  const row = page.getByRole("row").filter({ hasText: "E2E_API_URL" })
  await expect(row).toContainText("••••••••")
  await expect(row).not.toContainText("https://variables.example.test/api")
  await row
    .getByRole("button", { name: "Show value for E2E_API_URL", exact: true })
    .click()
  await expect(row).toContainText("https://variables.example.test/api")
  await row
    .getByRole("button", { name: "Hide value for E2E_API_URL", exact: true })
    .click()
  await row.getByRole("button", { name: "E2E_API_URL", exact: true }).click()
  const details = page.getByRole("dialog", {
    name: "Environment variable details",
  })
  await expect(details).toBeVisible()
  await expect(details.getByLabel("Value", { exact: true })).toHaveValue(
    "https://variables.example.test/api",
  )
  await page.keyboard.press("Escape")
  await expect(details).not.toBeVisible()
})

test("secret details expose metadata and replacement uses a password input", async () => {
  await navigate("/variables?tab=secrets")
  const row = page.getByRole("row").filter({ hasText: "E2E_API_TOKEN" })
  await row.getByRole("button", { name: "E2E_API_TOKEN", exact: true }).click()
  const details = page.getByRole("dialog", { name: "Secret details" })
  await expect(details).toContainText("default")
  await expect(details).toContainText("Available")
  await details.getByRole("button", { name: "Replace secret value" }).click()
  const replacement = page.getByRole("dialog", { name: "Replace secret value" })
  await expect(
    replacement.getByLabel("New value", { exact: true }),
  ).toHaveAttribute("type", "password")
  await replacement.getByRole("button", { name: "Cancel", exact: true }).click()
  await page.keyboard.press("Escape")
})

test("add dialogs validate duplicate names and clear canceled secret input", async () => {
  await navigate("/variables?tab=secrets")
  await page.getByRole("button", { name: "Add secret", exact: true }).click()
  const secretDialog = page.getByRole("dialog", {
    name: "Add secret",
    exact: true,
  })
  await secretDialog.getByLabel("Name", { exact: true }).fill("E2E_API_TOKEN")
  await secretDialog
    .getByLabel("Value", { exact: true })
    .fill("discarded-secret")
  await expect(secretDialog).toContainText(
    "A secret with this name already exists",
  )
  await expect(
    secretDialog.getByRole("button", { name: "Save", exact: true }),
  ).toBeDisabled()
  await secretDialog
    .getByRole("button", { name: "Cancel", exact: true })
    .click()
  await page.getByRole("button", { name: "Add secret", exact: true }).click()
  await expect(secretDialog.getByLabel("Value", { exact: true })).toHaveValue(
    "",
  )
  await secretDialog
    .getByRole("button", { name: "Cancel", exact: true })
    .click()
  await page
    .getByRole("tab", { name: "Environment Variables", exact: true })
    .click()
  await page.getByRole("button", { name: "Add variable", exact: true }).click()
  const envDialog = page.getByRole("dialog", {
    name: "Add environment variable",
  })
  await envDialog.getByLabel("Name", { exact: true }).fill("E2E_API_URL")
  await expect(envDialog).toContainText(
    "A variable with this name already exists",
  )
  await expect(
    envDialog.getByRole("button", { name: "Save", exact: true }),
  ).toBeDisabled()
  await envDialog.getByRole("button", { name: "Cancel", exact: true }).click()
})
