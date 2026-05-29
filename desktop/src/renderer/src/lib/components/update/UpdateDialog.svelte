<script lang="ts">
import DOMPurify from "dompurify"
import * as Dialog from "$lib/components/ui/dialog/index.js"
import { Button } from "$lib/components/ui/button/index.js"
import { Progress } from "$lib/components/ui/progress/index.js"
import {
  checkForUpdates,
  downloadUpdate,
  installUpdate,
} from "$lib/ipc/commands.js"
import {
  updateStatus,
  isChecking,
  lastCheckedAt,
} from "$lib/stores/updates.svelte.js"
import { markUserInitiated } from "./update-toasts.js"

let {
  open = $bindable(false),
  autoDownloadEnabled = true,
}: { open?: boolean; autoDownloadEnabled?: boolean } = $props()

const s = $derived(updateStatus())
const lastChecked = $derived(lastCheckedAt())
const sanitizedNotes = $derived(
  s.releaseNotes ? DOMPurify.sanitize(s.releaseNotes) : "",
)

function fmtMBps(bps: number): string {
  if (!bps) return ""
  const mbps = bps / 1_000_000
  return `${mbps.toFixed(2)} MB/s`
}

function fmtTime(ts: number | null): string {
  if (!ts) return ""
  return new Date(ts).toLocaleTimeString()
}

async function onCheck() {
  markUserInitiated()
  await checkForUpdates()
}

async function onDownload() {
  await downloadUpdate()
}

async function onInstall() {
  await installUpdate()
}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="max-w-lg">
		<Dialog.Header>
			<Dialog.Title>Application Updates</Dialog.Title>
		</Dialog.Header>

		{#if s.state === "checking"}
			<p class="text-sm text-muted-foreground">Checking for updates…</p>
		{:else if s.state === "available"}
			<div class="space-y-3">
				<p class="text-sm">
					<span class="font-medium">Version {s.version}</span> is available.
				</p>
				{#if s.releaseNotes}
					<div class="prose prose-sm dark:prose-invert max-h-48 overflow-y-auto rounded-md border p-3">
						{@html sanitizedNotes}
					</div>
				{/if}
				{#if autoDownloadEnabled}
					<p class="text-xs text-muted-foreground">Downloading in the background…</p>
				{:else}
					<Button onclick={onDownload}>Download</Button>
				{/if}
			</div>
		{:else if s.state === "downloading"}
			<div class="space-y-3">
				<p class="text-sm font-medium">Downloading v{s.version}…</p>
				<Progress value={s.progress?.percent ?? 0} max={100} />
				<p class="text-xs text-muted-foreground">
					{(s.progress?.percent ?? 0).toFixed(0)}% · {fmtMBps(s.progress?.bytesPerSecond ?? 0)}
				</p>
			</div>
		{:else if s.state === "downloaded"}
			<div class="space-y-3">
				<p class="text-sm">
					<span class="font-medium">Version {s.version}</span> is ready to install.
				</p>
				{#if s.releaseNotes}
					<div class="prose prose-sm dark:prose-invert max-h-48 overflow-y-auto rounded-md border p-3">
						{@html sanitizedNotes}
					</div>
				{/if}
				<div class="flex gap-2 justify-end">
					<Button variant="ghost" onclick={() => (open = false)}>Later</Button>
					<Button onclick={onInstall}>Restart and Update</Button>
				</div>
			</div>
		{:else if s.state === "not-available"}
			{#if s.code === "dev-mode"}
				<p class="text-sm text-muted-foreground">Updates are available in packaged builds.</p>
			{:else}
				<div class="space-y-2">
					<p class="text-sm text-muted-foreground">You're on the latest version.</p>
					{#if lastChecked}
						<p class="text-xs text-muted-foreground">Last checked at {fmtTime(lastChecked)}</p>
					{/if}
					<Button variant="outline" size="sm" onclick={onCheck} disabled={isChecking()}>
						Check Again
					</Button>
				</div>
			{/if}
		{:else if s.state === "error"}
			<div class="space-y-2">
				<p class="text-sm text-destructive">Update check failed: {s.error}</p>
				<Button variant="outline" size="sm" onclick={onCheck} disabled={isChecking()}>
					Check Again
				</Button>
			</div>
		{:else}
			<div class="space-y-2">
				<p class="text-sm text-muted-foreground">No update check has run yet.</p>
				<Button variant="outline" size="sm" onclick={onCheck} disabled={isChecking()}>
					Check for Updates
				</Button>
			</div>
		{/if}
	</Dialog.Content>
</Dialog.Root>
