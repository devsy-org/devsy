import { execFile } from "node:child_process"
import { createHash, randomUUID } from "node:crypto"
import {
  linkSync,
  lstatSync,
  mkdirSync,
  readdirSync,
  readFileSync,
  realpathSync,
  renameSync,
  statSync,
  unlinkSync,
  writeFileSync,
} from "node:fs"
import { dirname, isAbsolute, join } from "node:path"
import { promisify } from "node:util"
import type {
  LinuxDesktopIntegrationResult,
  LinuxDesktopIntegrationStatus,
} from "../shared/linux-desktop-integration.js"

export const APP_ID = "sh.devsy.app"
const ENTRY_FILE = `${APP_ID}.desktop`
const OWNER_KEY = "X-Devsy-AppImage-Integration"
const PATH_KEY = "X-Devsy-AppImage-Path"
const ICON_KEY = "X-Devsy-AppImage-Icon"
const ICON_HASH_KEY = "X-Devsy-AppImage-Icon-SHA256"
const DEFAULT_DATA_DIRS = "/usr/local/share:/usr/share"

// The Desktop Entry Exec grammar reserves these characters, so an argument
// containing any of them must be double-quoted.
const EXEC_RESERVED = /[\s"'\\<>~|&;$*?#()`]/
// Inside double quotes these four need a backslash.
const EXEC_QUOTED_ESCAPES = /["`$\\]/g
const CONTROL_CHARS = /\p{Cc}/u

export function escapeDesktopString(value: string): string {
  return value.replace(/\\/g, "\\\\")
}

export function unescapeDesktopString(value: string): string {
  return value.replace(/\\([\\nrts])/g, (_match, code: string) => {
    switch (code) {
      case "n":
        return "\n"
      case "r":
        return "\r"
      case "t":
        return "\t"
      case "s":
        return " "
      default:
        return "\\"
    }
  })
}

/** Quotes one Exec argument and applies the desktop-file string escaping. */
export function quoteExecArgument(argument: string): string {
  const percentEscaped = argument.replace(/%/g, "%%")
  const execValue = EXEC_RESERVED.test(argument)
    ? `"${percentEscaped.replace(EXEC_QUOTED_ESCAPES, "\\$&")}"`
    : percentEscaped
  return escapeDesktopString(execValue)
}

/** Splits an Exec value (as stored in the file) into its arguments. */
export function parseExec(raw: string): string[] {
  const value = unescapeDesktopString(raw)
  const args: string[] = []
  let current = ""
  let inQuotes = false
  let started = false
  for (let i = 0; i < value.length; i++) {
    const char = value[i]
    if (inQuotes) {
      if (char === "\\" && i + 1 < value.length) current += value[++i]
      else if (char === '"') inQuotes = false
      else current += char
    } else if (char === '"') {
      inQuotes = true
      started = true
    } else if (char === " " || char === "\t") {
      if (started) args.push(current)
      current = ""
      started = false
    } else {
      current += char
      started = true
    }
  }
  if (started) args.push(current)
  return args.map((arg) => arg.replace(/%%/g, "%"))
}

interface DesktopEntry {
  path: string
  values: Map<string, string>
}

function parseDesktopEntry(path: string, text: string): DesktopEntry {
  const values = new Map<string, string>()
  let inGroup = false
  for (const rawLine of text.split("\n")) {
    const line = rawLine.trim()
    if (line.startsWith("[")) {
      inGroup = line === "[Desktop Entry]"
    } else if (inGroup && line && !line.startsWith("#")) {
      const separator = line.indexOf("=")
      if (separator > 0) {
        values.set(
          line.slice(0, separator).trim(),
          line.slice(separator + 1).trim(),
        )
      }
    }
  }
  return { path, values }
}

function readDesktopEntries(directory: string): DesktopEntry[] {
  let names: string[]
  try {
    names = readdirSync(directory)
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code === "ENOENT") return []
    throw new Error(`Could not inspect ${directory}: ${describeError(err)}`)
  }
  const entries: DesktopEntry[] = []
  for (const name of names) {
    const path = join(directory, name)
    if (name.endsWith(".desktop")) {
      try {
        entries.push(parseDesktopEntry(path, readFileSync(path, "utf-8")))
      } catch (err) {
        throw new Error(
          `Could not inspect launcher ${path}: ${describeError(err)}`,
        )
      }
    } else if (lstatSync(path).isDirectory()) {
      entries.push(...readDesktopEntries(path))
    }
  }
  return entries
}

export interface LinuxDesktopIntegrationOptions {
  platform: NodeJS.Platform
  env: Record<string, string | undefined>
  homeDir: string
  /** PNG installed as the launcher icon. */
  iconSource: string
  /** Best-effort cache refresh after the applications directory changes. */
  refreshDesktopDatabase: (applicationsDir: string) => Promise<void>
}

function describeError(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

export class LinuxDesktopIntegration {
  constructor(private readonly options: LinuxDesktopIntegrationOptions) {}

  status(): LinuxDesktopIntegrationStatus {
    const rawPath = this.options.env.APPIMAGE
    if (
      this.options.platform !== "linux" ||
      this.options.env.FLATPAK_ID ||
      !rawPath
    ) {
      return { supported: false, installed: false, needsUpdate: false }
    }
    const problem = appImagePathProblem(rawPath)
    if (problem) {
      return {
        supported: true,
        installed: false,
        needsUpdate: false,
        problem,
      }
    }
    try {
      return { ...this.scan(rawPath), supported: true, appImagePath: rawPath }
    } catch (err) {
      return {
        supported: true,
        appImagePath: rawPath,
        installed: false,
        needsUpdate: false,
        problem: describeError(err),
      }
    }
  }

  /** Creates the launcher, or updates it when this integration owns it. */
  async install(): Promise<LinuxDesktopIntegrationResult> {
    return this.run(() => this.installLauncher())
  }

  /** Removes only the launcher and icon this integration wrote. */
  async remove(): Promise<LinuxDesktopIntegrationResult> {
    return this.run(() => this.removeLauncher())
  }

  /** Follows a moved AppImage; never creates a launcher the user declined. */
  async refreshOwned(): Promise<LinuxDesktopIntegrationResult> {
    const status = this.status()
    if (
      !status.installed ||
      !status.needsUpdate ||
      status.conflict ||
      status.problem
    ) {
      return { status }
    }
    return this.install()
  }

  private async run(
    operation: () => Promise<void>,
  ): Promise<LinuxDesktopIntegrationResult> {
    try {
      await operation()
      return { status: this.status() }
    } catch (err) {
      return { status: this.status(), error: describeError(err) }
    }
  }

  private dataHome(): string {
    const configured = this.options.env.XDG_DATA_HOME
    return configured?.startsWith("/")
      ? configured
      : join(this.options.homeDir, ".local", "share")
  }

  private applicationDirs(): string[] {
    const dataDirs = (this.options.env.XDG_DATA_DIRS || DEFAULT_DATA_DIRS)
      .split(":")
      .filter((dir) => dir.startsWith("/"))
    const flatpakExports = [
      join(
        this.options.homeDir,
        ".local",
        "share",
        "flatpak",
        "exports",
        "share",
      ),
      "/var/lib/flatpak/exports/share",
    ]
    return [...new Set([this.dataHome(), ...dataDirs, ...flatpakExports])].map(
      (dir) => join(dir, "applications"),
    )
  }

  private userEntryPath(): string {
    return join(this.dataHome(), "applications", ENTRY_FILE)
  }

  private iconPath(): string {
    return join(
      this.dataHome(),
      "icons",
      "hicolor",
      "128x128",
      "apps",
      `${APP_ID}.png`,
    )
  }

  private scan(appImagePath: string): {
    installed: boolean
    needsUpdate: boolean
    conflict?: string
  } {
    const userEntry = this.userEntryPath()
    const canonicalImage = realpathSync(appImagePath)
    let owned: DesktopEntry | undefined
    let conflict: string | undefined
    for (const directory of this.applicationDirs()) {
      for (const entry of readDesktopEntries(directory)) {
        if (
          entry.path === userEntry &&
          entry.values.get(OWNER_KEY) === "true" &&
          entry.values.get(PATH_KEY) &&
          !lstatSync(userEntry).isSymbolicLink()
        ) {
          owned = entry
        } else if (entry.path.endsWith(`/${ENTRY_FILE}`)) {
          conflict ??= `Devsy already has a launcher at ${entry.path}.`
        } else if (targetsAppImage(entry, canonicalImage)) {
          conflict ??= `This AppImage is already integrated by ${entry.path}.`
        } else if (
          entry.values
            .get("MimeType")
            ?.split(";")
            .includes("x-scheme-handler/devsy")
        ) {
          conflict ??= `Another devsy:// handler already exists at ${entry.path}.`
        }
      }
    }
    if (!owned) return { installed: false, needsUpdate: false, conflict }
    const recordedPath = unescapeDesktopString(owned.values.get(PATH_KEY) ?? "")
    return {
      installed: true,
      needsUpdate: recordedPath !== appImagePath || !isFile(this.iconPath()),
      conflict,
    }
  }

  private async installLauncher(): Promise<void> {
    const status = this.status()
    if (status.problem) throw new Error(status.problem)
    if (!status.supported || !status.appImagePath) {
      throw new Error(
        "Adding Devsy to applications only works from an AppImage.",
      )
    }
    if (status.conflict) throw new Error(status.conflict)

    const userEntry = this.userEntryPath()
    const iconPath = this.iconPath()
    const iconExists = pathExists(iconPath)
    if (iconExists && !this.ownsIcon(userEntry)) {
      throw new Error(
        `An icon Devsy does not own already exists at ${iconPath}.`,
      )
    }
    const icon = readFileSync(this.options.iconSource)
    const iconHash = createHash("sha256").update(icon).digest("hex")
    writeAtomically(iconPath, icon, iconExists)
    try {
      writeAtomically(
        userEntry,
        renderDesktopEntry(status.appImagePath, iconPath, iconHash),
        status.installed,
      )
    } catch (err) {
      if (!iconExists) removeIfPresent(iconPath)
      throw err
    }
    await this.options.refreshDesktopDatabase(dirname(userEntry))
  }

  private async removeLauncher(): Promise<void> {
    const status = this.status()
    if (status.problem) throw new Error(status.problem)
    if (!status.supported || !status.installed) {
      throw new Error("Devsy did not add this launcher, so it was left alone.")
    }
    const userEntry = this.userEntryPath()
    // Check references before mutation; unreadable application directories
    // must not turn uncertainty about shared ownership into deletion.
    const deleteIcon =
      this.ownsIcon(userEntry) && !this.iconUsedElsewhere(userEntry)
    unlinkSync(userEntry)
    if (deleteIcon) removeIfPresent(this.iconPath())
    await this.options.refreshDesktopDatabase(dirname(userEntry))
  }

  private ownsIcon(userEntry: string): boolean {
    try {
      const entry = parseDesktopEntry(
        userEntry,
        readFileSync(userEntry, "utf-8"),
      )
      if (
        entry.values.get(OWNER_KEY) !== "true" ||
        unescapeDesktopString(entry.values.get(ICON_KEY) ?? "") !==
          this.iconPath() ||
        !lstatSync(this.iconPath()).isFile()
      )
        return false
      const hash = createHash("sha256")
        .update(readFileSync(this.iconPath()))
        .digest("hex")
      return entry.values.get(ICON_HASH_KEY) === hash
    } catch {
      return false
    }
  }

  private iconUsedElsewhere(userEntry: string): boolean {
    return this.applicationDirs().some((directory) =>
      readDesktopEntries(directory).some(
        (entry) =>
          entry.path !== userEntry &&
          [APP_ID, this.iconPath()].includes(
            unescapeDesktopString(entry.values.get("Icon") ?? ""),
          ),
      ),
    )
  }
}

function appImagePathProblem(path: string): string | undefined {
  if (!isAbsolute(path) || CONTROL_CHARS.test(path)) {
    return "The AppImage path is not a usable absolute path."
  }
  if (path.includes("=")) {
    return "The AppImage path contains '=', which launchers cannot represent. Move the AppImage to a path without '=' and start it again."
  }
  if (!isFile(path)) return `The AppImage ${path} is not a file.`
  return undefined
}

function isFile(path: string): boolean {
  try {
    return statSync(path).isFile()
  } catch {
    return false
  }
}

function renderDesktopEntry(
  appImagePath: string,
  ownedIcon: string,
  iconHash: string,
): string {
  const lines = [
    "[Desktop Entry]",
    "Type=Application",
    "Name=Devsy",
    "Comment=Dev environments in any infrastructure",
    `Exec=${quoteExecArgument(appImagePath)} %U`,
    `Icon=${APP_ID}`,
    "Terminal=false",
    "Categories=Development;",
    "MimeType=x-scheme-handler/devsy;",
    `StartupWMClass=${APP_ID}`,
    `${OWNER_KEY}=true`,
    `${PATH_KEY}=${escapeDesktopString(appImagePath).replace(/ /g, "\\s")}`,
  ]
  lines.push(
    `${ICON_KEY}=${escapeDesktopString(ownedIcon).replace(/ /g, "\\s")}`,
  )
  lines.push(`${ICON_HASH_KEY}=${iconHash}`)
  return `${lines.join("\n")}\n`
}

function writeAtomically(
  path: string,
  content: string | Buffer,
  replace: boolean,
): void {
  const temporary = `${path}.${randomUUID()}.tmp`
  try {
    mkdirSync(dirname(path), { recursive: true })
    writeFileSync(temporary, content, { flag: "wx", mode: 0o644 })
    if (replace) renameSync(temporary, path)
    else linkSync(temporary, path)
  } catch (err) {
    throw new Error(`Could not write ${path}: ${describeError(err)}`)
  } finally {
    removeIfPresent(temporary)
  }
}

function pathExists(path: string): boolean {
  try {
    lstatSync(path)
    return true
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code === "ENOENT") return false
    throw err
  }
}

function removeIfPresent(path: string): void {
  try {
    unlinkSync(path)
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code !== "ENOENT") throw err
  }
}

/** Refreshes the MIME cache; the launcher works without the utility. */
export async function updateDesktopDatabase(
  applicationsDir: string,
): Promise<void> {
  await promisify(execFile)("update-desktop-database", [applicationsDir]).catch(
    () => {},
  )
}

function targetsAppImage(entry: DesktopEntry, canonicalImage: string): boolean {
  const args = parseExec(entry.values.get("Exec") ?? "")
  args.push(unescapeDesktopString(entry.values.get("TryExec") ?? ""))
  return args.some((argument) => {
    if (!isAbsolute(argument)) return false
    try {
      return realpathSync(argument) === canonicalImage
    } catch {
      return false
    }
  })
}
