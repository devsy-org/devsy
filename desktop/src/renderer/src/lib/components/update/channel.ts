import type { ReleaseChannel } from "$lib/ipc/commands.js"

// UI-only presentation over the backend channel values. The persisted
// `update-settings.json` and electron-updater channel still use
// "stable"/"beta"; "Preview" is purely a display label mapped here.
export interface ChannelMeta {
  value: ReleaseChannel
  label: string
}

export const CHANNELS: ChannelMeta[] = [
  {
    value: "stable",
    label: "Stable",
  },
  {
    value: "beta",
    label: "Preview",
  },
]

export function channelLabel(value: ReleaseChannel): string {
  return CHANNELS.find((c) => c.value === value)?.label ?? value
}

// Moving from Preview back to Stable can leave the user on a newer build
// than the latest Stable release, so it warrants a confirmation.
export function isDowngrade(from: ReleaseChannel, to: ReleaseChannel): boolean {
  return from === "beta" && to === "stable"
}
