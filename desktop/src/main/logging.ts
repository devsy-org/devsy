import type { LogLevel } from "../shared/app-settings.js"

const original = {
  debug: console.debug.bind(console),
  info: console.info.bind(console),
  log: console.log.bind(console),
  warn: console.warn.bind(console),
  error: console.error.bind(console),
}

const rank: Record<LogLevel, number> = { error: 0, warn: 1, info: 2, debug: 3, trace: 4 }
let current: LogLevel = "info"

export function setMainLogLevel(level: LogLevel | undefined): void {
  current = level && level in rank ? level : "info"
  const enabled = (name: LogLevel) => rank[current] >= rank[name]
  console.debug = enabled("debug") ? original.debug : () => {}
  console.info = enabled("info") ? original.info : () => {}
  console.log = enabled("info") ? original.log : () => {}
  console.warn = enabled("warn") ? original.warn : () => {}
  console.error = original.error
}

export function getMainLogLevel(): LogLevel {
  return current
}
