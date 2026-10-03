<script lang="ts">
import { onMount } from "svelte"
import * as Dialog from "$lib/components/ui/dialog/index.js"
import { Button } from "$lib/components/ui/button/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import { refreshSecrets } from "$lib/stores/secrets.js"
import { invoke, listen } from "$lib/ipc/bridge.js"
let open = $state(false)
let passphrase = $state("")
let remember = $state(false)
let busy = $state(false)
let error = $state("")
onMount(() => {
  let destroyed = false
  let unlisten: (() => void) | undefined
  void listen("secret_unlock_required", () => {
    open = true
    error = ""
  }).then((off) => {
    if (destroyed) off()
    else unlisten = off
  })
  return () => {
    destroyed = true
    unlisten?.()
  }
})
async function submit() {
  busy = true
  try {
    const result = await invoke<{ ok: boolean; message?: string }>(
      "secret_unlock_submit",
      { passphrase, remember },
    )
    passphrase = ""
    if (result.ok) {
      open = false
      remember = false
      await refreshSecrets()
    } else error = result.message ?? "Unable to unlock secrets."
  } catch {
    error = "Unable to submit the unlock credential."
  } finally {
    passphrase = ""
    busy = false
  }
}
function cancel() {
  passphrase = ""
  remember = false
  void invoke("secret_unlock_submit", {}).catch(() => undefined)
}
</script>
<Dialog.Root bind:open onOpenChange={(value) => { if (!value) cancel() }}>
  <Dialog.Content>
    <Dialog.Header>
      <Dialog.Title>Unlock protected secrets</Dialog.Title>
      <Dialog.Description>Enter the passphrase to retry this operation. It stays in this desktop session unless you choose to remember it.</Dialog.Description>
    </Dialog.Header>
    <form onsubmit={(event) => { event.preventDefault(); void submit() }} class="space-y-4">
      <Input type="password" aria-label="Secrets passphrase" autocomplete="off" bind:value={passphrase} disabled={busy} />
      <label class="flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={remember} disabled={busy} />Remember in OS keychain</label>
      <p class="text-xs text-muted-foreground">Remembering allows Devsy CLI and Desktop to recover the credential through your OS credential store.</p>
      {#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
      <Dialog.Footer>
        <Button type="button" variant="outline" disabled={busy} onclick={() => { cancel(); open = false }}>Cancel</Button>
        <Button type="submit" disabled={busy || !passphrase.trim()}>{busy ? "Unlocking…" : "Unlock and retry"}</Button>
      </Dialog.Footer>
    </form>
  </Dialog.Content>
</Dialog.Root>
