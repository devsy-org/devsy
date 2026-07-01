import type { UpdateStatus } from "$lib/ipc/events.js"

export function fmtMBps(bps: number | undefined): string {
  if (!bps) return ""
  return `${(bps / 1_000_000).toFixed(2)} MB/s`
}

export function fmtTime(ts: number | null): string {
  if (!ts) return ""
  return new Date(ts).toLocaleTimeString()
}

// One-line summary of the update state, shared by the settings hero and the
// standalone dialog so both surfaces read identically.
export function statusHeadline(s: UpdateStatus, currentVersion: string | null): string {
  switch (s.state) {
    case "checking":
      return "Checking for updates…"
    case "available":
      return `Version ${s.version ?? "unknown"} is available`
    case "downloading":
      return `Downloading v${s.version ?? "?"} · ${(s.progress?.percent ?? 0).toFixed(0)}%`
    case "downloaded":
      return `Version ${s.version ?? "unknown"} is ready to install`
    case "error":
      return "Update check failed"
    case "not-available":
      if (s.code === "dev-mode") return "Updates run in packaged builds"
      if (s.code === "channel-missing") return "No releases on this channel yet"
      return currentVersion ? `Devsy is up to date · v${currentVersion}` : "Devsy is up to date"
    default:
      return currentVersion ? `Devsy v${currentVersion}` : "Devsy"
  }
}
