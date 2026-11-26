<script lang="ts">
import * as Dialog from "$lib/components/ui/dialog/index.js"
import { Button } from "$lib/components/ui/button/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import { secretProtectionAction } from "$lib/ipc/commands.js"
import type {
  SecretProtectionStatus,
  SecretProtectionAction,
} from "$lib/types/index.js"
import { refreshSecretProtection } from "$lib/stores/secret-protection.js"
import { refreshSecrets } from "$lib/stores/secrets.js"
import { toasts } from "$lib/stores/toasts.js"
import { mapSecretError } from "$lib/variables/secret-errors.js"

let {
  open = $bindable(false),
  action = "set-passphrase",
  status,
}: {
  open?: boolean
  action?: SecretProtectionAction
  status: SecretProtectionStatus | null
} = $props()
let currentPassphrase = $state("")
let newPassphrase = $state("")
let confirmation = $state("")
let error = $state("")
let errorField = $state<string | undefined>()
let busy = $state(false)
let requireCredential = $state(false)
const titles: Record<SecretProtectionAction, string> = {
  "set-passphrase": "Use a passphrase",
  "change-passphrase": "Change passphrase",
  "remove-passphrase": "Remove passphrase protection",
  remember: "Remember on this device",
  forget: "Forget this device",
}
let setsPassphrase = $derived(
  action === "set-passphrase" || action === "change-passphrase",
)
let needsCurrent = $derived(
  action !== "set-passphrase" &&
    action !== "forget" &&
    (requireCredential || status?.availability !== "available"),
)
function clearCredentials() {
  currentPassphrase = ""
  newPassphrase = ""
  confirmation = ""
}
$effect(() => {
  if (!open) {
    clearCredentials()
    error = ""
    errorField = undefined
    requireCredential = false
  }
})
async function submit() {
  if (busy) return
  error = ""
  errorField = undefined
  if (needsCurrent && !currentPassphrase) {
    error = "Enter your current passphrase to continue."
    errorField = "currentPassphrase"
    return
  }
  if (setsPassphrase && !newPassphrase) {
    error = "Enter a new passphrase."
    errorField = "newPassphrase"
    return
  }
  if (setsPassphrase && newPassphrase !== confirmation) {
    error = "Passphrases do not match."
    errorField = "newPassphrase"
    return
  }
  busy = true
  const input = {
    action,
    ...(setsPassphrase ? { newPassphrase } : {}),
    ...(needsCurrent ? { currentPassphrase } : {}),
  }
  // Only the IPC call holds the submitted values while confirmation is pending.
  clearCredentials()
  try {
    const result = await secretProtectionAction(input)
    if (!result.ok) {
      const presented = mapSecretError(result)
      if (!presented.cancelled) {
        error = presented.message
        errorField = presented.field
        if (presented.field === "currentPassphrase") requireCredential = true
      }
      return
    }
    await Promise.all([refreshSecretProtection(), refreshSecrets()])
    open = false
    toasts.success("Secret security updated.")
  } catch (failure) {
    const presented = mapSecretError(failure)
    if (!presented.cancelled) {
      error = presented.message
      errorField = presented.field
      if (presented.field === "currentPassphrase") requireCredential = true
    }
  } finally {
    clearCredentials()
    busy = false
  }
}
</script>

<Dialog.Root bind:open>
  <Dialog.Content showCloseButton={!busy} onEscapeKeydown={(event) => { if (busy) event.preventDefault() }} onInteractOutside={(event) => { if (busy) event.preventDefault() }}>
    <Dialog.Header>
      <Dialog.Title>{titles[action]}</Dialog.Title>
      <Dialog.Description>
        {#if action === "remove-passphrase"}File-backed secrets will be re-encrypted using Devsy's automatic key mode.
        {:else if action === "remember"}Devsy stores the passphrase in your operating system credential manager.
        {:else if action === "forget"}Remove automatic access on this device. Secrets may remain available in this desktop session.
        {:else}This protection applies to file-backed secrets across all contexts.{/if}
      </Dialog.Description>
    </Dialog.Header>
    <form class="space-y-4" onsubmit={(event) => { event.preventDefault(); void submit() }}>
      {#if needsCurrent}
        <label class="block space-y-2 text-sm">Current passphrase
          <Input type="password" aria-label="Current passphrase" aria-describedby={errorField === "currentPassphrase" ? "protection-current-error" : undefined} autocomplete="off" spellcheck={false} autocapitalize="none" bind:value={currentPassphrase} disabled={busy} />
        </label>
        {#if error && errorField === "currentPassphrase"}<p id="protection-current-error" role="alert" class="text-sm text-destructive">{error}</p>{/if}
      {/if}
      {#if setsPassphrase}
        <label class="block space-y-2 text-sm">New passphrase<Input type="password" aria-label="New passphrase" aria-describedby={errorField === "newPassphrase" ? "protection-new-error" : undefined} autocomplete="off" spellcheck={false} autocapitalize="none" bind:value={newPassphrase} disabled={busy} /></label>
        <label class="block space-y-2 text-sm">Confirm new passphrase<Input type="password" aria-label="Confirm new passphrase" autocomplete="off" spellcheck={false} autocapitalize="none" bind:value={confirmation} disabled={busy} /></label>
        {#if newPassphrase.length > 0 && newPassphrase.length < 12}<p class="text-sm text-muted-foreground">Choose a long passphrase; at least 12 characters is recommended.</p>{/if}
      {/if}
      {#if error && errorField !== "currentPassphrase"}<p id="protection-new-error" role="alert" class="text-sm text-destructive">{error}</p>{/if}
      <Dialog.Footer>
        <Button type="button" variant="outline" disabled={busy} onclick={() => { clearCredentials(); open = false }}>Cancel</Button>
        <Button type="submit" variant={action === "remove-passphrase" ? "destructive" : "default"} disabled={busy}>{busy ? "Updating…" : action === "remove-passphrase" ? "Remove protection" : titles[action]}</Button>
      </Dialog.Footer>
    </form>
  </Dialog.Content>
</Dialog.Root>
