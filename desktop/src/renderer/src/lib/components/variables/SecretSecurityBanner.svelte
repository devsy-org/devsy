<script lang="ts">
import { Button } from "$lib/components/ui/button/index.js"
import {
  secretProtectionStatus,
  secretProtectionError,
  secretProtectionLoading,
  refreshSecretProtection,
} from "$lib/stores/secret-protection.js"
import { refreshSecrets } from "$lib/stores/secrets.js"
import { secretUnlockRequest } from "$lib/ipc/commands.js"
import { getSecretProtectionViewState } from "$lib/variables/secret-protection-state.js"
import { mapSecretError } from "$lib/variables/secret-errors.js"
let { onManageSecurity }: { onManageSecurity: () => void } = $props()
let busy = $state(false)
let error = $state("")
let view = $derived(getSecretProtectionViewState($secretProtectionStatus))
const messages = {
  passphrase_locked: "File-backed secrets are locked",
  backend_unavailable:
    "Devsy cannot access the credential service required by this secret store.",
  file_store_missing: "The file-backed secret store is missing.",
  store_corrupt: "Devsy couldn't read the encrypted secret store.",
  ownership_unknown: "Devsy couldn't verify the secret store's ownership.",
  unknown: "Secret security status is unavailable.",
}
let message = $derived(
  view in messages ? messages[view as keyof typeof messages] : "",
)
async function unlock() {
  busy = true
  error = ""
  try {
    const result = await secretUnlockRequest()
    if (!result.ok) {
      const presented = mapSecretError(result)
      if (!presented.cancelled) error = presented.message
    } else await Promise.all([refreshSecretProtection(), refreshSecrets()])
  } catch (failure) {
    error = mapSecretError(failure).message
  } finally {
    busy = false
  }
}
</script>

{#if message && ($secretProtectionStatus || $secretProtectionError)}
  <div role="status" class={`space-y-2 rounded-lg border p-4 ${view === "passphrase_locked" ? "bg-muted/40" : "border-destructive/40 bg-destructive/5"}`}>
    <p class="text-sm font-medium">{message}</p>
    {#if view === "passphrase_locked"}<p class="text-sm text-muted-foreground">Unlock them to use their values in workspace operations.</p>{/if}
    <div class="flex flex-wrap gap-2">
      {#if view === "passphrase_locked"}<Button size="sm" disabled={busy || $secretProtectionLoading || !!$secretProtectionError} onclick={unlock}>{busy ? "Unlocking…" : "Unlock"}</Button>{/if}
      <Button size="sm" variant="outline" onclick={onManageSecurity}>Manage security</Button>
    </div>
    {#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
  </div>
{/if}
