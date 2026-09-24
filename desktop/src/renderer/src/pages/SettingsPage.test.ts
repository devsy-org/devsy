import { render, screen } from "@testing-library/svelte"
import { afterEach, describe, expect, it, vi } from "vitest"

vi.mock("$lib/components/update/UpdatesPanel.svelte", () => ({
  default: vi.fn(),
}))

import SettingsPage from "./SettingsPage.svelte"

describe("SettingsPage layout", () => {
  afterEach(() => {
    document.body.innerHTML = ""
  })

  it("renders the startup toggles with the dependent open-to-tray switch disabled", async () => {
    render(SettingsPage)

    const section = document.querySelector("#startup")
    expect(section).toBeTruthy()
    const switches = section!.querySelectorAll('[role="switch"]')
    expect(switches).toHaveLength(2)
    const [runAtStartup, openToTray] = switches
    expect(runAtStartup.hasAttribute("disabled")).toBe(false)
    await vi.waitFor(() => {
      expect(openToTray.hasAttribute("disabled")).toBe(true)
    })
  })

  it("renders the notification level select", () => {
    render(SettingsPage)

    expect(screen.getByText("Workspace Notifications")).toBeTruthy()
    expect(screen.getByText("Failures only")).toBeTruthy()
  })

  it("renders all settings sections as one page without section navigation links", () => {
    render(SettingsPage)

    expect(screen.queryByRole("navigation", { name: "Settings sections" })).toBeNull()
    expect(document.querySelectorAll('a[href^="#"]')).toHaveLength(0)

    for (const name of ["General", "Startup", "Notifications", "Appearance", "Updates", "Advanced"]) {
      expect(screen.getByRole("heading", { name, level: 2 })).toBeTruthy()
    }
  })
})
