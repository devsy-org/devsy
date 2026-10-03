<script lang="ts">
import { onMount } from "svelte"
import { Button } from "$lib/components/ui/button/index.js"
import { Input } from "$lib/components/ui/input/index.js"
import { refreshSecrets } from "$lib/stores/secrets.js"
import { invoke } from "$lib/ipc/bridge.js"
let status = $state<{
  availability?: string
  keySource?: string
  remembered?: boolean
  rememberedAvailable?: boolean
  sessionUnlocked?: boolean
}>({})
let passphrase = $state("")
let confirmation = $state("")
let error = $state("")
let busy = $state(false)
async function refresh() {
  try {
    status = await invoke("secret_protection_status")
  } catch {
    error = "Unable to read secret protection status."
  }
}
onMount(() => {
  void refresh()
})
async function action(name: string) {
  if (
    ["set-passphrase", "change-passphrase"].includes(name) &&
    passphrase !== confirmation
  ) {
    error = "Passphrases do not match."
    return
  }
  busy = true
  error = ""
  try {
    if (name === "clear-session") await invoke("secret_session_clear")
    else {
      const result = await invoke<{ ok: boolean; message?: string }>(
        "secret_protection_action",
        {
          action: name,
          ...(["set-passphrase", "change-passphrase"].includes(name)
            ? { passphrase }
            : {}),
        },
      )
      if (!result.ok)
        error = result.message ?? "Secret protection operation failed."
    }
    await refresh()
    await refreshSecrets()
  } catch {
    error = "Secret protection operation failed."
  } finally {
    passphrase = ""
    confirmation = ""
    busy = false
  }
}
</script>
<section id="secret-protection" aria-labelledby="secret-protection-heading" class="space-y-4 rounded-lg border p-4 sm:p-6">
  <h2 id="secret-protection-heading" class="text-lg font-semibold">Secret protection</h2>
  <p class="text-sm text-muted-foreground">Availability: {status.availability?.replaceAll("_", " ") ?? "unknown"}. File store: {status.keySource === "passphrase" ? "Passphrase protected" : status.keySource ? "Automatic key" : "Not initialized"}. Remembered credential: {status.rememberedAvailable === false ? "OS keychain unavailable" : status.remembered ? "Yes" : "No"}. Session: {status.sessionUnlocked ? "Unlocked" : "No session credential"}.</p>
  <Input type="password" aria-label="New secrets passphrase" placeholder="New passphrase" autocomplete="off" bind:value={passphrase} disabled={busy} />
  <Input type="password" aria-label="Confirm secrets passphrase" placeholder="Confirm new passphrase" autocomplete="off" bind:value={confirmation} disabled={busy} />
  {#if passphrase.length > 0 && passphrase.length < 12}<p class="text-sm text-muted-foreground">Choose a long passphrase; at least 12 characters is recommended.</p>{/if}
  <div class="flex flex-wrap gap-2">
    <Button disabled={busy || !passphrase.trim()} onclick={() => action(status.keySource === "passphrase" ? "change-passphrase" : "set-passphrase")}>{status.keySource === "passphrase" ? "Change passphrase" : "Set passphrase"}</Button>
    <Button variant="outline" disabled={busy || status.keySource !== "passphrase"} onclick={() => action("remove-passphrase")}>Remove passphrase</Button>
    <Button variant="outline" disabled={busy || status.keySource !== "passphrase"} onclick={() => action("remember")}>Remember in OS keychain</Button>
    <Button variant="outline" disabled={busy || !status.remembered} onclick={() => action("forget")}>Forget remembered credential</Button>
    <Button variant="outline" disabled={busy} onclick={() => action("clear-session")}>Clear session credential</Button>
  </div>
  <p class="text-xs text-muted-foreground">All file-backed secrets share this protection. Remembering lets Devsy recover the credential from your OS keychain. Environment variables remain independent.</p>
  {#if status.keySource === "passphrase"}
    <p class="text-xs text-muted-foreground">Forgotten passphrase? Check for a remembered credential first. Without the passphrase, the encrypted file cannot be recovered. Run <code>devsy secret protection reset-file-store</code> in a terminal to review and confirm removal of every file-backed secret across all contexts. Keyring secrets and environment variables are unaffected.</p>
  {/if}
  {#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
</section>
