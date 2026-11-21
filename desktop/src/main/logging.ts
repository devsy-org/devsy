import {
  appendFileSync,
  existsSync,
  mkdirSync,
  renameSync,
  rmSync,
  statSync,
} from "node:fs"
import { join } from "node:path"
import {
  credentialAssignments,
  isCredentialKey,
} from "../../../logging/desktop/diagnostic-redaction.js"
import type { LogLevel } from "../shared/app-settings.js"

const rank: Record<LogLevel, number> = {
  error: 0,
  warn: 1,
  info: 2,
  debug: 3,
  trace: 4,
}
const maxFileBytes = 5 * 1024 * 1024
const maxRecordBytes = 64 * 1024
let current: LogLevel = "info"
let logPath: string | undefined

type RedactionRange = [start: number, end: number]

function redactText(value: string): string {
  const ranges = Array.from(
    credentialAssignments(value),
    ({ start, end }): RedactionRange => [start, end],
  )
  for (const match of value.matchAll(/(https?:\/\/)[^\s/@]+@/gi)) {
    ranges.push([
      match.index + match[1].length,
      match.index + match[0].length - 1,
    ])
  }
  for (const [name, secret] of Object.entries(process.env)) {
    if (!secret || !isCredentialKey(name)) continue
    for (
      let start = value.indexOf(secret);
      start >= 0;
      start = value.indexOf(secret, start + 1)
    ) {
      ranges.push([start, start + secret.length])
    }
  }
  ranges.sort(([left], [right]) => left - right)
  let output = ""
  let copied = 0
  for (const [start, end] of ranges) {
    if (start >= copied) output += `${value.slice(copied, start)}***`
    copied = Math.max(copied, end)
  }
  return output + value.slice(copied)
}

function errorField(
  value: Error,
  key: "name" | "message",
  fallback: string,
): string {
  for (
    let object: object | null = value;
    object !== null;
    object = Object.getPrototypeOf(object)
  ) {
    const field = Object.getOwnPropertyDescriptor(object, key)
    if (!field) continue
    return Object.hasOwn(field, "value") && typeof field.value === "string"
      ? field.value
      : fallback
  }
  return fallback
}

function safeScalar(value: unknown): unknown {
  if (typeof value === "string") return redactText(value)
  if (typeof value === "number" || typeof value === "bigint") {
    const rendered = String(value)
    if (redactText(rendered) !== rendered) return "***"
    return typeof value === "bigint" ? rendered : value
  }
  if (typeof value === "function") return "[unavailable]"
  return value
}

function safeValue(
  value: unknown,
  seen = new WeakSet<object>(),
  depth = 0,
): unknown {
  if (value === null || typeof value !== "object") return safeScalar(value)
  if (value instanceof Error)
    return {
      name: redactText(errorField(value, "name", "Error")),
      message: redactText(errorField(value, "message", "")),
    }
  if (depth >= 8 || seen.has(value)) return "[unavailable]"
  seen.add(value)
  if (Array.isArray(value))
    return Array.from({ length: value.length }, (_, index) =>
      safeValue(
        Object.getOwnPropertyDescriptor(value, index)?.value,
        seen,
        depth + 1,
      ),
    )
  const result: Record<string, unknown> = Object.create(null)
  for (const [key, property] of Object.entries(
    Object.getOwnPropertyDescriptors(value),
  )) {
    if (!property.enumerable || !Object.hasOwn(property, "value")) continue
    result[redactText(key)] = isCredentialKey(key)
      ? "***"
      : safeValue(property.value, seen, depth + 1)
  }
  return result
}

function appendRecord(record: string): void {
  if (!logPath) return
  try {
    if (
      existsSync(logPath) &&
      statSync(logPath).size + Buffer.byteLength(record) > maxFileBytes
    ) {
      for (let i = 2; i >= 1; i--) {
        const source = `${logPath}.${i}`
        if (existsSync(source)) {
          rmSync(`${logPath}.${i + 1}`, { force: true })
          renameSync(source, `${logPath}.${i + 1}`)
        }
      }
      rmSync(`${logPath}.1`, { force: true })
      renameSync(logPath, `${logPath}.1`)
    }
    appendFileSync(logPath, record, { encoding: "utf8", mode: 0o600 })
  } catch {
    logPath = undefined
  }
}

function emit(
  level: LogLevel,
  component: string | undefined,
  args: unknown[],
): void {
  if (rank[current] < rank[level]) return
  let record: string
  try {
    const [first, ...rest] = args
    const message = typeof first === "string" ? redactText(first) : ""
    const attrs = rest.length ? safeValue(rest) : undefined
    record = JSON.stringify({
      ts: new Date().toISOString(),
      level,
      msg: message,
      ...(component ? { component: redactText(component) } : {}),
      ...(typeof first !== "string" ? { value: safeValue(first) } : {}),
      ...(attrs ? { args: attrs } : {}),
    })
    if (Buffer.byteLength(record) + 1 > maxRecordBytes) {
      record = JSON.stringify({
        ts: new Date().toISOString(),
        level,
        msg: `${message.slice(0, 4096)} [record truncated]`,
      })
    }
  } catch {
    record = JSON.stringify({
      ts: new Date().toISOString(),
      level,
      msg: "[unavailable log record]",
    })
  }
  const sink =
    level === "trace" || level === "debug" ? console.debug : console[level]
  sink(record)
  appendRecord(`${record}\n`)
}

function logger(component?: string) {
  return {
    trace: (...args: unknown[]) => emit("trace", component, args),
    debug: (...args: unknown[]) => emit("debug", component, args),
    info: (...args: unknown[]) => emit("info", component, args),
    warn: (...args: unknown[]) => emit("warn", component, args),
    error: (...args: unknown[]) => emit("error", component, args),
    child: (name: string) => logger(component ? `${component}.${name}` : name),
  }
}

export const mainLog = logger()

export function initializeMainLog(directory: string): void {
  mkdirSync(directory, { recursive: true, mode: 0o700 })
  logPath = join(directory, "main.log")
}

export function setMainLogLevel(level: LogLevel | undefined): void {
  const env = process.env.DEVSY_LOG_LEVEL?.trim()
  if (env && !Object.hasOwn(rank, env))
    throw new Error("Invalid DEVSY_LOG_LEVEL")
  current = env
    ? (env as LogLevel)
    : level && Object.hasOwn(rank, level)
      ? level
      : "info"
}

export function getMainLogLevel(): LogLevel {
  return current
}
