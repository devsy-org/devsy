<script lang="ts">
import { onMount } from "svelte"
import { Button } from "$lib/components/ui/button/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import { Label } from "$lib/components/ui/label/index.js"
import { Separator } from "$lib/components/ui/separator/index.js"
import * as Command from "$lib/components/ui/command/index.js"
import * as Popover from "$lib/components/ui/popover/index.js"
import { Switch } from "$lib/components/ui/switch/index.js"
import {
  theme,
  applyTheme,
  uiScale,
  applyUIScale,
  defaultIde,
  setDefaultIde,
  fixedIde,
  setFixedIde,
  localOptions as localOptionsStore,
  loadLocalOptions,
  saveLocalOption,
  runAtStartup,
  openToTrayOnStartup,
  trayNotifications,
  desktopLogLevel,
  startupStatus,
  syncDesktopSettingsFromMain,
  updateDesktopSettings,
} from "$lib/stores/settings.js"
import type {
  Theme,
  UIScale,
  LocalOptions,
  OnBuildFailure,
} from "$lib/stores/settings.js"
import type { LogLevel, TrayNotificationLevel } from "$shared/app-settings.js"
import * as Select from "$lib/components/ui/select/index.js"
import UpdatesPanel from "$lib/components/update/UpdatesPanel.svelte"
import { Skeleton } from "$lib/components/ui/skeleton/index.js"
import { trackEngagement } from "$lib/analytics.js"

const THEMES: { value: Theme; label: string }[] = [
  { value: "light", label: "Light" },
  { value: "dark", label: "Dark" },
  { value: "system", label: "System" },
]

function setTheme(value: Theme) {
  theme.set(value)
  applyTheme(value)
}

const UI_SCALES: { value: UIScale; label: string }[] = [
  { value: "xs", label: "Extra Small" },
  { value: "sm", label: "Small" },
  { value: "md", label: "Default" },
  { value: "lg", label: "Large" },
  { value: "xl", label: "Extra Large" },
]

function setUIScale(value: UIScale) {
  uiScale.set(value)
  applyUIScale(value)
}

const IDE_OPTIONS = [
  { value: "none", label: "None" },
  { value: "vscode", label: "VS Code" },
  { value: "openvscode", label: "OpenVSCode Server" },
  { value: "vscode-web", label: "VS Code for the Web" },
  { value: "cursor", label: "Cursor" },
  { value: "zed", label: "Zed" },
  { value: "codium", label: "VSCodium" },
  { value: "windsurf", label: "Windsurf Editor" },
  { value: "antigravity", label: "Google Antigravity" },
  { value: "bob", label: "IBM Bob" },
  { value: "intellij", label: "IntelliJ IDEA" },
  { value: "pycharm", label: "PyCharm" },
  { value: "phpstorm", label: "PhpStorm" },
  { value: "rider", label: "Rider" },
  { value: "fleet", label: "Fleet" },
  { value: "goland", label: "GoLand" },
  { value: "webstorm", label: "WebStorm" },
  { value: "rustrover", label: "RustRover" },
  { value: "rubymine", label: "RubyMine" },
  { value: "clion", label: "CLion" },
  { value: "dataspell", label: "DataSpell" },
  { value: "jupyternotebook", label: "Jupyter Notebook" },
  { value: "vscode-insiders", label: "VS Code Insiders" },
  { value: "positron", label: "Positron" },
  { value: "rstudio", label: "RStudio Server" },
]

let loading = $state(true)
let saving = $state(false)
let ideComboOpen = $state(false)
let ideSearch = $state("")

let filteredIdes = $derived(
  ideSearch
    ? IDE_OPTIONS.filter((i) =>
        i.label.toLowerCase().includes(ideSearch.toLowerCase()),
      )
    : IDE_OPTIONS,
)

let local = $state<LocalOptions>({
  debugFlag: false,
  sshKeyPath: "",
  httpProxy: "",
  httpsProxy: "",
  noProxy: "",
  additionalCliFlags: "",
  additionalEnvVars: "",
  experimentalMultiDevcontainer: false,
  onBuildFailure: "prompt",
})

const ON_BUILD_FAILURE_OPTIONS: { value: OnBuildFailure; label: string }[] = [
  { value: "prompt", label: "Prompt with recovery options" },
  { value: "auto-recovery", label: "Automatically open recovery container" },
  { value: "nothing", label: "Do nothing" },
]

