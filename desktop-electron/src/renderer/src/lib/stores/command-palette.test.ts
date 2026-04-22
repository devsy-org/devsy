import { get } from "svelte/store"
import { describe, expect, it } from "vitest"
import { paletteOpen, togglePalette } from "./command-palette.js"

describe("command-palette store", () => {
  it("starts closed", () => {
    expect(get(paletteOpen)).toBe(false)
  })

  it("togglePalette opens when closed", () => {
    paletteOpen.set(false)
    togglePalette()
    expect(get(paletteOpen)).toBe(true)
  })

  it("togglePalette closes when open", () => {
    paletteOpen.set(true)
    togglePalette()
    expect(get(paletteOpen)).toBe(false)
  })

  it("double toggle returns to original state", () => {
    paletteOpen.set(false)
    togglePalette()
    togglePalette()
    expect(get(paletteOpen)).toBe(false)
  })
})
