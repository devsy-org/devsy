<script lang="ts">
import * as Sheet from "$lib/components/ui/sheet/index.js"
import * as Dialog from "$lib/components/ui/dialog/index.js"
import { Button } from "$lib/components/ui/button/index.js"
import { secretSessionClear, secretUnlockRequest } from "$lib/ipc/commands.js"
import type { SecretProtectionAction } from "$lib/types/index.js"
import {
  secretProtectionStatus,
  secretProtectionLoading,
  secretProtectionError,
  refreshSecretProtection,
} from "$lib/stores/secret-protection.js"
import { refreshSecrets } from "$lib/stores/secrets.js"
import {
  getSecretProtectionViewState,
  secretProtectionStateLabel,
  secretProtectionModeLabel,
} from "$lib/variables/secret-protection-state.js"
import { mapSecretError } from "$lib/variables/secret-errors.js"
import SecretProtectionActionDialog from "./SecretProtectionActionDialog.svelte"

let { open = $bindable(false) }: { open?: boolean } = $props()
let action = $state<SecretProtectionAction>("set-passphrase")
let actionOpen = $state(false)
let recoveryOpen = $state(false)
let copied = $state(false)
let recoveryError = $state("")
let busy = $state(false)
let error = $state("")
let view = $derived(getSecretProtectionViewState($secretProtectionStatus))
let protectedStore = $derived(
  $secretProtectionStatus?.keySource === "passphrase",
)
let healthy = $derived(
  view === "passphrase_available" ||
    view === "automatic_available" ||
    view === "passphrase_locked" ||
    view === "uninitialized",
)
let statusLabel = $derived(secretProtectionStateLabel(view))
let actionsDisabled = $derived(
  busy || $secretProtectionLoading || !!$secretProtectionError,
)
$effect(() => {
  if (open) {
    void refreshSecretProtection()
  }
})
function selectAction(selected: SecretProtectionAction) {
  action = selected
  actionOpen = true
}
async function sessionAction(unlock: boolean) {
  busy = true
  error = ""
  try {
    // Closing the sheet lets the shared unlock dialog receive focus.
    if (unlock) open = false
    if (!unlock) await secretSessionClear()
    const result = unlock ? await secretUnlockRequest() : { ok: true }
    if (!result.ok) {
      const presented = mapSecretError(result)
      if (!presented.cancelled) {
        error = presented.message
        open = true
      }
      return
    }
    await Promise.all([refreshSecretProtection(), refreshSecrets()])
  } catch (failure) {
    error = mapSecretError(failure).message
    open = true
  } finally {
    busy = false
  }
}
async function copyRecoveryCommand() {
  try {
    await navigator.clipboard.writeText(
      "devsy secret protection reset-file-store",
    )
    copied = true
  } catch {
    copied = false
    recoveryError = "Unable to copy the command. Select and copy it manually."
  }
}
</script>

