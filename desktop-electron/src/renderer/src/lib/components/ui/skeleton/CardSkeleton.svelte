<script lang="ts">
import { Skeleton } from "./index.js"
</script>

<div class="rounded-lg border bg-card p-4 shadow-sm">
  <div class="flex items-start justify-between gap-2">
    <Skeleton class="h-5 w-32" />
    <Skeleton class="h-5 w-16 rounded-full" />
  </div>
  <Skeleton class="mt-3 h-4 w-48" />
  <div class="mt-4 flex gap-2">
    <Skeleton class="h-8 w-20 rounded-md" />
    <Skeleton class="h-8 w-20 rounded-md" />
  </div>
</div>