const shortcuts = [
  { keys: "Cmd/Ctrl + K", action: "Open command palette" },
  { keys: "Cmd/Ctrl + N", action: "New workspace" },
  { keys: "Cmd/Ctrl + 1-9, 0", action: "Navigate sections" },
  { keys: "Escape", action: "Close dialogs and palette" },
]

const NOTIFICATION_OPTIONS: { value: TrayNotificationLevel; label: string }[] =
  [
    { value: "off", label: "Off" },
    { value: "failures", label: "Failures only" },
    { value: "all", label: "All terminal outcomes" },
  ]

const LOG_LEVEL_OPTIONS: { value: LogLevel; label: string }[] = [
  { value: "error", label: "Error" },
  { value: "warn", label: "Warn" },
  { value: "info", label: "Info" },
  { value: "debug", label: "Debug" },
  { value: "trace", label: "Trace" },
]

onMount(() => {
  local = loadLocalOptions()
  localOptionsStore.set(local)
  loading = false
  void syncDesktopSettingsFromMain()
})

function saveLocal(key: keyof LocalOptions, value: string | boolean) {
  saveLocalOption(key, value)
  ;(local as unknown as Record<string, string | boolean>)[key] = value
  trackEngagement("settings_changed", { setting: key })
}

function toggleLocal(key: keyof LocalOptions) {
  const current = local[key]
  saveLocal(key, !current)
}
</script>

