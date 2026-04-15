<script lang="ts">
import { onMount } from "svelte"
import { Button } from "$lib/components/ui/button/index.js"
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import { Separator } from "$lib/components/ui/separator/index.js"
import { ScrollArea } from "$lib/components/ui/scroll-area/index.js"
import { theme, applyTheme } from "$lib/stores/settings.js"
import type { Theme } from "$lib/stores/settings.js"
import { auditRecent } from "$lib/ipc/commands.js"
import type { AuditEntry } from "$lib/types/index.js"

const THEMES: { value: Theme; label: string }[] = [
  { value: "light", label: "Light" },
  { value: "dark", label: "Dark" },
  { value: "system", label: "System" },
]

function setTheme(value: Theme) {
  theme.set(value)
  applyTheme(value)
}

let activity = $state<AuditEntry[]>([])
let activityLoading = $state(false)

onMount(() => {
  loadActivity()
})

async function loadActivity() {
  activityLoading = true
  try {
    activity = await auditRecent(25)
  } catch {
    activity = []
  } finally {
    activityLoading = false
  }
}

function formatTimestamp(ts: string): string {
  try {
    const d = new Date(ts)
    return d.toLocaleString()
  } catch {
    return ts
  }
}
</script>

<div class="mx-auto max-w-xl space-y-6">
	<h1 class="text-2xl font-bold">Settings</h1>

	<div class="space-y-4">
		<h2 class="text-lg font-semibold">Theme</h2>
		<div class="flex gap-2">
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

	<div class="space-y-4">
		<div class="flex items-center justify-between">
			<h2 class="text-lg font-semibold">Activity</h2>
			<Button variant="ghost" size="sm" onclick={loadActivity}>Refresh</Button>
		</div>

		{#if activityLoading}
			<p class="text-sm text-muted-foreground">Loading activity...</p>
		{:else if activity.length === 0}
			<p class="text-sm text-muted-foreground">No activity recorded yet.</p>
		{:else}
			<ScrollArea class="h-80 rounded-md border">
				<div class="divide-y">
					{#each activity as entry}
						<div class="flex items-center gap-3 px-4 py-3">
							<span
								class={badgeVariants({
									variant: entry.success ? "default" : "destructive",
								})}
							>
								{entry.action}
							</span>
							<div class="min-w-0 flex-1">
								<div class="truncate text-sm">
									{entry.resourceType}
									{#if entry.resourceId}
										<span class="font-medium">{entry.resourceId}</span>
									{/if}
								</div>
								{#if entry.details}
									<div class="truncate text-xs text-muted-foreground">
										{entry.details}
									</div>
								{/if}
							</div>
							<div class="shrink-0 text-xs text-muted-foreground">
								{formatTimestamp(entry.timestamp)}
							</div>
						</div>
					{/each}
				</div>
			</ScrollArea>
		{/if}
	</div>

	<Separator />

	<div class="space-y-4">
		<h2 class="text-lg font-semibold">About</h2>
		<div class="space-y-1 text-sm text-muted-foreground">
			<p>DevPod Desktop</p>
			<p>Built with Tauri v2 + SvelteKit + shadcn-svelte</p>
		</div>
	</div>
</div>
