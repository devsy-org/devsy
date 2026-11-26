let quitting = false

export function markAppQuitting(): void {
  quitting = true
}

export function clearAppQuitting(): void {
  quitting = false
}

export function isAppQuitting(): boolean {
  return quitting
}
