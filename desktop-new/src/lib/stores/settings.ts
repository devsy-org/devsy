import { writable } from "svelte/store"
import { browser } from "$app/environment"

export type Theme = "light" | "dark" | "system"
export type FontSize = "small" | "medium" | "large"

const STORAGE_KEY = "devpod-theme"
const FONT_SIZE_KEY = "devpod-font-size"

const FONT_SIZE_CLASSES: Record<FontSize, string> = {
  small: "text-sm",
  medium: "text-base",
  large: "text-lg",
}

function getInitialFontSize(): FontSize {
  if (browser) {
    const stored = localStorage.getItem(FONT_SIZE_KEY)
    if (stored === "small" || stored === "medium" || stored === "large") {
      return stored
    }
  }
  return "medium"
}

export const fontSize = writable<FontSize>(getInitialFontSize())

export function applyFontSize(value: FontSize) {
  if (!browser) return
  localStorage.setItem(FONT_SIZE_KEY, value)
  const root = document.documentElement
  for (const cls of Object.values(FONT_SIZE_CLASSES)) {
    root.classList.remove(cls)
  }
  root.classList.add(FONT_SIZE_CLASSES[value])
}

function getInitialTheme(): Theme {
  if (browser) {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored === "light" || stored === "dark" || stored === "system") {
      return stored
    }
  }
  return "dark"
}

export const theme = writable<Theme>(getInitialTheme())

export function applyTheme(value: Theme) {
  if (!browser) return

  localStorage.setItem(STORAGE_KEY, value)

  const root = document.documentElement
  if (value === "system") {
    const prefersDark = window.matchMedia(
      "(prefers-color-scheme: dark)",
    ).matches
    root.classList.toggle("dark", prefersDark)
  } else {
    root.classList.toggle("dark", value === "dark")
  }
}

export function cycleTheme() {
  theme.update((current) => {
    const next: Theme =
      current === "light" ? "dark" : current === "dark" ? "system" : "light"
    applyTheme(next)
    return next
  })
}

export function initSettings() {
  const unsubTheme = theme.subscribe((value) => {
    applyTheme(value)
  })
  const unsubFont = fontSize.subscribe((value) => {
    applyFontSize(value)
  })
  const unsubscribe = () => {
    unsubTheme()
    unsubFont()
  }

  if (browser) {
    const mediaQuery = window.matchMedia("(prefers-color-scheme: dark)")
    const handler = () => {
      let current = "dark" as Theme
      theme.subscribe((v) => (current = v))()
      if (current === "system") {
        applyTheme("system")
      }
    }
    mediaQuery.addEventListener("change", handler)
    return () => {
      unsubscribe()
      mediaQuery.removeEventListener("change", handler)
    }
  }

  return unsubscribe
}
