import { analyticsTrack } from "$lib/ipc/commands.js"

// A hidden window ends its session after this, so a backgrounded window
// does not inflate session duration.
const IDLE_TIMEOUT_MS = 5 * 60 * 1000

let sessionStartedAt = 0
// When the window is hidden, the moment it went hidden; 0 while visible. Used
// as the session end so the idle grace and background time aren't counted.
let hiddenAt = 0
let idleTimer: ReturnType<typeof setTimeout> | null = null
let sessionActive = false

function nowMs(): number {
  return performance.now()
}

function startSession(): void {
  if (sessionActive) return
  sessionActive = true
  sessionStartedAt = nowMs()
  analyticsTrack("session_start")
}

function endSession(): void {
  if (!sessionActive) return
  sessionActive = false
  const endedAt = hiddenAt || nowMs()
  analyticsTrack("session_end", {
    duration_ms: Math.round(endedAt - sessionStartedAt),
  })
}

function clearIdleTimer(): void {
  if (idleTimer !== null) {
    clearTimeout(idleTimer)
    idleTimer = null
  }
}

function handleVisibilityChange(): void {
  if (document.visibilityState === "visible") {
    clearIdleTimer()
    hiddenAt = 0
    startSession()
    return
  }
  // Grace period so quick tab-outs don't fragment one session into many.
  hiddenAt = nowMs()
  clearIdleTimer()
  idleTimer = setTimeout(endSession, IDLE_TIMEOUT_MS)
}

// Returns a teardown function. Call once on app mount.
export function initSessionTracking(): () => void {
  startSession()
  document.addEventListener("visibilitychange", handleVisibilityChange)
  window.addEventListener("pagehide", endSession)

  return () => {
    clearIdleTimer()
    document.removeEventListener("visibilitychange", handleVisibilityChange)
    window.removeEventListener("pagehide", endSession)
    endSession()
  }
}

// Keep feature names low-cardinality; put variable detail in properties.
export function trackEngagement(
  feature: string,
  properties?: Record<string, unknown>,
): void {
  analyticsTrack("engagement", { feature, ...properties })
}
