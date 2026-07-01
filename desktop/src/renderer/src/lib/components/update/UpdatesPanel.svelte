<script lang="ts">
import { onMount } from "svelte"
import DOMPurify from "dompurify"
import {
  CheckCircle2,
  Download,
  RefreshCw,
  AlertTriangle,
  Loader2,
} from "@lucide/svelte"
import { Button } from "$lib/components/ui/button/index.js"
import { Label } from "$lib/components/ui/label/index.js"
import { Separator } from "$lib/components/ui/separator/index.js"
import { Switch } from "$lib/components/ui/switch/index.js"
import { Progress } from "$lib/components/ui/progress/index.js"
import ConfirmDialog from "$lib/components/layout/ConfirmDialog.svelte"
import { autoUpdate, setAutoUpdate } from "$lib/stores/settings.js"
import {
  getAppVersion,
  getReleaseChannel,
  setReleaseChannel as setReleaseChannelIpc,
  checkForUpdates as checkForUpdatesIpc,
  downloadUpdate,
  installUpdate,
  type ReleaseChannel,
} from "$lib/ipc/commands.js"
import { updateStatus, lastCheckedAt, isChecking } from "$lib/stores/updates.svelte.js"
import { markUserInitiated } from "./update-toasts.js"
import { toasts } from "$lib/stores/toasts.js"
import { extractErrorMessage } from "$lib/utils/error.js"
import { CHANNELS, channelLabel, isDowngrade } from "./channel.js"
import { fmtMBps, fmtTime, statusHeadline } from "./status-copy.js"

let appVersion = $state<string | null>(null)
let releaseChannel = $state<ReleaseChannel>("stable")

let confirmOpen = $state(false)
let pendingChannel = $state<ReleaseChannel | null>(null)

const s = $derived(updateStatus())
const lastChecked = $derived(lastCheckedAt())
const sanitizedNotes = $derived(s.releaseNotes ? DOMPurify.sanitize(s.releaseNotes) : "")
const headline = $derived(statusHeadline(s, appVersion))

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
    await checkForUpdatesIpc()
  } catch (err) {
    toasts.error(`Update check failed: ${extractErrorMessage(err)}`)
  }
}

function requestChannel(channel: ReleaseChannel): void {
  if (channel === releaseChannel) return
  if (isDowngrade(releaseChannel, channel)) {
    pendingChannel = channel
    confirmOpen = true
    return
  }
  applyChannel(channel)
}

