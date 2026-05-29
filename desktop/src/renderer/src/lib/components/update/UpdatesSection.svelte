<script lang="ts">
import { onMount } from "svelte"
import { Check } from "@lucide/svelte"
import { Button } from "$lib/components/ui/button/index.js"
import { Label } from "$lib/components/ui/label/index.js"
import { Separator } from "$lib/components/ui/separator/index.js"
import { Switch } from "$lib/components/ui/switch/index.js"
import { autoUpdate, setAutoUpdate } from "$lib/stores/settings.js"
import {
  getAppVersion,
  getReleaseChannel,
  setReleaseChannel as setReleaseChannelIpc,
  checkForUpdates as checkForUpdatesIpc,
  type ReleaseChannel,
} from "$lib/ipc/commands.js"
import { updateStatus as getUpdateStatusFn } from "$lib/stores/updates.svelte.js"
import { markUserInitiated, openUpdateDialog } from "./update-toasts.js"
import { toasts } from "$lib/stores/toasts.js"
import { extractErrorMessage } from "$lib/utils/error.js"

let appVersion = $state<string | null>(null)
let releaseChannel = $state<ReleaseChannel>("stable")

const liveStatus = $derived(getUpdateStatusFn())

async function loadVersion(): Promise<void> {
  try {
    appVersion = await getAppVersion()
  } catch {
    appVersion = null
  }
}

async function handleCheckForUpdates(): Promise<void> {
  try {
    markUserInitiated()
    openUpdateDialog()
    await checkForUpdatesIpc()
  } catch (err) {
    toasts.error(`Update check failed: ${extractErrorMessage(err)}`)
  }
}

async function handleChannelChange(channel: ReleaseChannel): Promise<void> {
  const previous = releaseChannel
  releaseChannel = channel
  try {
    await setReleaseChannelIpc(channel)
    toasts.success(`Switched to ${channel} update channel`)
  } catch (err) {
    releaseChannel = previous
    toasts.error(`Failed to switch channel: ${extractErrorMessage(err)}`)
  }
}

onMount(async () => {
  await loadVersion()
  try {
    releaseChannel = await getReleaseChannel()
  } catch {
    // Ignore — defaults to stable
  }
})
</script>

<h2 class="text-lg font-semibold">Updates</h2>

<div class="space-y-4">
  <div class="flex items-center justify-between">
    <div>
      <Label>Automatic Updates</Label>
      <p class="text-xs text-muted-foreground">Download and install updates in the background</p>
    </div>
    <Switch checked={$autoUpdate} onCheckedChange={(v) => setAutoUpdate(v)} />
  </div>

  <div class="space-y-3">
    <Label>Release Channel</Label>
    <div class="grid grid-cols-2 gap-3">
      <button
        class="rounded-lg border p-3 text-left transition-colors {releaseChannel === 'stable' ? 'border-primary bg-primary/5 ring-1 ring-primary' : 'border-border hover:border-muted-foreground/50'}"
        onclick={() => handleChannelChange("stable")}
      >
        <div class="flex items-center gap-2">
          <span class="font-medium text-sm">Stable</span>
          {#if releaseChannel === "stable"}
            <Check class="h-3.5 w-3.5 text-primary" />
          {/if}
        </div>
        <p class="mt-1 text-xs text-muted-foreground">Production-ready releases</p>
      </button>
      <button
        class="rounded-lg border p-3 text-left transition-colors {releaseChannel === 'beta' ? 'border-primary bg-primary/5 ring-1 ring-primary' : 'border-border hover:border-muted-foreground/50'}"
        onclick={() => handleChannelChange("beta")}
      >
        <div class="flex items-center gap-2">
          <span class="font-medium text-sm">Beta</span>
          {#if releaseChannel === "beta"}
            <Check class="h-3.5 w-3.5 text-primary" />
          {/if}
        </div>
        <p class="mt-1 text-xs text-muted-foreground">Early access to new features</p>
      </button>
    </div>
  </div>
</div>

<Separator />

<div class="space-y-3">
  <h2 class="text-lg font-semibold">Version</h2>
  <div class="flex items-center justify-between rounded-lg border p-3">
    <div>
      <p class="text-sm font-medium">Devsy</p>
      {#if appVersion}
        <p class="text-xs font-mono text-muted-foreground">v{appVersion}</p>
      {:else}
        <p class="text-xs text-muted-foreground">Version unavailable</p>
      {/if}
    </div>
    <Button
      variant="outline"
      size="sm"
      onclick={handleCheckForUpdates}
      disabled={liveStatus.state === "checking"}
    >
      {liveStatus.state === "checking" ? "Checking..." : "Check for Updates"}
    </Button>
  </div>

  {#if liveStatus.state !== "idle"}
    <div class="rounded-md border p-3 flex items-center justify-between">
      <p class="text-sm">
        {#if liveStatus.state === "checking"}
          Checking for updates…
        {:else if liveStatus.state === "available"}
          Update v{liveStatus.version} available
        {:else if liveStatus.state === "downloading"}
          Downloading v{liveStatus.version} · {(liveStatus.progress?.percent ?? 0).toFixed(0)}%
        {:else if liveStatus.state === "downloaded"}
          Update v{liveStatus.version} ready to install
        {:else if liveStatus.state === "not-available"}
          {liveStatus.code === "dev-mode" ? "Updates available in packaged builds" : "You're on the latest version"}
        {:else if liveStatus.state === "error"}
          Update error: {liveStatus.error}
        {/if}
      </p>
      <Button variant="outline" size="sm" onclick={openUpdateDialog}>
        View
      </Button>
    </div>
  {/if}
</div>
