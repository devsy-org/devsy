<script lang="ts">
import { Button } from "$lib/components/ui/button/index.js"
import { Separator } from "$lib/components/ui/separator/index.js"
import { theme, applyTheme } from "$lib/stores/settings.js"
import type { Theme } from "$lib/stores/settings.js"

const THEMES: { value: Theme; label: string }[] = [
  { value: "light", label: "Light" },
  { value: "dark", label: "Dark" },
  { value: "system", label: "System" },
]

function setTheme(value: Theme) {
  theme.set(value)
  applyTheme(value)
}
</script>

<div class="mx-auto max-w-xl space-y-6">
  <h1 class="text-2xl font-bold">Settings</h1>

  <div class="space-y-4">
    <h2 class="text-lg font-semibold">Theme</h2>
    <div class="flex gap-2">
      {#each THEMES as t (t.value)}
        <Button
          variant={$theme === t.value ? "default" : "outline"}
          onclick={() => setTheme(t.value)}
        >
          {t.label}
        </Button>
      {/each}
    </div>
  </div>

  <Separator />

  <div class="space-y-4">
    <h2 class="text-lg font-semibold">About</h2>
    <div class="space-y-1 text-sm text-muted-foreground">
      <p>DevPod Desktop</p>
      <p>Built with Tauri v2 + SvelteKit + shadcn-svelte</p>
    </div>
  </div>
</div>
