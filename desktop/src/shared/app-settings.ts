export type TrayNotificationLevel = "off" | "failures" | "all"

export interface AppSettings {
  runAtStartup: boolean
  openToTrayOnStartup: boolean
  trayNotifications: TrayNotificationLevel
}

export interface StartupStatus {
  applied: boolean
  enabled: boolean
  status: "enabled" | "disabled" | "denied" | "unavailable" | "error"
  detail?: string
}

export interface AppSettingsState {
  settings: AppSettings
  startup: StartupStatus
}
