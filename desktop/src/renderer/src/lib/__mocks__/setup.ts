/**
 * Vitest setup file.
 * Provides browser API polyfills that jsdom doesn't fully cover.
 */

import { afterAll } from "vitest"

// bits-ui's body scroll lock schedules a ~24ms setTimeout to reset the body
// style after a Dialog/Sheet unmounts. If a test file finishes before that
// timer fires, jsdom is torn down and the callback throws
// "ReferenceError: document is not defined". Drain any pending cleanup before
// teardown, while document still exists.
afterAll(async () => {
  await new Promise((resolve) => setTimeout(resolve, 30))
})

// Node 22+ has a built-in localStorage that requires --localstorage-file.
// Override with a simple in-memory implementation for tests.
if (
  typeof globalThis.localStorage === "undefined" ||
  typeof globalThis.localStorage.getItem !== "function"
) {
  const store = new Map<string, string>()
  globalThis.localStorage = {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, value: string) => store.set(key, value),
    removeItem: (key: string) => store.delete(key),
    clear: () => store.clear(),
    get length() {
      return store.size
    },
    key: (index: number) => [...store.keys()][index] ?? null,
  }
}

// jsdom doesn't implement matchMedia
if (typeof window !== "undefined" && typeof window.matchMedia !== "function") {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  })
}
