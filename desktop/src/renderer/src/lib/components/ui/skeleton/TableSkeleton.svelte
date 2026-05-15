<script lang="ts">
import { Skeleton } from "./index.js"

let { rows = 5, columns = 5 }: { rows?: number; columns?: number } = $props()
</script>

<div class="rounded-md border">
  <div class="border-b px-4 py-3 flex gap-4">
    {#each Array(columns) as _}
      <Skeleton class="h-4 flex-1" />
    {/each}
  </div>
  {#each Array(rows) as _, i}
    <div class="px-4 py-3 flex gap-4 {i < rows - 1 ? 'border-b' : ''}">
      {#each Array(columns) as _}
        <Skeleton class="h-4 flex-1" />
      {/each}
    </div>
  {/each}
</div>
