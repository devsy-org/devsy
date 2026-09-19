<script lang="ts">
import { goto } from "$lib/router.js"
import { onMount, onDestroy } from "svelte"
import { Ellipsis, Trash2 } from "@lucide/svelte"
import { Button } from "$lib/components/ui/button/index.js"
import { Separator } from "$lib/components/ui/separator/index.js"
import { badgeVariants } from "$lib/components/ui/badge/index.js"
import * as DropdownMenu from "$lib/components/ui/dropdown-menu/index.js"
import * as Tabs from "$lib/components/ui/tabs/index.js"
import { ScrollArea } from "$lib/components/ui/scroll-area/index.js"
import ConfirmDialog from "$lib/components/layout/ConfirmDialog.svelte"
import { machines } from "$lib/stores/machines.js"
import {
  machineStart,
  machineStop,
  machineDelete,
  machineStatus,
	 machineDiagnosticsGet,
	 machineDiagnosticsRefresh,
  auditByResource,
} from "$lib/ipc/commands.js"
import { toasts } from "$lib/stores/toasts.js"
import { Skeleton } from "$lib/components/ui/skeleton/index.js"
import { extractErrorMessage } from "$lib/utils/error.js"
import type { AuditEntry } from "$lib/types/index.js"
import { formatTimestamp } from "$lib/utils/time.js"
import type { MachineDiagnosticsCache } from "$shared/machine-diagnostics-types.js"

let { params = {} }: { params?: Record<string, string> } = $props()

let id = $derived(params.id ?? "")
let machine = $derived($machines.find((m) => m.id === id))

let status = $state<string | null>(null)
let polling = $state(false)
let pollTimer: ReturnType<typeof setInterval> | null = null
let diagnosticsTimer: ReturnType<typeof setInterval> | null = null
let diagnostics = $state<MachineDiagnosticsCache | null>(null)
let diagnosticsRefreshing = $state(false)
const diagnosticLabels: Record<string, string> = {
  available: "Available", machine_stopped: "Machine stopped", not_initialized: "Not initialized",
  permission_denied: "Access denied", unavailable: "Unavailable", unsupported: "Unsupported agent",
  corrupt: "Unreadable diagnostic data", fresh: "Recently updated", stale: "Out of date", unknown: "Freshness unknown",
  healthy: "Healthy", degraded: "Needs attention", active: "Active", idle_due: "Idle; eligible",
  busy: "Busy", not_configured: "Auto-stop disabled", invalid_config: "Invalid configuration", not_running: "State unavailable",
}
function diagnosticLabel(value: string): string { return diagnosticLabels[value] ?? value }
let diagnosticsError = $state<string | null>(null)
let disposed = false

let isRunning = $derived(
  (status ?? machine?.status)?.toLowerCase() === "running",
)
let isStopped = $derived.by(() => {
  const s = (status ?? machine?.status ?? "").toLowerCase()
  return !s || s === "stopped" || s === "notfound"
})

function statusBadgeVariant(): "default" | "secondary" | "outline" {
  if (isRunning) return "default"
  return "outline"
}

let auditEntries = $state<AuditEntry[]>([])
let auditLoading = $state(false)
let confirmDeleteOpen = $state(false)
let confirmForceDeleteOpen = $state(false)
let deleting = $state(false)

onMount(async () => {
  await refreshStatus()
  if (disposed) return
  try {
    diagnostics = await machineDiagnosticsGet(id)
  } catch (error) {
    diagnosticsError = error instanceof Error ? error.message : "Could not load cached diagnostics."
  }
	 await refreshDiagnostics()
  loadAudit()
  if (disposed) return

  // Poll status every 5 seconds
  pollTimer = setInterval(refreshStatus, 5000)
	 diagnosticsTimer = setInterval(refreshDiagnostics, 30000)
  document.addEventListener("visibilitychange", refreshDiagnostics)
})

onDestroy(() => {
  disposed = true
  document.removeEventListener("visibilitychange", refreshDiagnostics)
  if (pollTimer) clearInterval(pollTimer)
	 if (diagnosticsTimer) clearInterval(diagnosticsTimer)
})

