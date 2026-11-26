export interface LinuxDesktopIntegrationStatus {
  /** True only when Devsy runs from an AppImage on Linux. */
  supported: boolean
  appImagePath?: string
  /** A launcher written by this integration exists. */
  installed: boolean
  /** The owned launcher points at a different AppImage path or lost its icon. */
  needsUpdate: boolean
  /** Another launcher already covers Devsy or this AppImage; install is refused. */
  conflict?: string
  /** The AppImage path cannot be used in a launcher. */
  problem?: string
}

export interface LinuxDesktopIntegrationResult {
  status: LinuxDesktopIntegrationStatus
  error?: string
}
