import { get } from "svelte/store"
import { afterEach, beforeEach, describe, expect, it } from "vitest"

import {
  applyTheme,
  CONTEXT_OPTION_KEYS,
  cycleTheme,
  DEFAULT_CONTEXT_OPTIONS,
  effectiveTheme,
  getWorkspaceRecoveryState,
  initSettings,
  parseContextOptions,
  setWorkspaceRecoveryState,
  theme,
} from "./settings.js"

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

  describe("effectiveTheme", () => {
    const realMatchMedia = window.matchMedia
    let osDark = false
    let osListeners: Array<() => void> = []

    function setOsDark(value: boolean) {
      osDark = value
      for (const listener of osListeners) listener()
    }

    beforeEach(() => {
      osDark = false
      osListeners = []
      window.matchMedia = ((query: string) => ({
        get matches() {
          return osDark
        },
        media: query,
        addEventListener: (_type: string, listener: () => void) => {
          osListeners.push(listener)
        },
        removeEventListener: (_type: string, listener: () => void) => {
          osListeners = osListeners.filter((entry) => entry !== listener)
        },
      })) as unknown as typeof window.matchMedia
    })

    afterEach(() => {
      window.matchMedia = realMatchMedia
    })

    it("follows an explicit override regardless of the OS preference", () => {
      osDark = false
      applyTheme("dark")
      expect(get(effectiveTheme)).toBe("dark")

      osDark = true
      applyTheme("light")
      expect(get(effectiveTheme)).toBe("light")
    })

    it("resolves system to the current OS preference", () => {
      osDark = true
      applyTheme("system")
      expect(get(effectiveTheme)).toBe("dark")

      osDark = false
      applyTheme("system")
      expect(get(effectiveTheme)).toBe("light")
    })

    it("tracks live OS transitions while the setting stays system, through the single media-query listener", () => {
      theme.set("system")
      const dispose = initSettings()
      try {
        expect(osListeners).toHaveLength(1)
        expect(get(effectiveTheme)).toBe("light")

        setOsDark(true)
        expect(get(theme)).toBe("system")
        expect(get(effectiveTheme)).toBe("dark")
        expect(document.documentElement.classList.contains("dark")).toBe(true)

        setOsDark(false)
        expect(get(effectiveTheme)).toBe("light")
      } finally {
        dispose()
      }
      expect(osListeners).toHaveLength(0)
    })

    it("ignores OS transitions while an explicit theme is set", () => {
      theme.set("light")
      const dispose = initSettings()
      try {
        setOsDark(true)
        expect(get(effectiveTheme)).toBe("light")
      } finally {
        dispose()
      }
    })

    it("follows theme changes made through the theme store", () => {
      const dispose = initSettings()
      try {
        theme.set("light")
        expect(get(effectiveTheme)).toBe("light")
        theme.set("dark")
        expect(get(effectiveTheme)).toBe("dark")
      } finally {
        dispose()
      }
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

  describe("workspace recovery state", () => {
    it("returns empty state when nothing is stored", () => {
      expect(getWorkspaceRecoveryState("ws-1")).toEqual({})
    })

    it("persists and reads back per-workspace state", () => {
      setWorkspaceRecoveryState("ws-1", {
        buildFailed: true,
        inRecovery: false,
      })
      expect(getWorkspaceRecoveryState("ws-1")).toEqual({
        buildFailed: true,
        inRecovery: false,
      })
    })

    it("keeps state isolated per workspace", () => {
      setWorkspaceRecoveryState("ws-1", { inRecovery: true })
      setWorkspaceRecoveryState("ws-2", { buildFailed: true })
      expect(getWorkspaceRecoveryState("ws-1")).toEqual({ inRecovery: true })
      expect(getWorkspaceRecoveryState("ws-2")).toEqual({ buildFailed: true })
    })

    it("clears the entry when no flags are set", () => {
      setWorkspaceRecoveryState("ws-1", { buildFailed: true })
      setWorkspaceRecoveryState("ws-1", {
        buildFailed: false,
        inRecovery: false,
      })
      expect(getWorkspaceRecoveryState("ws-1")).toEqual({})
    })
  })
})

describe("GitHub CLI context forwarding", () => {
  it("defaults off for absent and empty options", () => {
    expect(DEFAULT_CONTEXT_OPTIONS.githubCliCredentialForwarding).toBe(false)
    expect(parseContextOptions({}).githubCliCredentialForwarding).toBe(false)
    expect(
      parseContextOptions({ SSH_INJECT_GITHUB_CREDENTIALS: { value: "" } })
        .githubCliCredentialForwarding,
    ).toBe(false)
    expect(CONTEXT_OPTION_KEYS.githubCliCredentialForwarding).toBe(
      "SSH_INJECT_GITHUB_CREDENTIALS",
    )
  })
  it("stays independent from Git and Docker", () => {
    const enabled = parseContextOptions({
      SSH_INJECT_GITHUB_CREDENTIALS: { value: "true" },
      SSH_INJECT_GIT_CREDENTIALS: { value: "false" },
      SSH_INJECT_DOCKER_CREDENTIALS: { value: "false" },
    })
    expect(enabled.githubCliCredentialForwarding).toBe(true)
    expect(enabled.gitCredentialForwarding).toBe(false)
    expect(enabled.dockerCredentialForwarding).toBe(false)
    expect(
      parseContextOptions({ SSH_INJECT_GITHUB_CREDENTIALS: { value: "false" } })
        .githubCliCredentialForwarding,
    ).toBe(false)
  })
})
