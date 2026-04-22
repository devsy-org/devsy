<script lang="ts">
import { ChevronRight } from "@lucide/svelte"
import { page } from "$app/stores"

interface Crumb {
  label: string
  href?: string
}

const LABELS: Record<string, string> = {
  workspaces: "Workspaces",
  providers: "Providers",
  machines: "Machines",
  contexts: "Contexts",
  terminals: "Terminals",
  settings: "Settings",
  new: "New",
  add: "Add",
}

let crumbs = $derived.by(() => {
  const parts = $page.url.pathname.split("/").filter(Boolean)
  if (parts.length === 0) return [] as Crumb[]

  const result: Crumb[] = []
  let path = ""

  for (let i = 0; i < parts.length; i++) {
    path += `/${parts[i]}`
    const isLast = i === parts.length - 1
    const label = LABELS[parts[i]] ?? decodeURIComponent(parts[i])
    result.push({ label, href: isLast ? undefined : path })
  }

  return result
})
</script>

{#if crumbs.length > 0}
  <nav class="flex items-center gap-1 text-sm text-muted-foreground">
    <a href="/" class="hover:text-foreground transition-colors">Home</a>
    {#each crumbs as crumb}
      <ChevronRight class="h-3 w-3" />
      {#if crumb.href}
        <a href={crumb.href} class="hover:text-foreground transition-colors">{crumb.label}</a>
      {:else}
        <span class="text-foreground font-medium">{crumb.label}</span>
      {/if}
    {/each}
  </nav>
{/if}
