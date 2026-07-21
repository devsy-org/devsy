export interface CLIError {
  code: string
  message: string
}

export interface CliLogLine {
  level?: "debug" | "info" | "warn" | "error" | "panic" | "fatal" | string
  ts?: string
  msg?: string
  cliError?: CLIError
  [key: string]: unknown
}
