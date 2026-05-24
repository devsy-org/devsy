<script lang="ts">
import { goto } from "$lib/router.js"
import ProviderWizard from "$lib/components/provider/ProviderWizard.svelte"

let wizardOpen = $state(true)

function handleComplete() {
  goto("/providers")
}

$effect(() => {
  if (!wizardOpen) {
    goto("/providers")
  }
})
</script>

<ProviderWizard bind:open={wizardOpen} oncomplete={handleComplete} />
