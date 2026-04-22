<script lang="ts">
import { goto } from "$app/navigation"
import { onMount } from "svelte"

// Redirect to workspaces page — creation now uses a sheet
onMount(() => {
  goto("/workspaces", { replaceState: true })
})
</script>
