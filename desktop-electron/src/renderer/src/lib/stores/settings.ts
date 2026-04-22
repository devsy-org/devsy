import { writable } from "svelte/store"

const browser = typeof window !== "undefined"

// ── UI Settings (localStorage) ──────────────────────────────────────

export type Theme = "light" | "dark" | "system"
export type ColorScheme = "default" | "emerald" | "purple"
export type UIScale = "xs" | "sm" | "md" | "lg" | "xl"
export type SidebarPosition = "left" | "right"

const STORAGE_KEY = "devpod-theme"
const COLOR_SCHEME_KEY = "devpod-color-scheme"
const UI_SCALE_KEY = "devpod-ui-scale"
const SIDEBAR_KEY = "devpod-sidebar-position"
const AUTO_UPDATE_KEY = "devpod-auto-update"
const FIXED_IDE_KEY = "devpod-fixed-ide"
const DEFAULT_IDE_KEY = "devpod-default-ide"

const UI_SCALE_CLASSES: Record<UIScale, string> = {
  xs: "ui-scale-xs",
  sm: "ui-scale-sm",
  md: "",
  lg: "ui-scale-lg",
  xl: "ui-scale-xl",
}

function getStored<T extends string>(
  key: string,
  valid: readonly T[],
  fallback: T,
): T {
  if (browser) {
    const stored = localStorage.getItem(key)
    if (stored && (valid as readonly string[]).includes(stored)) {
      return stored as T
    }
  }
  return fallback
}

function getStoredBool(key: string, fallback: boolean): boolean {
  if (browser) {
    const stored = localStorage.getItem(key)
    if (stored === "true") return true
    if (stored === "false") return false
  }
  return fallback
}

function getStoredString(key: string, fallback: string): string {
  if (browser) {
    return localStorage.getItem(key) ?? fallback
  }
  return fallback
}

// Theme
export const theme = writable<Theme>(
  getStored(STORAGE_KEY, ["light", "dark", "system"] as const, "dark"),
)

export function applyTheme(value: Theme) {
  if (!browser) return
  localStorage.setItem(STORAGE_KEY, value)
  const root = document.documentElement
  if (value === "system") {
    const prefersDark = window.matchMedia(
      "(prefers-color-scheme: dark)",
    ).matches
    root.classList.toggle("dark", prefersDark)
  } else {
    root.classList.toggle("dark", value === "dark")
  }
}

export function cycleTheme() {
  theme.update((current) => {
    const next: Theme =
      current === "light" ? "dark" : current === "dark" ? "system" : "light"
    applyTheme(next)
    return next
  })
}

// Color scheme (accent)
export const colorScheme = writable<ColorScheme>(
  getStored(
    COLOR_SCHEME_KEY,
    ["default", "emerald", "purple"] as const,
    "emerald",
  ),
)

const COLOR_SCHEME_CLASSES: ColorScheme[] = ["emerald", "purple"]

export function applyColorScheme(value: ColorScheme) {
  if (!browser) return
  localStorage.setItem(COLOR_SCHEME_KEY, value)
  const root = document.documentElement
  for (const cls of COLOR_SCHEME_CLASSES) {
    root.classList.remove(`theme-${cls}`)
  }
  if (value !== "default") {
    root.classList.add(`theme-${value}`)
  }
}

export function setColorScheme(value: ColorScheme) {
  colorScheme.set(value)
  applyColorScheme(value)
}

// UI Scale
export const uiScale = writable<UIScale>(
  getStored(UI_SCALE_KEY, ["xs", "sm", "md", "lg", "xl"] as const, "md"),
)

export function applyUIScale(value: UIScale) {
  if (!browser) return
  localStorage.setItem(UI_SCALE_KEY, value)
  const root = document.documentElement
  for (const cls of Object.values(UI_SCALE_CLASSES)) {
    if (cls) root.classList.remove(cls)
  }
  const cls = UI_SCALE_CLASSES[value]
  if (cls) root.classList.add(cls)
}

// Sidebar position
export const sidebarPosition = writable<SidebarPosition>(
  getStored(SIDEBAR_KEY, ["left", "right"] as const, "left"),
)