<div class="space-y-6">
  <h1 class="text-2xl font-bold">Settings</h1>

  <div class="min-w-0 max-w-3xl space-y-6">
      <section id="general" aria-labelledby="general-heading" class="scroll-mt-6 rounded-lg border p-4 sm:p-6">
        <h2 id="general-heading" class="text-lg font-semibold">General</h2>
      {#if loading}
        <div class="mt-4 space-y-6">
          <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div class="space-y-1.5">
              <Skeleton class="h-4 w-32" />
              <Skeleton class="h-3 w-48" />
            </div>
            <Skeleton class="h-5 w-10 rounded-full" />
          </div>
          <div class="space-y-2">
            <Skeleton class="h-4 w-40" />
            <Skeleton class="h-3 w-56" />
            <Skeleton class="h-9 w-full" />
          </div>

          <Separator />
          <Skeleton class="h-5 w-40" />
          <div class="space-y-2">
            <Skeleton class="h-4 w-24" />
            <Skeleton class="h-9 w-full" />
          </div>
          <div class="space-y-2">
            <Skeleton class="h-4 w-28" />
            <Skeleton class="h-9 w-full" />
          </div>
        </div>
      {:else}
      <div class="mt-4 space-y-6">
        <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <Label>Debug Mode</Label>
            <p class="text-xs text-muted-foreground">Run all commands with --debug flag</p>
          </div>
          <Switch checked={local.debugFlag} onCheckedChange={() => toggleLocal("debugFlag")} disabled={loading || saving} />
        </div>

        <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <Label>Logging Level</Label>
            <p class="text-xs text-muted-foreground">Controls desktop and launched CLI diagnostic logs</p>
          </div>
          <Select.Root
            type="single"
            value={$desktopLogLevel}
            onValueChange={(v) => {
              if (v) updateDesktopSettings({ logLevel: v as LogLevel })
            }}
          >
            <Select.Trigger class="h-9 w-full sm:w-[280px]"><span>{LOG_LEVEL_OPTIONS.find((o) => o.value === $desktopLogLevel)?.label ?? "Info"}</span></Select.Trigger>
            <Select.Content>
              {#each LOG_LEVEL_OPTIONS as o (o.value)}
                <Select.Item value={o.value} label={o.label} />
              {/each}
            </Select.Content>
          </Select.Root>
        </div>

        <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <Label>On Build Failure</Label>
            <p class="text-xs text-muted-foreground">What to do when a dev container build fails</p>
          </div>
          <Select.Root
            type="single"
            value={local.onBuildFailure}
            onValueChange={(v) => {
              if (v) {
                local.onBuildFailure = v as OnBuildFailure
                saveLocal("onBuildFailure", v)
              }
            }}
          >
            <Select.Trigger class="h-9 w-full sm:w-[280px]">
              <span>{ON_BUILD_FAILURE_OPTIONS.find((o) => o.value === local.onBuildFailure)?.label ?? "Prompt with recovery options"}</span>
            </Select.Trigger>
            <Select.Content>
              {#each ON_BUILD_FAILURE_OPTIONS as o (o.value)}
                <Select.Item value={o.value} label={o.label} />
              {/each}
            </Select.Content>
          </Select.Root>
        </div>

        <div class="space-y-2">
          <Label>SSH Key for Git Commit Signing</Label>
          <p class="text-xs text-muted-foreground">Path to SSH key for signing Git commits</p>
          <Input
            value={local.sshKeyPath}
            placeholder="~/.ssh/id_ed25519"
            oninput={(e) => (local.sshKeyPath = e.currentTarget.value)}
            onblur={() => saveLocal("sshKeyPath", local.sshKeyPath)}
            disabled={loading || saving}
          />
        </div>

        <Separator />

        <div class="space-y-4">
          <h2 class="text-lg font-semibold">Proxy Configuration</h2>
          <div class="space-y-2">
            <Label>HTTP Proxy</Label>
            <Input
              value={local.httpProxy}
              placeholder="http://proxy:8080"
              oninput={(e) => (local.httpProxy = e.currentTarget.value)}
              onblur={() => saveLocal("httpProxy", local.httpProxy)}
              disabled={loading || saving}
            />
          </div>
          <div class="space-y-2">
            <Label>HTTPS Proxy</Label>
            <Input
              value={local.httpsProxy}
              placeholder="https://proxy:8443"
              oninput={(e) => (local.httpsProxy = e.currentTarget.value)}
              onblur={() => saveLocal("httpsProxy", local.httpsProxy)}
              disabled={loading || saving}
            />
          </div>
          <div class="space-y-2">
            <Label>No Proxy</Label>
            <Input
              value={local.noProxy}
              placeholder="localhost,127.0.0.1,.internal"
              oninput={(e) => (local.noProxy = e.currentTarget.value)}
              onblur={() => saveLocal("noProxy", local.noProxy)}
              disabled={loading || saving}
            />
          </div>
        </div>

        <Separator />

        <div class="space-y-2">
          <h2 class="text-lg font-semibold">Keyboard Shortcuts</h2>
          <div class="rounded-md border divide-y">
            {#each shortcuts as shortcut}
              <div class="flex items-center justify-between px-4 py-2 text-sm">
                <span class="text-muted-foreground">{shortcut.action}</span>
                <kbd class="rounded border bg-muted px-2 py-0.5 text-xs font-mono">{shortcut.keys}</kbd>
              </div>
            {/each}
          </div>
        </div>

      </div>
      {/if}
      </section>

      <section id="startup" aria-labelledby="startup-heading" class="scroll-mt-6 rounded-lg border p-4 sm:p-6">
        <h2 id="startup-heading" class="text-lg font-semibold">Startup</h2>
        <div class="mt-4 space-y-6">
          <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <Label>Run at Startup</Label>
              <p class="text-xs text-muted-foreground">Launch Devsy automatically after you sign in</p>
            </div>
            <Switch
              checked={$runAtStartup}
              onCheckedChange={(v) => updateDesktopSettings({ runAtStartup: v })}
              disabled={loading}
            />
          </div>

          {#if $startupStatus?.status === "denied" || $startupStatus?.status === "error"}
            <p class="text-xs text-yellow-600 dark:text-yellow-400">{$startupStatus.detail}</p>
          {/if}

          <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <Label>Open to Tray on Startup</Label>
              <p class="text-xs text-muted-foreground">Start in the system tray without opening a window. Applies only when Devsy starts automatically; clicking Devsy yourself always opens the window.</p>
            </div>
            <Switch
              checked={$openToTrayOnStartup}
              onCheckedChange={(v) => updateDesktopSettings({ openToTrayOnStartup: v })}
              disabled={loading || !$runAtStartup}
            />
          </div>
        </div>
      </section>

      <section id="notifications" aria-labelledby="notifications-heading" class="scroll-mt-6 rounded-lg border p-4 sm:p-6">
        <h2 id="notifications-heading" class="text-lg font-semibold">Notifications</h2>
        <div class="mt-4 space-y-6">
          <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <Label>Workspace Notifications</Label>
              <p class="text-xs text-muted-foreground">System notifications when workspace start and stop operations finish</p>
            </div>
            <Select.Root
              type="single"
              value={$trayNotifications}
              onValueChange={(v) => {
                if (v) updateDesktopSettings({ trayNotifications: v as TrayNotificationLevel })
              }}
            >
              <Select.Trigger class="h-9 w-full sm:w-[280px]">
                <span>{NOTIFICATION_OPTIONS.find((o) => o.value === $trayNotifications)?.label ?? "Failures only"}</span>
              </Select.Trigger>
              <Select.Content>
                {#each NOTIFICATION_OPTIONS as o (o.value)}
                  <Select.Item value={o.value} label={o.label} />
                {/each}
              </Select.Content>
            </Select.Root>
          </div>
        </div>
      </section>

      <section id="appearance" aria-labelledby="appearance-heading" class="scroll-mt-6 rounded-lg border p-4 sm:p-6">
        <h2 id="appearance-heading" class="text-lg font-semibold">Appearance</h2>
        <div class="mt-4 space-y-6">
        <div class="space-y-2">
          <h2 class="text-lg font-semibold">Theme</h2>
          <div class="flex flex-wrap gap-2">
            {#each THEMES as t (t.value)}
              <Button
                variant={$theme === t.value ? "default" : "outline"}
                onclick={() => setTheme(t.value)}
              >
                {t.label}
              </Button>
            {/each}
          </div>
        </div>

        <Separator />


        <div class="space-y-2">
          <h2 class="text-lg font-semibold">UI Scale</h2>
          <p class="text-xs text-muted-foreground">Adjust the overall size of text and interface elements</p>
          <div class="flex flex-wrap gap-2">
            {#each UI_SCALES as s (s.value)}
              <Button
                variant={$uiScale === s.value ? "default" : "outline"}
                onclick={() => setUIScale(s.value)}
              >
                {s.label}
              </Button>
            {/each}
          </div>
        </div>

      </div>
      </section>

      <section id="updates" aria-labelledby="updates-heading" class="scroll-mt-6 rounded-lg border p-4 sm:p-6">
        <h2 id="updates-heading" class="text-lg font-semibold">Updates</h2>
        <div class="mt-4">
          <UpdatesPanel />
        </div>
      </section>

      <section id="advanced" aria-labelledby="advanced-heading" class="scroll-mt-6 rounded-lg border p-4 sm:p-6">
        <h2 id="advanced-heading" class="text-lg font-semibold">Advanced</h2>
        <div class="mt-4 space-y-6">
        <div class="rounded-md border border-yellow-500/30 bg-yellow-500/5 p-3">
          <p class="text-sm text-yellow-600 dark:text-yellow-400">
            Experimental features may be unstable. Use at your own risk.
          </p>
        </div>

        <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <Label>Multiple Devcontainer Detection</Label>
            <p class="text-xs text-muted-foreground">Check for multiple devcontainers when creating workspaces. May take longer for larger repos.</p>
          </div>
          <Switch checked={local.experimentalMultiDevcontainer} onCheckedChange={() => toggleLocal("experimentalMultiDevcontainer")} disabled={loading || saving} />
        </div>

        <Separator />

        <div class="space-y-2">
          <Label>Additional CLI Flags</Label>
          <p class="text-xs text-muted-foreground">Append custom flags to all Devsy CLI commands</p>
          <Input
            value={local.additionalCliFlags}
            placeholder="--flag1 --flag2=value"
            oninput={(e) => (local.additionalCliFlags = e.currentTarget.value)}
            onblur={() => saveLocal("additionalCliFlags", local.additionalCliFlags)}
            disabled={loading || saving}
          />
        </div>

        <div class="space-y-2">
          <Label>Additional Environment Variables</Label>
          <p class="text-xs text-muted-foreground">Comma-separated environment variables passed to Devsy commands</p>
          <Input
            value={local.additionalEnvVars}
            placeholder="FOO=bar,BAZ=false"
            oninput={(e) => (local.additionalEnvVars = e.currentTarget.value)}
            onblur={() => saveLocal("additionalEnvVars", local.additionalEnvVars)}
            disabled={loading || saving}
          />
        </div>
        </div>
      </section>
  </div>
</div>