<Sheet.Root bind:open>
  <Sheet.Content class="w-full overflow-y-auto p-6 sm:max-w-md">
    <Sheet.Header>
      <Sheet.Title>Secret Security</Sheet.Title>
      <Sheet.Description>Protection for shared file-backed secrets across all contexts. Environment variables and keyring-backed secrets are managed separately.</Sheet.Description>
    </Sheet.Header>
    {#if $secretProtectionLoading && !$secretProtectionStatus}<p role="status">Loading security status…</p>{/if}
    {#if $secretProtectionError}<p role="alert" class="text-sm text-destructive">{$secretProtectionError}</p><Button variant="outline" onclick={() => refreshSecretProtection()}>Retry</Button>{/if}
    <dl class="grid grid-cols-2 gap-3 rounded-lg border p-4 text-sm">
      <dt class="text-muted-foreground">Status</dt><dd>{statusLabel}</dd>
      <dt class="text-muted-foreground">Protection</dt><dd>{secretProtectionModeLabel($secretProtectionStatus)}</dd>
      <dt class="text-muted-foreground">Remembered on device</dt><dd>{$secretProtectionStatus?.rememberedAvailable === false ? "Unavailable" : $secretProtectionStatus?.remembered ? "Yes" : "No"}</dd>
    </dl>
    {#if healthy && $secretProtectionStatus}
      <section class="space-y-3"><h3 class="font-medium">Passphrase protection</h3>
        <Button variant="outline" disabled={actionsDisabled} onclick={() => selectAction(protectedStore ? "change-passphrase" : "set-passphrase")}>{protectedStore ? "Change passphrase" : "Use a passphrase"}</Button>
        {#if view === "passphrase_locked"}<Button disabled={actionsDisabled} onclick={() => sessionAction(true)}>Unlock</Button>{/if}
      </section>
      {#if protectedStore}
        {#if !$secretProtectionStatus.remembered}
        <section class="space-y-3"><h3 class="font-medium">Device access</h3>
          <p class="text-sm text-muted-foreground">Devsy stores the passphrase in your operating system credential manager.</p>
          {#if !$secretProtectionStatus.remembered && $secretProtectionStatus.rememberedAvailable}<Button variant="outline" disabled={actionsDisabled} onclick={() => selectAction("remember")}>Remember on this device</Button>
          {:else if !$secretProtectionStatus.rememberedAvailable}<p class="text-sm text-muted-foreground">Your operating system credential manager is unavailable.</p>{/if}
        </section>
        {/if}
        <section class="space-y-3 border-t pt-4"><h3 class="font-medium">Remove protection</h3><Button variant="destructive" disabled={actionsDisabled} onclick={() => selectAction("remove-passphrase")}>Remove passphrase protection</Button></section>
      {/if}
    {:else if $secretProtectionStatus}<p class="text-sm text-muted-foreground">Devsy needs attention to access the shared secret store. Review recovery options before changing protection.</p>{/if}
    {#if $secretProtectionStatus?.remembered}
      <section class="space-y-3"><h3 class="font-medium">Device access</h3><Button variant="outline" disabled={actionsDisabled} onclick={() => selectAction("forget")}>Forget this device</Button></section>
    {/if}
    {#if $secretProtectionStatus?.sessionUnlocked}
      <section class="space-y-3"><h3 class="font-medium">Advanced</h3><p class="text-sm text-muted-foreground">Removes the passphrase held by this desktop session. Other available credentials may still unlock the store.</p><Button variant="outline" disabled={busy} onclick={() => sessionAction(false)}>Clear session passphrase</Button></section>
    {/if}
    <Button variant="ghost" onclick={() => { copied = false; recoveryError = ""; recoveryOpen = true }}>Recovery options</Button>
    {#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
  </Sheet.Content>
</Sheet.Root>
<SecretProtectionActionDialog bind:open={actionOpen} {action} status={$secretProtectionStatus} />
<Dialog.Root bind:open={recoveryOpen}>
  <Dialog.Content>
    <Dialog.Header><Dialog.Title>Recovery options</Dialog.Title><Dialog.Description>A passphrase remembered on this device may still unlock the store. Without a valid credential, encrypted file contents cannot be decrypted.</Dialog.Description></Dialog.Header>
    <p class="text-sm">Resetting permanently removes every file-backed secret across all contexts. Keyring-backed secrets, environment variables, and external secret sources are separate.</p>
    <p class="text-sm">Review and confirm a reset in your terminal:</p>
    <code class="break-all rounded bg-muted p-2 text-xs">devsy secret protection reset-file-store</code>
    {#if recoveryError}<p role="alert" class="text-sm text-destructive">{recoveryError}</p>{/if}
    <Dialog.Footer><Button variant="outline" onclick={() => { recoveryOpen = false }}>Close</Button><Button onclick={copyRecoveryCommand}>{copied ? "Copied" : "Copy command"}</Button></Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