export function setSidebarPosition(value: SidebarPosition) {
  if (browser) localStorage.setItem(SIDEBAR_KEY, value)
  sidebarPosition.set(value)
}

// Auto-update
export const autoUpdate = writable<boolean>(
  getStoredBool(AUTO_UPDATE_KEY, true),
)

export function setAutoUpdate(value: boolean) {
  if (browser) localStorage.setItem(AUTO_UPDATE_KEY, String(value))
  autoUpdate.set(value)
}

// Default IDE
export const defaultIde = writable<string>(
  getStoredString(DEFAULT_IDE_KEY, "vscode"),
)

export function setDefaultIde(value: string) {
  if (browser) localStorage.setItem(DEFAULT_IDE_KEY, value)
  defaultIde.set(value)
}

// Fixed IDE (always use default)
export const fixedIde = writable<boolean>(getStoredBool(FIXED_IDE_KEY, false))

export function setFixedIde(value: boolean) {
  if (browser) localStorage.setItem(FIXED_IDE_KEY, String(value))
  fixedIde.set(value)
}

// ── Context Options (DevPod CLI) ────────────────────────────────────

// Options stored in DevPod CLI context (devpod context set-options)
export interface ContextOptions {
  telemetry: boolean
  agentUrl: string
  dotfilesUrl: string
  dotfilesScript: string
  dockerCredentialForwarding: boolean
  gitCredentialForwarding: boolean
  gitSshSignatureForwarding: boolean
  sshAgentForwarding: boolean
  sshAddPrivateKeys: boolean
  sshStrictHostKeyChecking: boolean
  gpgAgentForwarding: boolean
  agentInjectTimeout: string
  registryCache: string
  exitAfterTimeout: boolean
  sshConfigPath: string
  sshConfigIncludePath: string
}

// Options stored locally (not supported by DevPod CLI context)
export interface LocalOptions {
  debugFlag: boolean
  sshKeyPath: string
  httpProxy: string
  httpsProxy: string
  noProxy: string
  additionalCliFlags: string
  additionalEnvVars: string
  experimentalMultiDevcontainer: boolean
}

export const DEFAULT_CONTEXT_OPTIONS: ContextOptions = {
  telemetry: true,
  agentUrl: "",
  dotfilesUrl: "",
  dotfilesScript: "",
  dockerCredentialForwarding: true,
  gitCredentialForwarding: true,
  gitSshSignatureForwarding: true,
  sshAgentForwarding: true,
  sshAddPrivateKeys: true,
  sshStrictHostKeyChecking: false,
  gpgAgentForwarding: false,
  agentInjectTimeout: "20",
  registryCache: "",
  exitAfterTimeout: true,
  sshConfigPath: "",
  sshConfigIncludePath: "",
}

export const DEFAULT_LOCAL_OPTIONS: LocalOptions = {
  debugFlag: false,
  sshKeyPath: "",
  httpProxy: "",
  httpsProxy: "",
  noProxy: "",
  additionalCliFlags: "",
  additionalEnvVars: "",
  experimentalMultiDevcontainer: false,
}

// Map from our keys to DevPod CLI context option keys
export const CONTEXT_OPTION_KEYS: Record<keyof ContextOptions, string> = {
  telemetry: "TELEMETRY",
  agentUrl: "AGENT_URL",
  dotfilesUrl: "DOTFILES_URL",
  dotfilesScript: "DOTFILES_SCRIPT",
  dockerCredentialForwarding: "SSH_INJECT_DOCKER_CREDENTIALS",
  gitCredentialForwarding: "SSH_INJECT_GIT_CREDENTIALS",
  gitSshSignatureForwarding: "GIT_SSH_SIGNATURE_FORWARDING",
  sshAgentForwarding: "SSH_AGENT_FORWARDING",
  sshAddPrivateKeys: "SSH_ADD_PRIVATE_KEYS",
  sshStrictHostKeyChecking: "SSH_STRICT_HOST_KEY_CHECKING",
  gpgAgentForwarding: "GPG_AGENT_FORWARDING",
  agentInjectTimeout: "AGENT_INJECT_TIMEOUT",
  registryCache: "REGISTRY_CACHE",
  exitAfterTimeout: "EXIT_AFTER_TIMEOUT",
  sshConfigPath: "SSH_CONFIG_PATH",
  sshConfigIncludePath: "SSH_CONFIG_INCLUDE_PATH",
}

