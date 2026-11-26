<script lang="ts">
import * as Sheet from "$lib/components/ui/sheet/index.js"
import * as Dialog from "$lib/components/ui/dialog/index.js"
import { Button } from "$lib/components/ui/button/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import { Label } from "$lib/components/ui/label/index.js"
import { Switch } from "$lib/components/ui/switch/index.js"
import { secretSet } from "$lib/ipc/commands.js"
import { refreshSecrets } from "$lib/stores/secrets.js"
import { toasts } from "$lib/stores/toasts.js"
import type { Secret } from "$lib/types/index.js"
import { secretStorage, secretAvailability } from "./secret-presentation.js"

let {
  open = $bindable(false),
  replaceOpen = $bindable(false),
  secret,
  attachmentBusy = false,
  attachmentError = "",
  attachmentReset = 0,
  onAttachmentChange,
}: {
  open?: boolean
  replaceOpen?: boolean
  secret: Secret | null
  attachmentBusy?: boolean
  attachmentError?: string
  attachmentReset?: number
  onAttachmentChange: (secret: Secret, checked: boolean) => void
} = $props()
let value = $state("")
let busy = $state(false)
let error = $state("")
let returnFocus: HTMLElement | null = null
let replaceReturnFocus: HTMLElement | null = null
$effect(() => {
  if (!replaceOpen) {
    value = ""
    error = ""
  }
})
$effect(() => {
  if (!open) replaceOpen = false
})
async function replace() {
  if (!secret || !value || busy) return
  const target = { ...secret }
  busy = true
  error = ""
  try {
    await secretSet(target.name, value, target.context)
    value = ""
    replaceOpen = false
    toasts.success(`Secret "${target.name}" updated`)
    await refreshSecrets()
  } catch {
    value = ""
    error = "Unable to replace the secret. Enter its value again and retry."
  } finally {
    busy = false
  }
}
</script>

<Sheet.Root bind:open={() => open, (next) => { if (!busy) open = next }}>
  <Sheet.Content onOpenAutoFocus={() => { returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null }} onCloseAutoFocus={(e) => { e.preventDefault(); returnFocus?.focus() }} onEscapeKeydown={(e) => { if (busy) e.preventDefault() }} onInteractOutside={(e) => { if (busy) e.preventDefault() }}>
    <Sheet.Header>
      <Sheet.Title>Secret details</Sheet.Title>
      <Sheet.Description>Manage metadata and replace the value without revealing it.</Sheet.Description>
    </Sheet.Header>
    {#if secret}
      <div class="space-y-6 p-4">
        <dl class="space-y-3 text-sm">
          <div><dt class="text-muted-foreground">Name</dt><dd class="break-all font-medium">{secret.name}</dd></div>
          <div><dt class="text-muted-foreground">Context</dt><dd>{secret.context}</dd></div>
          <div><dt class="text-muted-foreground">Storage</dt><dd>{secretStorage(secret)}</dd></div>
          <div><dt class="text-muted-foreground">Availability</dt><dd>{secretAvailability(secret)}</dd></div>
        </dl>
        <div class="flex items-center justify-between gap-3">
          <Label for="secret-details-inject">Inject into workspaces</Label>
          {#key attachmentReset}
            <Switch id="secret-details-inject" checked={secret.attached ?? false} disabled={attachmentBusy} aria-label={`Inject ${secret.name} into workspaces from details`} onCheckedChange={(checked) => { if (secret) onAttachmentChange(secret, checked) }} />
          {/key}
        </div>
        {#if attachmentError}<p class="text-sm text-destructive" role="alert">{attachmentError}</p>{/if}
        <Button variant="outline" onclick={() => { replaceOpen = true }}>Replace secret value</Button>
      </div>
    {/if}
    <Dialog.Root bind:open={() => replaceOpen, (next) => { if (!busy) replaceOpen = next }}>
      <Dialog.Content showCloseButton={!busy} onOpenAutoFocus={() => { replaceReturnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null }} onCloseAutoFocus={(e) => { e.preventDefault(); replaceReturnFocus?.focus() }} class="sm:max-w-md" onEscapeKeydown={(e) => { if (busy) e.preventDefault() }} onInteractOutside={(e) => { if (busy) e.preventDefault() }}>
        <Dialog.Header>
          <Dialog.Title>Replace secret value</Dialog.Title>
          <Dialog.Description>Replace the value of {secret?.name} in {secret?.context}. Its name and attachment settings are preserved.</Dialog.Description>
        </Dialog.Header>
        <form class="space-y-4" onsubmit={(e) => { e.preventDefault(); replace() }}>
          <Label for="replace-secret-value">New value</Label>
          <Input id="replace-secret-value" type="password" bind:value autocomplete="off" spellcheck={false} autocapitalize="none" disabled={busy} aria-describedby={error ? "replace-secret-error" : undefined} />
          {#if error}<p id="replace-secret-error" class="text-sm text-destructive" role="alert">{error}</p>{/if}
          <Dialog.Footer>
            <Button type="button" variant="outline" disabled={busy} onclick={() => { value = ""; replaceOpen = false }}>Cancel</Button>
            <Button type="submit" disabled={busy || !value}>{busy ? "Saving..." : "Replace"}</Button>
          </Dialog.Footer>
        </form>
      </Dialog.Content>
    </Dialog.Root>
  </Sheet.Content>
</Sheet.Root>
