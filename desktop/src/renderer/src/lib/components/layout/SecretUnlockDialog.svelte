<script lang="ts">
import { onMount } from "svelte"
import * as Dialog from "$lib/components/ui/dialog/index.js"
import { Button } from "$lib/components/ui/button/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import { refreshSecrets } from "$lib/stores/secrets.js"
import { toasts } from "$lib/stores/toasts.js"
import { invoke, listen } from "$lib/ipc/bridge.js"
let open = $state(false)
let passphrase = $state("")
let remember = $state(false)
let busy = $state(false)
let error = $state("")
let requestId = $state("")
let readRememberedNotices: () => void = () => {}
onMount(() => {
  let destroyed = false
  let unlisten: (() => void) | undefined
  let unlistenNotices: (() => void) | undefined
  let readingNotices = false
  let readRequested = false
  const shownNotices = new Set<string>()
  async function reportRememberedNotices() {
    readRequested = true
    if (readingNotices) return
    readingNotices = true
    try {
      while (readRequested && !destroyed) {
        readRequested = false
        const result = await invoke<{
          ok: boolean
          notices?: { requestId: string; message: string }[]
        }>("secret_unlock_notices")
        if (destroyed || !result.ok) return
        for (const notice of result.notices ?? []) {
          if (destroyed) return
          if (!shownNotices.has(notice.requestId)) {
            toasts.info(notice.message, { sticky: true })
            shownNotices.add(notice.requestId)
          }
          await invoke("secret_unlock_notice_ack", {
            requestId: notice.requestId,
          })
        }
      }
    } catch {
      // Main retains unacknowledged outcomes for the next event or page mount.
    } finally {
      readingNotices = false
    }
  }
  readRememberedNotices = () => {
    void reportRememberedNotices()
  }
  void listen("secret_unlock_notice", readRememberedNotices).then((off) => {
    if (destroyed) off()
    else {
      unlistenNotices = off
      readRememberedNotices()
    }
  })
  void listen<{ requestId?: unknown }>("secret_unlock_required", (event) => {
    const nextRequestId = event.payload?.requestId
    if (typeof nextRequestId !== "string" || !nextRequestId.trim()) return
    if (requestId === nextRequestId) return
    requestId = nextRequestId
    passphrase = ""
    remember = false
    open = true
    error = ""
    busy = false
  }).then((off) => {
    if (destroyed) off()
    else unlisten = off
  })
  return () => {
    destroyed = true
    readRememberedNotices = () => {}
    unlisten?.()
    unlistenNotices?.()
  }
})
async function submit() {
  const submittedRequestId = requestId
  if (!submittedRequestId || busy) return
  const submittedPassphrase = passphrase
  const submittedRemember = remember
  busy = true
  try {
    const result = await invoke<{
      ok: boolean
      remembered?: boolean
      message?: string
    }>("secret_unlock_submit", {
      requestId: submittedRequestId,
      passphrase: submittedPassphrase,
      remember: submittedRemember,
    })
    if (!result.ok && result.remembered === true) readRememberedNotices()
    if (requestId !== submittedRequestId) return
    passphrase = ""
    if (result.ok || result.remembered === true) {
      requestId = ""
      open = false
      remember = false
      await refreshSecrets()
    } else error = result.message ?? "Unable to unlock secrets."
  } catch {
    if (requestId === submittedRequestId)
      error = "Unable to submit the unlock credential."
  } finally {
    if (requestId === submittedRequestId) {
      passphrase = ""
      busy = false
    }
  }
}
function cancel() {
  const canceledRequestId = requestId
  requestId = ""
  passphrase = ""
  remember = false
  busy = false
  if (canceledRequestId) {
    void invoke("secret_unlock_submit", {
      requestId: canceledRequestId,
    }).catch(() => undefined)
  }
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
      <p class="text-xs text-muted-foreground">Once approved, remembering may finish even if you cancel unlocking. Use Forget in Settings to remove the saved credential.</p>
      {#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
      <Dialog.Footer>
        <Button type="button" variant="outline" onclick={() => { cancel(); open = false }}>Cancel</Button>
        <Button type="submit" disabled={busy || !passphrase.trim()}>{busy ? "Unlocking…" : "Unlock and retry"}</Button>
      </Dialog.Footer>
    </form>
  </Dialog.Content>
</Dialog.Root>
