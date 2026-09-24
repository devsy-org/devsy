export type TrayNotificationLevel = "off" | "failures" | "all"
export type LogLevel = "error" | "warn" | "info" | "debug" | "trace"

export interface AppSettings {
  runAtStartup: boolean
  openToTrayOnStartup: boolean
  trayNotifications: TrayNotificationLevel
  desktopLogLevel: LogLevel
  cliCaptureLogLevel: LogLevel
  /** Removed after migration; accepted only when reading older settings files. */
  logLevel?: LogLevel
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
