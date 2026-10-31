<script lang="ts">
import { onMount } from "svelte"
import * as Dialog from "$lib/components/ui/dialog/index.js"
import { Button } from "$lib/components/ui/button/index.js"
import { getAppSettings, setAppSettings } from "$lib/ipc/commands.js"
import {
  addToApplications,
  integrationBusy,
  integrationError,
  refreshIntegrationStatus,
  shouldPromptForIntegration,
} from "$lib/stores/linux-desktop-integration.js"
import { syncDesktopSettingsFromMain } from "$lib/stores/settings.js"

let open = $state(false)

// Persisting at display time keeps the prompt to a single appearance even if
// the window is closed without choosing.
onMount(() => {
  let destroyed = false
  void (async () => {
    try {
      const status = await refreshIntegrationStatus()
      if (!status || destroyed) return
      const { settings } = await getAppSettings()
      if (
        destroyed ||
        !shouldPromptForIntegration(
          status,
          settings.appImageIntegrationPromptDismissed,
        )
      )
        return
      // Do not show the prompt until its dismissal is durably recorded.
      await setAppSettings({ appImageIntegrationPromptDismissed: true })
      if (!destroyed) open = true
      await syncDesktopSettingsFromMain()
    } catch (err) {
      console.warn("[integration] prompt unavailable:", err)
    }
  })()
  return () => {
    destroyed = true
  }
})

async function add() {
  await addToApplications()
  if (!$integrationError) open = false
}
</script>

<Dialog.Root bind:open>
  <Dialog.Content>
    <Dialog.Header>
      <Dialog.Title>Add Devsy to your applications?</Dialog.Title>
      <Dialog.Description>You started Devsy directly from an AppImage, so your desktop has no launcher or icon for it. Adding Devsy creates a launcher in your applications menu and lets devsy:// links open this AppImage. You can remove it later in Settings.</Dialog.Description>
    </Dialog.Header>
    {#if $integrationError}<p role="alert" class="text-sm text-destructive">{$integrationError}</p>{/if}
    <Dialog.Footer>
      <Button type="button" variant="outline" onclick={() => (open = false)} disabled={$integrationBusy}>Not now</Button>
      <Button type="button" onclick={add} disabled={$integrationBusy}>{$integrationBusy ? "Adding..." : "Add to applications"}</Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
