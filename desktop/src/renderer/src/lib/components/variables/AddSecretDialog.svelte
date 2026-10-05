<script lang="ts">
import { untrack } from "svelte"
import * as Dialog from "$lib/components/ui/dialog/index.js"
import { Button } from "$lib/components/ui/button/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import { Label } from "$lib/components/ui/label/index.js"
import { Switch } from "$lib/components/ui/switch/index.js"
import { secrets, secretsError, refreshSecrets } from "$lib/stores/secrets.js"
import { activeContext } from "$lib/stores/contexts.js"
import { secretSet, secretAttach } from "$lib/ipc/commands.js"
import { toasts } from "$lib/stores/toasts.js"

let { open = $bindable(false) }: { open?: boolean } = $props()
let name = $state("")
let value = $state("")
let inject = $state(false)
let busy = $state(false)
let error = $state("")
let context = $state("")
let returnFocus: HTMLElement | null = null
let nameValid = $derived(/^[A-Za-z_][A-Za-z0-9_]*$/.test(name.trim()))
let duplicate = $derived(
  $secrets.some((s) => s.context === context && s.name === name.trim()),
)
$effect(() => {
  if (!open) {
    name = ""
    value = ""
    inject = false
    error = ""
  } else {
    context = untrack(() => $activeContext)
    returnFocus =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null
  }
})
async function save() {
  if (busy || !nameValid || !value || duplicate) return
  if (!context) {
    error = "Select a context before adding a secret."
    return
  }
  const savedName = name.trim()
  const savedContext = context
  const attach = inject
  busy = true
  error = ""
  await refreshSecrets()
  if ($secretsError) {
    error = "Unable to check existing secrets. Retry before adding this value."
    busy = false
    return
  }
  if (
    $secrets.some((s) => s.context === savedContext && s.name === savedName)
  ) {
    error =
      "A secret with this name already exists. Use its row menu to replace the value."
    busy = false
    return
  }
  try {
    await secretSet(savedName, value, savedContext)
    value = ""
  } catch {
    value = ""
    error = "Unable to save the secret. Enter its value again and retry."
    busy = false
    return
  }
  let attached = true
  if (attach) {
    try {
      await secretAttach(savedName, savedContext)
    } catch {
      attached = false
    }
  }
  open = false
  busy = false
  if (attached) toasts.success(`Secret "${savedName}" saved`)
  else
    toasts.info("Secret saved, but Devsy could not enable workspace injection.")
  await refreshSecrets()
}
</script>

<Dialog.Root bind:open={() => open, (next) => { if (!busy) open = next }}>
  <Dialog.Content showCloseButton={!busy} onCloseAutoFocus={(e) => { e.preventDefault(); returnFocus?.focus() }} class="sm:max-w-md" onEscapeKeydown={(e) => { if (busy) e.preventDefault() }} onInteractOutside={(e) => { if (busy) e.preventDefault() }}>
    <Dialog.Header>
      <Dialog.Title>Add secret</Dialog.Title>
      <Dialog.Description>Store a sensitive value securely in the active context.</Dialog.Description>
    </Dialog.Header>
    <form class="space-y-4" onsubmit={(e) => { e.preventDefault(); save() }}>
      <div class="space-y-1.5">
        <Label for="add-secret-name">Name</Label>
        <Input id="add-secret-name" bind:value={name} disabled={busy} aria-describedby="add-secret-name-help" autocomplete="off" />
        <p id="add-secret-name-help" class="text-sm text-muted-foreground">
          {#if duplicate}A secret with this name already exists. Use its row menu to replace the value.
          {:else if name && !nameValid}Start with a letter or underscore; use letters, digits, and underscores.
          {:else}For example, DB_PASSWORD.{/if}
        </p>
      </div>
      <div class="space-y-1.5">
        <Label for="add-secret-value">Value</Label>
        <Input id="add-secret-value" type="password" bind:value autocomplete="off" spellcheck={false} autocapitalize="none" disabled={busy} aria-describedby={error ? "add-secret-error" : undefined} />
      </div>
      <div class="flex items-center justify-between gap-3">
        <Label for="add-secret-inject">Inject into workspaces</Label>
        <Switch id="add-secret-inject" bind:checked={inject} disabled={busy} aria-label="Inject new secret into workspaces" />
      </div>
      {#if error}<p id="add-secret-error" role="alert" class="text-sm text-destructive">{error}</p>{/if}
      <Dialog.Footer>
        <Button type="button" variant="outline" disabled={busy} onclick={() => { value = ""; open = false }}>Cancel</Button>
        <Button type="submit" disabled={busy || !nameValid || !value || duplicate}>{busy ? "Saving..." : "Save"}</Button>
      </Dialog.Footer>
    </form>
  </Dialog.Content>
</Dialog.Root>