async function refreshStatus() {
  try {
    status = await machineStatus(id)
  } catch {
    // Status fetch failed
  }
}

async function refreshDiagnostics() {
  if (disposed || diagnosticsRefreshing || !isRunning || document.hidden) return
  diagnosticsRefreshing = true
  try {
    diagnostics = await machineDiagnosticsRefresh(id)
    diagnosticsError = null
  } catch (error) {
    diagnosticsError = error instanceof Error ? error.message : "Could not collect remote diagnostics."
  } finally {
    diagnosticsRefreshing = false
  }
}

async function loadAudit() {
  auditLoading = true
  try {
    auditEntries = await auditByResource("machine", id)
  } catch {
    auditEntries = []
  } finally {
    auditLoading = false
  }
}

async function handleStart() {
  polling = true
  try {
    await machineStart(id)
    toasts.success(`Started ${id}`)
    await refreshStatus()
	 await refreshDiagnostics()
  } catch (err) {
    toasts.error(`Failed to start: ${extractErrorMessage(err)}`)
  } finally {
    polling = false
  }
}

async function handleStop() {
  polling = true
  try {
    await machineStop(id)
    toasts.success(`Stopped ${id}`)
    await refreshStatus()
	 diagnostics = await machineDiagnosticsGet(id)
  } catch (err) {
    toasts.error(`Failed to stop: ${extractErrorMessage(err)}`)
  } finally {
    polling = false
  }
}

async function handleDelete(force = false) {
  deleting = true
  try {
    await machineDelete(id, force)
    toasts.success(`Deleted ${id}`)
    confirmDeleteOpen = false
    confirmForceDeleteOpen = false
    goto("/machines")
  } catch (err) {
    toasts.error(`Failed to delete: ${extractErrorMessage(err)}`)
  } finally {
    deleting = false
  }
}
</script>

