/** Mock for $app/environment in vitest */
export let browser = false
export const building = false
export const dev = true
export const version = "test"

export function _setBrowser(value: boolean) {
  browser = value
}
