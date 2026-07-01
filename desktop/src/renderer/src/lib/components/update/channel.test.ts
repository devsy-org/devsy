import { describe, it, expect } from "vitest"
import { CHANNELS, channelLabel, isDowngrade } from "./channel.js"

describe("channel labels", () => {
  it("maps beta to the Preview display label", () => {
    expect(channelLabel("beta")).toBe("Preview")
  })

  it("maps stable to Stable", () => {
    expect(channelLabel("stable")).toBe("Stable")
  })

  it("exposes both channels with backend values intact", () => {
    expect(CHANNELS.map((c) => c.value)).toEqual(["stable", "beta"])
  })

  it("flags Preview as unstable", () => {
    expect(CHANNELS.find((c) => c.value === "beta")?.unstable).toBe(true)
    expect(CHANNELS.find((c) => c.value === "stable")?.unstable).toBe(false)
  })
})

describe("isDowngrade", () => {
  it("treats Preview → Stable as a downgrade", () => {
    expect(isDowngrade("beta", "stable")).toBe(true)
  })

  it("treats Stable → Preview as not a downgrade", () => {
    expect(isDowngrade("stable", "beta")).toBe(false)
  })

  it("treats same-channel as not a downgrade", () => {
    expect(isDowngrade("beta", "beta")).toBe(false)
    expect(isDowngrade("stable", "stable")).toBe(false)
  })
})
