import { push, replace, router } from "svelte-spa-router"
import { readable } from "svelte/store"

export { push, replace, router }

/**
 * Drop-in replacement for SvelteKit's goto().
 * Navigates using the hash router.
 */
export function goto(path: string, opts?: { replaceState?: boolean }) {
  if (opts?.replaceState) {
    replace(path)
  } else {
    push(path)
  }
}

/**
 * Readable store for the current location path.
 * Derived from the hash router state.
 */
export const location = readable("/", (set) => {
  function update() {
    const hash = window.location.hash
    const qsPos = hash.indexOf("?")
    const path = qsPos > -1 ? hash.slice(1, qsPos) : hash.slice(1)
    set(path || "/")
  }
  update()
  window.addEventListener("hashchange", update)
  return () => window.removeEventListener("hashchange", update)
})

/**
 * Readable store for the current querystring.
 * Derived from the hash router state.
 */
export const querystring = readable("", (set) => {
  function update() {
    const hash = window.location.hash
    const qsPos = hash.indexOf("?")
    set(qsPos > -1 ? hash.slice(qsPos + 1) : "")
  }
  update()
  window.addEventListener("hashchange", update)
  return () => window.removeEventListener("hashchange", update)
})
