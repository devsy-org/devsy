import type { LogLevel } from "../shared/app-settings.js"

const rank: Record<LogLevel, number> = { error: 0, warn: 1, info: 2, debug: 3, trace: 4 }
let current: LogLevel = "info"

function enabled(level: LogLevel): boolean {
  return rank[current] >= rank[level]
}

export const mainLog = {
  debug: (...args: unknown[]) => { if (enabled("debug")) console.debug(...args) },
  trace: (...args: unknown[]) => { if (enabled("trace")) console.debug(...args) },
  info: (...args: unknown[]) => { if (enabled("info")) console.info(...args) },
  warn: (...args: unknown[]) => { if (enabled("warn")) console.warn(...args) },
  error: (...args: unknown[]) => console.error(...args),
}

export function setMainLogLevel(level: LogLevel | undefined): void {
  current = level && level in rank ? level : "info"
}

export function getMainLogLevel(): LogLevel {
  return current
}
