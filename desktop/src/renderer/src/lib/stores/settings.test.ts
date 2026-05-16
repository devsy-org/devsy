import { get } from "svelte/store"
import { beforeEach, describe, expect, it } from "vitest"

import { applyTheme, cycleTheme, theme } from "./settings.js"

describe("settings store", () => {
  beforeEach(() => {
    theme.set("dark")
    localStorage.clear()
    document.documentElement.classList.remove("dark")
  })

  describe("applyTheme", () => {
    it("applies dark theme", () => {
      applyTheme("dark")

      expect(localStorage.getItem("devsy-theme")).toBe("dark")
      expect(document.documentElement.classList.contains("dark")).toBe(true)
    })

    it("applies light theme", () => {
      applyTheme("light")

      expect(localStorage.getItem("devsy-theme")).toBe("light")
      expect(document.documentElement.classList.contains("dark")).toBe(false)
    })

    it("applies system theme using media query", () => {
      applyTheme("system")

      expect(localStorage.getItem("devsy-theme")).toBe("system")
    })

    it("persists theme to localStorage", () => {
      applyTheme("light")
      expect(localStorage.getItem("devsy-theme")).toBe("light")

      applyTheme("dark")
      expect(localStorage.getItem("devsy-theme")).toBe("dark")
    })
  })

  describe("cycleTheme", () => {
    it("cycles light -> dark", () => {
      theme.set("light")
      cycleTheme()
      expect(get(theme)).toBe("dark")
    })

    it("cycles dark -> system", () => {
      theme.set("dark")
      cycleTheme()
      expect(get(theme)).toBe("system")
    })

    it("cycles system -> light", () => {
      theme.set("system")
      cycleTheme()
      expect(get(theme)).toBe("light")
    })

    it("full cycle returns to original", () => {
      theme.set("light")
      cycleTheme() // dark
      cycleTheme() // system
      cycleTheme() // light
      expect(get(theme)).toBe("light")
    })
  })
})
