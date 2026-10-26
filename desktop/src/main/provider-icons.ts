import {
  PROVIDER_ICON_MAX_BYTES,
  PROVIDER_ICON_MIME_TYPES,
  type ProviderIconReply,
  type ProviderIconRequest,
  type ProviderIconTheme,
} from "../shared/provider-icon.js"
import type { CliRunner } from "./cli.js"

// Base64 of the 1 MiB image cap plus the JSON envelope must fit in stdout.
const CLI_MAX_STDOUT_BYTES = 2 * 1024 * 1024
const MAX_BASE64_LENGTH = Math.ceil(PROVIDER_ICON_MAX_BYTES / 3) * 4
const MAX_CACHE_ENTRIES = 128
const MAX_CONTEXT_PATH_BYTES = 255
const INVALID_CONTEXT_PATH_CHARACTERS = /[\\/:<>"|?*]/u
const BASE64_PATTERN =
  /^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/

interface CliProviderIconResult {
  revision?: unknown
  mimeType?: unknown
  dataBase64?: unknown
  missing?: unknown
}

type ObservedIcon =
  | { revision: string; dataUrl: string }
  | { revision: string; missing: true }

function requireIdentifier(value: unknown, label: string): string {
  // A leading dash would be parsed as a CLI flag.
  if (
    typeof value !== "string" ||
    value.trim() === "" ||
    value.startsWith("-") ||
    value.includes("\0")
  )
    throw new Error(`A valid ${label} is required.`)
  return value
}

function requireContext(value: unknown): string {
  if (
    typeof value !== "string" ||
    value.trim() === "" ||
    hasContextControlCharacter(value) ||
    value.startsWith("-") ||
    value === "." ||
    value === ".." ||
    value.endsWith(".") ||
    value.endsWith(" ") ||
    Buffer.byteLength(value, "utf8") > MAX_CONTEXT_PATH_BYTES ||
    INVALID_CONTEXT_PATH_CHARACTERS.test(value) ||
    (process.platform === "win32" && isReservedWindowsContextName(value))
  )
    throw new Error("A valid context is required.")
  return value
}

function hasContextControlCharacter(value: string): boolean {
  for (let index = 0; index < value.length; index++) {
    const code = value.charCodeAt(index)
    if (code < 0x20 || code === 0x7f) return true
  }
  return false
}

function isReservedWindowsContextName(value: string): boolean {
  const basename = value.split(".", 1)[0].toUpperCase()
  return /^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])$/.test(basename)
}

function requireTheme(value: unknown): ProviderIconTheme {
  if (value !== "light" && value !== "dark")
    throw new Error("Icon theme must be light or dark.")
  return value
}

/** Re-checks type and size of the CLI payload and builds the internal data URL. */
export function toObservedIcon(raw: unknown): ObservedIcon {
  if (!raw || typeof raw !== "object")
    throw new Error("Provider icon result is not an object.")
  const result = raw as CliProviderIconResult
  const revision = typeof result.revision === "string" ? result.revision : ""
  if (result.missing === true) return { revision, missing: true }

  const mimeType = result.mimeType
  if (
    typeof mimeType !== "string" ||
    !(PROVIDER_ICON_MIME_TYPES as readonly string[]).includes(mimeType)
  )
    throw new Error("Provider icon has an unsupported image type.")

  const data = result.dataBase64
  if (
    typeof data !== "string" ||
    data.length === 0 ||
    data.length > MAX_BASE64_LENGTH ||
    data.length % 4 !== 0 ||
    !BASE64_PATTERN.test(data)
  )
    throw new Error("Provider icon data is invalid or too large.")
  if (Buffer.from(data, "base64").length > PROVIDER_ICON_MAX_BYTES)
    throw new Error("Provider icon data is too large.")

  return { revision, dataUrl: `data:${mimeType};base64,${data}` }
}

/**
 * Reads installed provider icons through the CLI. Results are cached by
 * context, provider, revision and theme; concurrent identical reads share one
 * CLI invocation. The cache only ever holds bytes the CLI reported for the
 * revision in the key, so a stale caller can never receive another revision's
 * image.
 */
