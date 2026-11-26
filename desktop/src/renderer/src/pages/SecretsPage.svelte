<script lang="ts">
import { onMount } from "svelte"
import { replace } from "$lib/router.js"

onMount(() => {
  void replace("/variables?tab=secrets")
})
</script>