<div class="space-y-6">
  <div class="flex items-center gap-4">
    <Button variant="ghost" size="sm" onclick={() => goto("/machines")}>
      &larr; Back
    </Button>
    <h1 class="text-2xl font-bold">{id}</h1>
    {#if status}
      <span class={badgeVariants({ variant: statusBadgeVariant() })}>{status}</span>
    {/if}
    {#if polling}
      <span class="text-xs text-muted-foreground animate-pulse">updating...</span>
    {/if}
  </div>

  {#if machine}
    <div class="flex gap-2">
      {#if isStopped}
        <Button size="sm" onclick={handleStart} disabled={polling}>
          {polling ? "Starting..." : "Start"}
        </Button>
      {/if}
      {#if isRunning}
        <Button variant="outline" size="sm" onclick={handleStop} disabled={polling}>
          {polling ? "Stopping..." : "Stop"}
        </Button>
      {/if}
      <Button variant="destructive" size="sm" onclick={() => (confirmDeleteOpen = true)} disabled={polling}>Delete</Button>

      <DropdownMenu.Root>
        <DropdownMenu.Trigger>
          {#snippet child({ props })}
            <Button {...props} variant="outline" size="icon" class="h-8 w-8">
              <Ellipsis class="h-4 w-4" />
              <span class="sr-only">More actions</span>
            </Button>
          {/snippet}
        </DropdownMenu.Trigger>
        <DropdownMenu.Content align="end">
          <DropdownMenu.Item
            class="text-destructive data-[highlighted]:text-destructive"
            onclick={() => (confirmForceDeleteOpen = true)}
            disabled={polling}
          >
            <Trash2 class="mr-2 h-4 w-4" />
            Force Delete
          </DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Root>
    </div>
  {/if}

  <Separator />

  {#if !machine}
    <p class="text-muted-foreground">Machine not found.</p>
  {:else}
    <Tabs.Root value="details">
      <Tabs.List variant="line">
        <Tabs.Trigger value="details">Details</Tabs.Trigger>
		<Tabs.Trigger value="diagnostics">Diagnostics</Tabs.Trigger>
        <Tabs.Trigger value="activity">Activity</Tabs.Trigger>
      </Tabs.List>

      <Tabs.Content value="details">
        <div class="mt-4 grid grid-cols-2 gap-4 text-sm">
          <div class="text-muted-foreground">ID</div>
          <div>{machine.id}</div>

          <div class="text-muted-foreground">Provider</div>
          <div>{machine.provider?.name ?? "N/A"}</div>

          <div class="text-muted-foreground">Status</div>
          <div>{status ?? machine.status ?? "Unknown"}</div>

          <div class="text-muted-foreground">Created</div>
          <div>{machine.creationTimestamp ? formatTimestamp(machine.creationTimestamp) : "N/A"}</div>

          <div class="text-muted-foreground">Last Used</div>
          <div>{machine.lastUsed ? formatTimestamp(machine.lastUsed) : "N/A"}</div>
        </div>
      </Tabs.Content>

		<Tabs.Content value="diagnostics">
          <div class="mt-4 flex items-center justify-between gap-4">
            <p class="text-sm text-muted-foreground">Updates every 30 seconds while this page is visible and the machine is running.</p>
            <Button variant="outline" size="sm" onclick={refreshDiagnostics} disabled={diagnosticsRefreshing || !isRunning}>{diagnosticsRefreshing ? "Refreshing…" : "Refresh diagnostics"}</Button>
          </div>
          {#if diagnosticsError}<p role="alert">{diagnosticsError}</p>{/if}
		  <div class="mt-4 space-y-4 text-sm">
			{#if !diagnostics}
			  <p class="text-muted-foreground">Remote diagnostics are not available yet.</p>
			{:else}
			  <div class="grid grid-cols-2 gap-4">
				<div class="text-muted-foreground">Devsy daemon</div>
				<div>{diagnosticLabel(diagnostics.response.daemon?.health ?? "unavailable")}</div>
				<div class="text-muted-foreground">Diagnostics</div>
				<div>{diagnosticLabel(diagnostics.response.source.availability)} / {diagnosticLabel(diagnostics.response.source.freshness)}</div>
				<div class="text-muted-foreground">Last successful collection</div>
				<div>{diagnostics.lastSuccessfulCollectionAt ? formatTimestamp(diagnostics.lastSuccessfulCollectionAt) : "No successful collection yet"}</div>
                <div class="text-muted-foreground">Last collection attempt</div>
                <div>{formatTimestamp(diagnostics.lastAttemptAt)}</div>
			  </div>
			  {#if diagnostics.historyGap}
				<p class="text-muted-foreground">Some older remote diagnostic events were rotated before Desktop collected them.</p>
			  {/if}
              {#if diagnostics.response.cursor.state === "reset"}
                <p class="text-muted-foreground">The daemon session changed or the event cursor expired. Collection will resume from retained events; earlier collected history remains below.</p>
              {/if}
			  {#if diagnostics.lastCollectionError}
				<p class="text-muted-foreground">
				  The last refresh failed; showing the most recently collected diagnostics.
				  {diagnostics.lastCollectionError}
				</p>
			  {/if}
			  {#if !isRunning}
				<p class="text-muted-foreground">This is a historic diagnostic snapshot because the machine is not running.</p>
			  {/if}
			  {#if diagnostics.response.source.message}
				<p class="text-muted-foreground">Diagnostic detail: {diagnostics.response.source.message}</p>
			  {/if}
			  {#if diagnostics.response.daemon?.lastError}
				<p class="text-muted-foreground">Last recorded daemon error ({formatTimestamp(diagnostics.response.daemon.lastError.timestamp)}): {diagnostics.response.daemon.lastError.message} · {diagnostics.response.daemon.lastError.code}</p>
			  {/if}
			  {#if diagnostics.response.daemon?.shutdownCandidate}
				<p class="text-muted-foreground">Shutdown candidate: {diagnostics.response.daemon.shutdownCandidate.workspaceId} {diagnostics.response.daemon.shutdownCandidate.eligibleAt ? `(eligible ${formatTimestamp(diagnostics.response.daemon.shutdownCandidate.eligibleAt)})` : "(eligible now)"}</p>
			  {/if}
			  {#if diagnostics.response.daemon?.workspaces?.length}
				<div class="rounded-md border overflow-x-auto">
                  <table class="w-full text-left text-sm">
                    <thead class="border-b text-muted-foreground"><tr><th class="p-3">Workspace</th><th class="p-3">State</th><th class="p-3">Last activity</th><th class="p-3">Machine shutdown status</th></tr></thead>
                    <tbody>
                      {#each diagnostics.response.daemon.workspaces as workspace}
                        <tr class="border-b last:border-0">
                          <td class="p-3">{workspace.id}</td>
                          <td class="p-3">{diagnosticLabel(workspace.state)}</td>
                          <td class="p-3">{workspace.lastActivityAt ? formatTimestamp(workspace.lastActivityAt) : "-"}</td>
                          <td class="p-3">
                            {workspace.blockerReason ?? (workspace.blocksMachineShutdown ? "Blocked" : "Eligible when all workspaces are idle")}
                            {#if workspace.state === "active" && workspace.idleDeadlineAt}<div class="text-muted-foreground">Idle after {formatTimestamp(workspace.idleDeadlineAt)}</div>{/if}
                          </td>
                        </tr>
                      {/each}
                    </tbody>
                  </table>
				</div>
			  {/if}
			  {#if diagnostics.events.length === 0}
				<p class="text-muted-foreground">No diagnostic events collected.</p>
			  {:else}
                <ScrollArea class="h-80 rounded-md border">
                  <div class="divide-y">
                    {#each diagnostics.events as event (`${event.sessionId}:${event.sequence}`)}
                      <div class="px-4 py-3">
                        <span class="font-medium" class:text-destructive={event.level === "error"}>{event.level.toUpperCase()} · {event.type}</span>
                        {#if event.workspaceId}<span class="text-muted-foreground"> {event.workspaceId}</span>{/if}
                        <div class="text-muted-foreground">{event.message}</div>
                        {#if event.errorCode}<div class="text-xs">Error code: {event.errorCode}</div>{/if}
                        <div class="text-xs text-muted-foreground">{formatTimestamp(event.timestamp)}</div>
                      </div>
                    {/each}
                  </div>
                </ScrollArea>
			  {/if}
			{/if}
		  </div>
		</Tabs.Content>

      <Tabs.Content value="activity">
        <div class="mt-4 space-y-4">
          {#if auditLoading}
            <div class="divide-y rounded-md border">
              {#each { length: 5 } as _}
                <div class="flex items-center gap-3 px-4 py-3">
                  <Skeleton class="h-5 w-16 rounded-full" />
                  <Skeleton class="h-4 w-48 flex-1" />
                  <Skeleton class="h-3 w-24 shrink-0" />
                </div>
              {/each}
            </div>
          {:else if auditEntries.length === 0}
            <p class="text-sm text-muted-foreground">
              No activity recorded for this machine.
            </p>
          {:else}
            <ScrollArea class="h-80 rounded-md border">
              <div class="divide-y">
                {#each auditEntries as entry}
                  <div class="flex items-center gap-3 px-4 py-3">
                    <span
                      class={badgeVariants({
                        variant: entry.success ? "default" : "destructive",
                      })}
                    >
                      {entry.action}
                    </span>
                    <div class="min-w-0 flex-1">
                      {#if entry.details}
                        <span class="text-sm text-muted-foreground">{entry.details}</span>
                      {/if}
                    </div>
                    <span class="shrink-0 text-xs text-muted-foreground">
                      {formatTimestamp(entry.timestamp)}
                    </span>
                  </div>
                {/each}
              </div>
            </ScrollArea>
          {/if}
        </div>
      </Tabs.Content>
    </Tabs.Root>
  {/if}
</div>

<ConfirmDialog
  bind:open={confirmDeleteOpen}
  title="Delete machine"
  description="This will permanently delete machine '{id}'. This action cannot be undone."
  confirmLabel="Delete"
  loading={deleting}
  onconfirm={() => handleDelete(false)}
/>

<ConfirmDialog
  bind:open={confirmForceDeleteOpen}
  title="Force delete machine"
  description="This will forcefully delete machine '{id}', skipping graceful shutdown. Use this only if the machine is stuck. This action cannot be undone."
  confirmLabel="Force Delete"
  loading={deleting}
  onconfirm={() => handleDelete(true)}
/>
