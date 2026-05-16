<script lang="ts">
import { TriangleAlert } from "@lucide/svelte"
import { goto } from "$lib/router.js"
import { Button } from "$lib/components/ui/button/index.js"
</script>

<div class="flex h-full items-center justify-center">
  <div class="flex flex-col items-center gap-4 text-center">
    <TriangleAlert class="h-12 w-12 text-muted-foreground" />
    <h1 class="text-4xl font-bold">404</h1>
    <p class="text-muted-foreground">Page not found</p>
    <div class="flex gap-2">
      <Button variant="outline" onclick={() => history.back()}>Go Back</Button>
      <Button onclick={() => goto("/")}>Dashboard</Button>
    </div>
  </div>
</div>
