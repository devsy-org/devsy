<script lang="ts">
import * as Sheet from "$lib/components/ui/sheet/index.js"
import { Button } from "$lib/components/ui/button/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import { Label } from "$lib/components/ui/label/index.js"
import { Switch } from "$lib/components/ui/switch/index.js"
import { envSet } from "$lib/ipc/commands.js"
import { refreshEnv } from "$lib/stores/env.js"
import { toasts } from "$lib/stores/toasts.js"
import { variableIdentity } from "$lib/variables/identity.js"
import type { EnvVar } from "$lib/types/index.js"
let {
  open = $bindable(false),
  variable,
  updating = false,
  reset = 0,
  attachmentError = "",
  onattachment,
  ondelete,
}: {
  open?: boolean
  variable: EnvVar | null
  updating?: boolean
  reset?: number
  attachmentError?: string
  onattachment: (row: EnvVar, attached: boolean) => void
  ondelete: (row: EnvVar) => void
} = $props()
let value = $state("")
let saving = $state(false)
let error = $state("")
let identity = $state("")
let wasOpen = false
let viewGeneration = 0
let initiatingControl: HTMLElement | null = null
$effect(() => {
  if (open !== wasOpen) {
    viewGeneration++
    if (open)
      initiatingControl =
        document.activeElement instanceof HTMLElement
          ? document.activeElement
          : null
    wasOpen = open
  }
  if (!open || !variable) {
    value = ""
    error = ""
    identity = ""
  } else {
    const next = variableIdentity(variable.context, variable.name)
    if (identity !== next) {
      identity = next
      value = variable.value
      error = ""
    }
  }
})
async function save() {
  if (!variable || saving) return
  const target = { ...variable }
  const submittedIdentity = variableIdentity(target.context, target.name)
  const generation = viewGeneration
  saving = true
  error = ""
  try {
    await envSet(target.name, value, target.context)
    toasts.success(`Environment variable "${target.name}" updated`)
    if (identity === submittedIdentity && generation === viewGeneration)
      open = false
    await refreshEnv()
  } catch {
    if (identity === submittedIdentity && generation === viewGeneration)
      error = "Unable to update variable. Check your connection and try again."
  } finally {
    saving = false
  }
}
</script>
<Sheet.Root bind:open>
  <Sheet.Content onCloseAutoFocus={(event) => { if (initiatingControl?.isConnected) { event.preventDefault(); initiatingControl.focus() } }}>
    <Sheet.Header><Sheet.Title>Environment variable details</Sheet.Title><Sheet.Description>Values are stored as non-sensitive plaintext configuration.</Sheet.Description></Sheet.Header>
    {#if variable}
      <div class="space-y-5 px-4">
        <dl class="space-y-2"><dt class="text-sm text-muted-foreground">Name</dt><dd class="font-mono">{variable.name}</dd><dt class="text-sm text-muted-foreground">Context</dt><dd>{variable.context}</dd></dl>
        <form class="space-y-4" onsubmit={(event) => { event.preventDefault(); save() }}>
          <Label for="edit-env-value">Value</Label><Input id="edit-env-value" bind:value disabled={saving} />
          {#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
          <Button type="submit" disabled={saving}>{saving ? "Saving…" : "Update value"}</Button>
        </form>
        <div class="flex items-center justify-between"><Label for="edit-env-inject">Inject into workspaces</Label>{#key reset}<Switch id="edit-env-inject" checked={variable.attached} disabled={updating || saving} onCheckedChange={(checked) => variable && onattachment(variable, checked)} />{/key}</div>
        {#if attachmentError}<p role="alert" class="text-sm text-destructive">{attachmentError}</p>{/if}
        <Button variant="destructive" disabled={saving} onclick={() => variable && ondelete(variable)}>Delete variable</Button>
      </div>
    {/if}
  </Sheet.Content>
</Sheet.Root>
