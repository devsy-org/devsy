import { describe, expect, it } from "vitest"
import {
  clearAppQuitting,
  isAppQuitting,
  markAppQuitting,
} from "../app-lifecycle.js"

describe("app lifecycle", () => {
  it("starts as not quitting", () => {
    expect(isAppQuitting()).toBe(false)
  })

  it("marks the application as quitting", () => {
    markAppQuitting()
    expect(isAppQuitting()).toBe(true)
    clearAppQuitting()
    expect(isAppQuitting()).toBe(false)
  })
})
