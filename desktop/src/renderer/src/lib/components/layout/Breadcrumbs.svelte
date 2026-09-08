<script lang="ts">
import { ChevronRight } from "@lucide/svelte"
import { location } from "$lib/router.js"

interface Crumb {
  label: string
  href?: string
}

const LABELS: Record<string, string> = {
  workspaces: "Workspaces",
  providers: "Providers",
  machines: "Machines",
  contexts: "Contexts",
  secrets: "Secrets",
  env: "Env Vars",
  terminals: "Terminals",
  "ssh-keys": "SSH Keys",
  settings: "Settings",
  new: "New",
  add: "Add",
}

let crumbs = $derived.by(() => {
  const parts = $location.split("/").filter(Boolean)
  if (parts.length === 0) return [] as Crumb[]

  const result: Crumb[] = []
  let path = ""

  for (let i = 0; i < parts.length; i++) {
    path += `/${parts[i]}`
    const isLast = i === parts.length - 1
    const label = LABELS[parts[i]] ?? decodeURIComponent(parts[i])
    result.push({ label, href: isLast ? undefined : `#${path}` })
  }

  return result
})
</script>

{#if crumbs.length > 0}
  <nav aria-label="Breadcrumb" class="flex items-center gap-1 text-sm text-muted-foreground">
    <ol class="flex items-center gap-1">
      <li class="flex items-center gap-1">
        <a href="#/" class="hover:text-foreground transition-colors">Home</a>
      </li>
      {#each crumbs as crumb}
        <li class="flex items-center gap-1">
          <ChevronRight class="h-3 w-3" aria-hidden="true" />
          {#if crumb.href}
            <a href={crumb.href} class="hover:text-foreground transition-colors">{crumb.label}</a>
          {:else}
            <span class="text-foreground font-medium" aria-current="page">{crumb.label}</span>
          {/if}
        </li>
      {/each}
    </ol>
  </nav>
{/if}