export const contextOptions = writable<ContextOptions>({
  ...DEFAULT_CONTEXT_OPTIONS,
})

const LOCAL_OPTIONS_KEY = "devpod-local-options"

export const localOptions = writable<LocalOptions>({
  ...DEFAULT_LOCAL_OPTIONS,
})

export function loadLocalOptions(): LocalOptions {
  if (!browser) return { ...DEFAULT_LOCAL_OPTIONS }
  try {
    const stored = localStorage.getItem(LOCAL_OPTIONS_KEY)
    if (stored) {
      return { ...DEFAULT_LOCAL_OPTIONS, ...JSON.parse(stored) }
    }
  } catch {
    // ignore
  }
  return { ...DEFAULT_LOCAL_OPTIONS }
}

export function saveLocalOption(
  key: keyof LocalOptions,
  value: string | boolean,
) {
  if (!browser) return
  const current = loadLocalOptions()
  ;(current as unknown as Record<string, string | boolean>)[key] = value
  localStorage.setItem(LOCAL_OPTIONS_KEY, JSON.stringify(current))
  localOptions.set(current)
}

export function parseContextOptions(
  raw: Record<string, { value?: string }>,
): ContextOptions {
  function str(key: string, fallback = ""): string {
    return raw[key]?.value ?? fallback
  }
  function bool(key: string, fallback = false): boolean {
    const v = raw[key]?.value
    if (v === undefined || v === "") return fallback
    return v !== "false"
  }

  return {
    telemetry: bool("TELEMETRY", true),
    agentUrl: str("AGENT_URL"),
    dotfilesUrl: str("DOTFILES_URL"),
    dotfilesScript: str("DOTFILES_SCRIPT"),
    dockerCredentialForwarding: bool("SSH_INJECT_DOCKER_CREDENTIALS", true),
    gitCredentialForwarding: bool("SSH_INJECT_GIT_CREDENTIALS", true),
    gitSshSignatureForwarding: bool("GIT_SSH_SIGNATURE_FORWARDING", true),
    sshAgentForwarding: bool("SSH_AGENT_FORWARDING", true),
    sshAddPrivateKeys: bool("SSH_ADD_PRIVATE_KEYS", true),
    sshStrictHostKeyChecking: bool("SSH_STRICT_HOST_KEY_CHECKING"),
    gpgAgentForwarding: bool("GPG_AGENT_FORWARDING"),
    agentInjectTimeout: str("AGENT_INJECT_TIMEOUT", "20"),
    registryCache: str("REGISTRY_CACHE"),
    exitAfterTimeout: bool("EXIT_AFTER_TIMEOUT", true),
    sshConfigPath: str("SSH_CONFIG_PATH"),
    sshConfigIncludePath: str("SSH_CONFIG_INCLUDE_PATH"),
  }
}

// ── Init ────────────────────────────────────────────────────────────

export function initSettings() {
  const unsubTheme = theme.subscribe((value) => {
    applyTheme(value)
  })
  const unsubColor = colorScheme.subscribe((value) => {
    applyColorScheme(value)
  })
  const unsubScale = uiScale.subscribe((value) => {
    applyUIScale(value)
  })
  const unsubscribe = () => {
    unsubTheme()
    unsubColor()
    unsubScale()
  }

  if (browser) {
    const mediaQuery = window.matchMedia("(prefers-color-scheme: dark)")
    const handler = () => {
      let current = "dark" as Theme
      theme.subscribe((v) => (current = v))()
      if (current === "system") {
        applyTheme("system")
      }
    }
    mediaQuery.addEventListener("change", handler)
    return () => {
      unsubscribe()
      mediaQuery.removeEventListener("change", handler)
    }
  }

  return unsubscribe
}
