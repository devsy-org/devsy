import { get } from "svelte/store"
import { beforeEach, describe, expect, it } from "vitest"

import {
  applyTheme,
  CONTEXT_OPTION_KEYS,
  cycleTheme,
  DEFAULT_CONTEXT_OPTIONS,
  getWorkspaceRecoveryState,
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
