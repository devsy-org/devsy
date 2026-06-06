<script lang="ts">
let { value = $bindable("") }: { value?: string } = $props()
</script>

<input
  type="search"
  bind:value
  placeholder="Search images…"
  aria-label="Search images"
  class="w-full rounded-md border border-input bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-ring"
/>