async function applyChannel(channel: ReleaseChannel): Promise<void> {
  const previous = releaseChannel
  releaseChannel = channel
  confirmOpen = false
  pendingChannel = null
  try {
    markUserInitiated()
    await setReleaseChannelIpc(channel)
    toasts.success(`Switched to the ${channelLabel(channel)} channel`)
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

<div class="space-y-8">
  <!-- Status hero -->
  <section class="space-y-3">
    <div class="rounded-lg border p-4">
      <div class="flex items-start gap-3">
        <div class="mt-0.5 shrink-0">
          {#if s.state === "checking"}
            <Loader2 class="h-5 w-5 animate-spin text-muted-foreground" />
          {:else if s.state === "available" || s.state === "downloading"}
            <Download class="h-5 w-5 text-primary {s.state === 'downloading' ? 'animate-pulse' : ''}" />
          {:else if s.state === "downloaded"}
            <CheckCircle2 class="h-5 w-5 text-primary" />
          {:else if s.state === "error"}
            <AlertTriangle class="h-5 w-5 text-destructive" />
          {:else}
            <CheckCircle2 class="h-5 w-5 text-muted-foreground" />
          {/if}
        </div>

        <div class="min-w-0 flex-1 space-y-2">
          <p class="text-sm font-medium">{headline}</p>

          {#if s.state === "error"}
            <p class="text-xs text-destructive">{s.error}</p>
          {:else if s.state === "downloading"}
            <Progress value={s.progress?.percent ?? 0} max={100} />
            <p class="text-xs text-muted-foreground">
              {(s.progress?.percent ?? 0).toFixed(0)}% · {fmtMBps(s.progress?.bytesPerSecond)}
            </p>
          {:else if lastChecked && (s.state === "not-available" || s.state === "idle")}
            <p class="text-xs text-muted-foreground">Last checked at {fmtTime(lastChecked)}</p>
          {/if}

          {#if (s.state === "available" || s.state === "downloaded") && sanitizedNotes}
            <div class="prose prose-sm dark:prose-invert max-h-40 overflow-y-auto rounded-md border p-3">
              {@html sanitizedNotes}
            </div>
          {/if}
        </div>

        <div class="shrink-0">
          {#if s.state === "available"}
            <Button size="sm" onclick={() => downloadUpdate()}>Download</Button>
          {:else if s.state === "downloaded"}
            <Button size="sm" onclick={() => installUpdate()}>Restart &amp; Update</Button>
          {:else}
            <Button
              variant="outline"
              size="sm"
              onclick={handleCheckForUpdates}
              disabled={isChecking()}
              class="gap-2"
            >
              <RefreshCw class="h-3.5 w-3.5 {isChecking() ? 'animate-spin' : ''}" />
              {isChecking() ? "Checking…" : "Check now"}
            </Button>
          {/if}
        </div>
      </div>
    </div>
  </section>

  <Separator />

  <!-- Release channel -->
  <section class="space-y-3">
    <div>
      <Label>Release Channel</Label>
      <p class="text-xs text-muted-foreground">Choose how early you receive new versions</p>
    </div>
    <div class="grid grid-cols-2 gap-3">
      {#each CHANNELS as c (c.value)}
        <button
          class="rounded-lg border p-3 text-left transition-colors {releaseChannel === c.value
            ? 'border-primary bg-primary/5 ring-1 ring-primary'
            : 'border-border hover:border-muted-foreground/50'}"
          onclick={() => requestChannel(c.value)}
        >
          <div class="flex items-center gap-2">
            <span class="text-sm font-medium">{c.label}</span>
            {#if releaseChannel === c.value}
              <CheckCircle2 class="h-3.5 w-3.5 text-primary" />
            {/if}
          </div>
          <p class="mt-1 text-xs text-muted-foreground">{c.cadence}</p>
          {#if c.unstable}
            <p class="mt-1 text-xs text-yellow-600 dark:text-yellow-400">{c.description}</p>
          {:else}
            <p class="mt-1 text-xs text-muted-foreground">{c.description}</p>
          {/if}
        </button>
      {/each}
    </div>
  </section>

  <Separator />

  <!-- Update behavior -->
  <section class="space-y-3">
    <Label>Update Behavior</Label>
    <div class="flex items-center justify-between">
      <div>
        <p class="text-sm">Download updates automatically</p>
        <p class="text-xs text-muted-foreground">
          Updates download in the background; you choose when to restart.
        </p>
      </div>
      <Switch checked={$autoUpdate} onCheckedChange={(v) => setAutoUpdate(v)} />
    </div>
  </section>

  <Separator />

  <!-- Version footer -->
  <section class="flex items-center justify-between rounded-lg border p-3">
    <div>
      <p class="text-sm font-medium">Devsy</p>
      {#if appVersion}
        <p class="font-mono text-xs text-muted-foreground">v{appVersion}</p>
      {:else}
        <p class="text-xs text-muted-foreground">Version unavailable</p>
      {/if}
    </div>
    <span class="text-xs text-muted-foreground">{channelLabel(releaseChannel)} channel</span>
  </section>
</div>

<ConfirmDialog
  bind:open={confirmOpen}
  title="Switch to the Stable channel?"
  description="You may currently be on a newer Preview build than the latest Stable release. You'll stay on your current version until Stable catches up — no downgrade happens automatically."
  confirmLabel="Switch to Stable"
  cancelLabel="Stay on Preview"
  variant="default"
  onconfirm={() => pendingChannel && applyChannel(pendingChannel)}
/>
