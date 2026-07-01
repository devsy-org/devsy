import type { ReleaseChannel } from "$lib/ipc/commands.js"

// UI-only presentation over the backend channel values. The persisted
// `update-settings.json` and electron-updater channel still use
// "stable"/"beta"; "Preview" is purely a display label mapped here.
export interface ChannelMeta {
  value: ReleaseChannel
  label: string
  cadence: string
  description: string
  unstable: boolean
}

export const CHANNELS: ChannelMeta[] = [
  {
    value: "stable",
    label: "Stable",
    cadence: "Released on a regular schedule",
    description: "Production-ready builds, tested before release.",
    unstable: false,
  },
  {
    value: "beta",
    label: "Preview",
    cadence: "Updated frequently",
    description: "Early access to new features. May be unstable.",
    unstable: true,
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
