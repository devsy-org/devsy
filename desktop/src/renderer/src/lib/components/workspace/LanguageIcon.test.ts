import { cleanup, render } from "@testing-library/svelte"
import { tick } from "svelte"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { theme } from "$lib/stores/settings.js"
import LanguageIcon from "./LanguageIcon.svelte"

describe("LanguageIcon", () => {
  let mediaQuery: MediaQueryList
  let setSystemPreference: (prefersDark: boolean) => void

  beforeEach(() => {
    theme.set("system")
    let matches = false
    const events = new EventTarget()
    mediaQuery = {
      get matches() {
        return matches
      },
      media: "(prefers-color-scheme: dark)",
      onchange: null,
      addEventListener: events.addEventListener.bind(events),
      removeEventListener: events.removeEventListener.bind(events),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: events.dispatchEvent.bind(events),
    } as unknown as MediaQueryList
    vi.spyOn(window, "matchMedia").mockReturnValue(mediaQuery)
    setSystemPreference = (prefersDark) => {
      matches = prefersDark
      events.dispatchEvent(new Event("change"))
    }
  })

  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
    theme.set("dark")
  })

  it("uses the explicit light theme when the operating system is dark", () => {
    setSystemPreference(true)
    theme.set("light")

    const { container } = render(LanguageIcon, { props: { name: "Go" } })

    expect(container.querySelector("img")?.getAttribute("src")).toBe(
      "./icons/languages/go.svg",
    )
  })

  it("uses the explicit dark theme when the operating system is light", () => {
    setSystemPreference(false)
    theme.set("dark")

    const { container } = render(LanguageIcon, { props: { name: "Rust" } })

    expect(container.querySelector("img")?.getAttribute("src")).toBe(
      "./icons/languages/rust_dark.svg",
    )
  })

  it("follows system preference changes only in system mode", async () => {
    const { container } = render(LanguageIcon, { props: { name: "PHP" } })
    setSystemPreference(true)
    await tick()

    expect(container.querySelector("img")?.getAttribute("src")).toBe(
      "./icons/languages/php_dark.svg",
    )

    setSystemPreference(false)
    await tick()
    expect(container.querySelector("img")?.getAttribute("src")).toBe(
      "./icons/languages/php.svg",
    )

    theme.set("light")
    setSystemPreference(true)
    await tick()
    expect(container.querySelector("img")?.getAttribute("src")).toBe(
      "./icons/languages/php.svg",
    )
  })
})
