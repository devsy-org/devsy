import {
  appendFileSync,
  existsSync,
  mkdirSync,
  renameSync,
  rmSync,
  statSync,
} from "node:fs"
import { join } from "node:path"
import type { LogLevel } from "../shared/app-settings.js"

const rank: Record<LogLevel, number> = {
  error: 0,
  warn: 1,
  info: 2,
  debug: 3,
  trace: 4,
}
const sensitiveKey =
  /(password|passwd|token|secret|api.?key|authorization|credential|private.?key|access.?key|telemetry.?distinct.?id)/i
const sensitiveLabel = new RegExp(`^(?:${sensitiveKey.source})$`, "i")
const maxFileBytes = 5 * 1024 * 1024
const maxRecordBytes = 64 * 1024
let current: LogLevel = "info"
let logPath: string | undefined

function redactLabels(value: string): string {
  const tokens = /[a-z0-9_.-]+/gi
  let output = ""
  let copied = 0
  for (let match = tokens.exec(value); match; match = tokens.exec(value)) {
    const start = match.index
    let end = tokens.lastIndex
    let label = match[0]
    let candidateEnd = end
    for (let parts = 0; parts < 2; parts++) {
      const suffix = /^[ \t]+[a-z0-9_.-]+/i.exec(
        value.slice(candidateEnd, candidateEnd + 64),
      )
      if (!suffix) break
      candidateEnd += suffix[0].length
      const candidate = value.slice(start, candidateEnd)
      if (sensitiveLabel.test(candidate)) {
        end = candidateEnd
        label = candidate
      }
    }
    let next = end
    if (!sensitiveKey.test(label)) continue
    if (value[next] === '"' || value[next] === "'") next++
    while (next < value.length && /\s/.test(value[next])) next++
    if (value[next] !== ":" && value[next] !== "=") continue
    next++
    while (next < value.length && /\s/.test(value[next])) next++
    const secretStart = next
    const scheme = value.slice(next, next + 6).toLowerCase()
    const schemeLength = scheme.startsWith("bearer")
      ? 6
      : scheme.startsWith("basic")
        ? 5
        : 0
    if (schemeLength && /\s/.test(value[next + schemeLength] ?? "")) {
      next += schemeLength
      while (next < value.length && /\s/.test(value[next])) next++
    }
    const quote =
      value[next] === '"' || value[next] === "'" ? value[next++] : ""
    if (quote) {
      while (next < value.length) {
        if (value[next] === "\\") {
          next = Math.min(next + 2, value.length)
        } else if (value[next++] === quote) {
          break
        }
      }
    } else {
      while (next < value.length && !/[\s,;]/.test(value[next])) next++
    }
    if (next === secretStart) continue
    output += `${value.slice(copied, secretStart)}***`
    copied = next
    tokens.lastIndex = next
  }
  return output + value.slice(copied)
}

function redactText(value: string): string {
  let result = value
  const secrets = Object.entries(process.env)
    .filter(([name, secret]) => sensitiveKey.test(name) && secret)
    .map(([, secret]) => secret as string)
    .sort((a, b) => b.length - a.length)
  for (const secret of secrets) result = result.split(secret).join("***")
  return redactLabels(result.replace(/(https?:\/\/)[^\s/@]+@/gi, "$1***@"))
}

function safeValue(
  value: unknown,
  seen = new WeakSet<object>(),
  depth = 0,
): unknown {
  if (typeof value === "string") return redactText(value)
  if (typeof value === "bigint") return value.toString()
  if (value === null || typeof value !== "object") return value
  if (value instanceof Error)
    return { name: redactText(value.name), message: redactText(value.message) }
  if (depth >= 8 || seen.has(value)) return "[unavailable]"
  seen.add(value)
  if (Array.isArray(value))
    return value.map((item) => safeValue(item, seen, depth + 1))
  const result: Record<string, unknown> = {}
  for (const [key, item] of Object.entries(value)) {
    result[redactText(key)] = sensitiveKey.test(key)
      ? "***"
      : safeValue(item, seen, depth + 1)
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
