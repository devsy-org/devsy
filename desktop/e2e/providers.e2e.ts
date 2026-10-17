import type { ElectronApplication, Page } from "@playwright/test"
import { expect, test } from "@playwright/test"
import { launchApp, resetMockState } from "./electron-app.js"

let app: ElectronApplication
let page: Page

test.beforeAll(async () => {
  resetMockState()
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
    await expect(main).toContainText("Default")
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

  test("searches installed sources and clears an unmatched query", async () => {
    const search = page.getByRole("textbox", {
      name: "Search installed providers",
    })
    await search.fill("devsy-provider-kubernetes")
    const main = page.locator('[data-slot="sidebar-inset"] main')
    await expect(
      main.getByRole("button", { name: /kubernetes icon/ }),
    ).toBeVisible()
    await expect(main.getByRole("button", { name: /docker icon/ })).toHaveCount(
      0,
    )
    await search.fill("no-provider-matches-this")
    await expect(
      main.getByText("No providers match your search."),
    ).toBeVisible()
    await main.getByRole("button", { name: "Clear search" }).click()
    await expect(search).toHaveValue("")
    await expect(
      main.getByRole("button", { name: /docker icon/ }),
    ).toBeVisible()
  })

  test("discovers additional providers and reviews optional settings before initialization", async () => {
    const window = await app.browserWindow(page)
    const bounds = await window.evaluate((browserWindow) =>
      browserWindow.getBounds(),
    )
    try {
      await window.evaluate((browserWindow) =>
        browserWindow.setBounds({ width: 1000, height: 700 }),
      )
      await page
        .getByRole("button", { name: "Add Provider", exact: true })
        .click()
      const wizard = page.getByRole("dialog", {
        name: "Add Provider",
        exact: true,
      })
      await expect(
        wizard.getByRole("list", { name: "Provider setup steps" }),
      ).toBeVisible()
      await wizard
        .getByLabel("Provider source", { exact: true })
        .fill("https://example.test/provider.yaml")
      await wizard
        .getByRole("button", { name: "Continue", exact: true })
        .click()
      await expect(
        wizard.getByText("Enter an installation name for this custom source."),
      ).toBeVisible()
      const terraform = wizard.getByRole("button", { name: /terraform/ })
      await terraform.scrollIntoViewIfNeeded()
      await terraform.click()
      await expect(terraform).toHaveAttribute("aria-pressed", "true")
      await wizard.getByLabel("Name (optional)").fill("catalog-probe")
      const next = wizard.getByRole("button", { name: "Continue", exact: true })
      await next.focus()
      expect(
        await next.evaluate((element) => {
          const rect = element.getBoundingClientRect()
          const hit = document.elementFromPoint(
            rect.x + rect.width / 2,
            rect.y + rect.height / 2,
          )
          return (
            rect.top >= 0 &&
            rect.bottom <= innerHeight &&
            (hit === element || element.contains(hit))
          )
        }),
      ).toBe(true)
      await next.click()
      await expect(
        wizard.getByRole("heading", { name: "Configure catalog-probe" }),
      ).toBeVisible()
      await expect(wizard.getByLabel("Docker Host")).toHaveValue(
        "unix:///var/run/docker.sock",
      )
      await expect(wizard.getByLabel("Disk Size")).toHaveValue("30")
      await wizard.getByLabel("Disk Size").fill("32")
      await wizard
        .getByRole("button", { name: "Continue", exact: true })
        .click()
      await expect(
        wizard.getByText("catalog-probe is ready to use."),
      ).toBeVisible({ timeout: 15000 })
      await wizard.getByRole("button", { name: "Done", exact: true }).click()
      await expect(wizard).toBeHidden()
      await expect(
        page
          .locator('[data-slot="sidebar-inset"] main')
          .getByRole("button", { name: /catalog-probe/ }),
      ).toBeVisible()
      await expect(
        page
          .locator('[data-slot="sidebar-inset"] main')
          .getByRole("button", { name: /docker icon docker Default/ }),
      ).toBeVisible()
    } finally {
      const dialog = page.getByRole("dialog")
      if (await dialog.isVisible()) await page.keyboard.press("Escape")
      await page.evaluate(async () => {
        await (
          window as unknown as {
            electronAPI: {
              invoke: (
                channel: string,
                args: Record<string, unknown>,
              ) => Promise<unknown>
            }
          }
        ).electronAPI.invoke("provider_delete", { name: "catalog-probe" })
      })
      await window.evaluate(
        (browserWindow, previousBounds) =>
          browserWindow.setBounds(previousBounds),
        bounds,
      )
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

test.describe("Provider lifecycle badges", () => {
  test("never shows 'not initialized' while add+init is in flight", async () => {
    const main = page.locator('[data-slot="sidebar-inset"] main')

    await page.evaluate(async () => {
      const api = (
        window as unknown as {
          electronAPI: {
            invoke: (c: string, a?: Record<string, unknown>) => Promise<unknown>
          }
        }
      ).electronAPI
      await api.invoke("provider_delete", { name: "lifecycleprobe" })
      await api.invoke("provider_add", { name: "lifecycleprobe" })
      void api.invoke("provider_init", { name: "lifecycleprobe" })
    })

    try {
      const card = main.locator("button", { hasText: "lifecycleprobe" }).first()
      await expect(card).toBeVisible({ timeout: 10000 })

      let sawBusy = false
      let settled = false
      const deadline = Date.now() + 8000
      while (Date.now() < deadline) {
        const text = ((await card.textContent()) ?? "").toLowerCase()
        expect(text).not.toContain("not initialized")
        if (/installing|initializing/.test(text)) sawBusy = true
        // Reached only once the busy label is replaced by the settled badge.
        if (sawBusy && /(^|\s)initialized/.test(text)) {
          settled = true
          break
        }
        await page.waitForTimeout(100)
      }

      expect(sawBusy, "expected a busy badge during install/init").toBe(true)
      expect(settled, "expected the card to settle as initialized").toBe(true)
    } finally {
      // Mock CLI state is shared across specs, so don't leave this behind.
      await page.evaluate(async () => {
        await (
          window as unknown as {
            electronAPI: {
              invoke: (
                c: string,
                a?: Record<string, unknown>,
              ) => Promise<unknown>
            }
          }
        ).electronAPI.invoke("provider_delete", { name: "lifecycleprobe" })
      })
    }
  })

  // An abandoned wizard (skip init, or close mid-flow) leaves the install's
  // job open. Without an explicit release the card spins on "installing..."
  // forever, since nothing else will ever finish that job.
  test("releases the job when a provider is added but never initialized", async () => {
    const main = page.locator('[data-slot="sidebar-inset"] main')

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

    await api("provider_delete", { name: "skipprobe" })
    await api("provider_add", { name: "skipprobe" })

    try {
      const card = main.locator("button", { hasText: "skipprobe" }).first()
      await expect(card).toContainText(/installing/i, { timeout: 10000 })

      // What the wizard does on skip/close.
      await api("provider_release_job", { name: "skipprobe" })

      // Settles to the uninitialized state rather than staying busy.
      await expect(card).toContainText(/not initialized/i, { timeout: 10000 })
    } finally {
      await api("provider_delete", { name: "skipprobe" })
    }
  })
})
