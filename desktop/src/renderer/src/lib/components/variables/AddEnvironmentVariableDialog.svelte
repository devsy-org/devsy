<script lang="ts">
import { Button } from "$lib/components/ui/button/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import { Label } from "$lib/components/ui/label/index.js"
import { Switch } from "$lib/components/ui/switch/index.js"
import * as Dialog from "$lib/components/ui/dialog/index.js"
import { activeContext } from "$lib/stores/contexts.js"
import { envVars, refreshEnv } from "$lib/stores/env.js"
import { envList, envSet, envAttach } from "$lib/ipc/commands.js"
import { toasts } from "$lib/stores/toasts.js"

let { open = $bindable(false) }: { open?: boolean } = $props()
let name = $state("")
let value = $state("")
let inject = $state(false)
let saving = $state(false)
let error = $state("")
let saved = $state(false)
let context = $state("")
let wasOpen = false
let initiatingControl: HTMLElement | null = null
const valid = $derived(/^[A-Za-z_][A-Za-z0-9_]*$/.test(name.trim()))
const duplicate = $derived(
  $envVars.some((row) => row.name === name.trim() && row.context === context),
)
$effect(() => {
  if (!open) {
    name = ""
    value = ""
    inject = false
    error = ""
    saved = false
  } else if (!wasOpen) {
    context = $activeContext
    initiatingControl =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null
  }
  wasOpen = open
})
async function save() {
  if (!valid || duplicate || saving || saved) return
  const target = name.trim()
  const targetContext = context
  const targetValue = value
  const targetInject = inject
  saving = true
  error = ""
  try {
    const currentVariables = await envList()
    if (
      currentVariables.some(
        (row) => row.name === target && row.context === targetContext,
      )
    ) {
      error =
        "A variable with this name already exists in this context. Edit the existing row."
      await refreshEnv()
      saving = false
      return
    }
  } catch {
    error =
      "Unable to check existing variables. Check your connection and try again."
    saving = false
    return
  }
  try {
    await envSet(target, targetValue, targetContext)
    saved = true
    value = ""
    if (targetInject) {
      try {
        await envAttach(target, targetContext)
      } catch {
        error = `Variable "${target}" was saved, but workspace injection could not be enabled. Enable it from the table.`
        await refreshEnv()
        return
      }
    }
    toasts.success(`Environment variable "${target}" saved`)
    open = false
    await refreshEnv()
  } catch {
    error = "Unable to save variable. Check your connection and try again."
  } finally {
    saving = false
  }
}
</script>
<Dialog.Root bind:open>
  <Dialog.Content class="sm:max-w-md" showCloseButton={!saving} onEscapeKeydown={(event) => { if (saving) event.preventDefault() }} onInteractOutside={(event) => { if (saving) event.preventDefault() }} onCloseAutoFocus={(event) => { if (initiatingControl?.isConnected) { event.preventDefault(); initiatingControl.focus() } }}>
    <Dialog.Header>
      <Dialog.Title>Add environment variable</Dialog.Title>
      <Dialog.Description>Store non-sensitive plaintext configuration in your current context. Use Secrets for credentials.</Dialog.Description>
    </Dialog.Header>
    <form class="space-y-4" onsubmit={(event) => { event.preventDefault(); save() }}>
      <div class="space-y-1.5">
        <Label for="add-env-name">Name</Label>
        <Input id="add-env-name" bind:value={name} disabled={saving || saved} aria-describedby="add-env-name-error" />
        <p id="add-env-name-error" class="text-sm text-destructive" role={duplicate ? "alert" : undefined}>
          {#if duplicate && !saved}A variable with this name already exists in this context. Edit the existing row.{:else if name && !valid}Use letters, digits, and underscores, starting with a letter or underscore.{/if}
        </p>
      </div>
      <div class="space-y-1.5"><Label for="add-env-value">Value</Label><Input id="add-env-value" bind:value disabled={saving || saved} /><p class="text-xs text-muted-foreground">An empty value is allowed.</p></div>
      <div class="flex items-center justify-between"><Label for="add-env-inject">Inject into workspaces</Label><Switch id="add-env-inject" bind:checked={inject} disabled={saving || saved} /></div>
      {#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
      <Dialog.Footer><Button type="button" variant="outline" disabled={saving} onclick={() => (open = false)}>{saved ? "Done" : "Cancel"}</Button>{#if !saved}<Button type="submit" disabled={saving || !valid || duplicate}>{saving ? "Saving…" : "Save"}</Button>{/if}</Dialog.Footer>
    </form>
  </Dialog.Content>
</Dialog.Root>