export class ProviderIconReader {
  private cache = new Map<string, ObservedIcon>()
  private inFlight = new Map<string, Promise<ObservedIcon>>()
  private providerEpochs = new Map<string, number>()
  private epoch = 0

  constructor(private cli: Pick<CliRunner, "run">) {}

  async read(request: ProviderIconRequest): Promise<ProviderIconReply> {
    const providerName = requireIdentifier(request?.providerName, "provider")
    if (!/^[a-z0-9-]{1,32}$/.test(providerName))
      throw new Error("A valid provider is required.")
    const context = requireContext(request.context)
    const theme = requireTheme(request.theme)
    const expectedRevision =
      typeof request.expectedRevision === "string"
        ? request.expectedRevision
        : ""
    if (expectedRevision.includes("\0"))
      throw new Error("A valid icon revision is required.")

    const requestKey = cacheKey(context, providerName, expectedRevision, theme)
    const cached = expectedRevision ? this.touch(requestKey) : undefined
    const observed = cached ?? (await this.load(requestKey, request))
    if (observed.revision !== expectedRevision)
      return { revision: observed.revision, stale: true }
    return observed
  }

  invalidateProvider(providerName: string): void {
    const marker = `\0${providerName}\0`
    const keys = [...this.cache.keys(), ...this.inFlight.keys()].filter((key) =>
      key.includes(marker),
    )
    for (const key of keys) {
      const [context, name] = key.split("\0")
      const scope = `${context}\0${name}`
      this.providerEpochs.set(scope, (this.providerEpochs.get(scope) ?? 0) + 1)
      this.cache.delete(key)
      this.inFlight.delete(key)
    }
  }

  invalidateAll(): void {
    this.epoch++
    this.inFlight.clear()
    this.cache.clear()
    this.providerEpochs.clear()
  }

  private touch(key: string): ObservedIcon | undefined {
    const hit = this.cache.get(key)
    if (hit) {
      this.cache.delete(key)
      this.cache.set(key, hit)
    }
    return hit
  }

  private load(
    requestKey: string,
    request: ProviderIconRequest,
  ): Promise<ObservedIcon> {
    const pending = this.inFlight.get(requestKey)
    if (pending) return pending

    const scope = `${request.context}\0${request.providerName}`
    const epoch = this.epoch
    const providerEpoch = this.providerEpochs.get(scope) ?? 0
    const run = this.fetch(request).then((observed) => {
      if (
        epoch === this.epoch &&
        providerEpoch === (this.providerEpochs.get(scope) ?? 0) &&
        observed.revision
      ) {
        this.store(
          cacheKey(
            request.context,
            request.providerName,
            observed.revision,
            request.theme,
          ),
          observed,
        )
      }
      return observed
    })
    this.inFlight.set(requestKey, run)
    const release = () => {
      if (this.inFlight.get(requestKey) === run)
        this.inFlight.delete(requestKey)
    }
    run.then(release, release)
    return run
  }

  private async fetch(request: ProviderIconRequest): Promise<ObservedIcon> {
    const raw = await this.cli.run<unknown>(
      [
        "provider",
        "icon",
        request.providerName,
        "--theme",
        request.theme,
        "--context",
        request.context,
        "--result-format",
        "json",
      ],
      { maxBuffer: CLI_MAX_STDOUT_BYTES },
    )
    return toObservedIcon(raw)
  }

  private store(key: string, value: ObservedIcon): void {
    this.cache.delete(key)
    this.cache.set(key, value)
    while (this.cache.size > MAX_CACHE_ENTRIES) {
      const oldest = this.cache.keys().next().value
      if (oldest === undefined) break
      this.cache.delete(oldest)
    }
  }
}

function cacheKey(
  context: string,
  providerName: string,
  revision: string,
  theme: ProviderIconTheme,
): string {
  return `${context}\0${providerName}\0${revision}\0${theme}`
}
