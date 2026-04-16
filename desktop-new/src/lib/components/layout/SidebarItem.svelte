<script lang="ts">
import { page } from "$app/stores"
import { cn } from "$lib/utils.js"
import type { Component } from "svelte"

let {
  href,
  label,
  badgeCount,
  icon,
}: {
  href: string
  label: string
  badgeCount?: number
  icon?: Component<{ class?: string }>
} = $props()

let isActive = $derived(
  href === "/"
    ? $page.url.pathname === "/"
    : $page.url.pathname.startsWith(href),
)
</script>

<a
  {href}
  class={cn(
    "flex items-center justify-between rounded-md px-3 py-2 text-sm font-medium transition-colors",
    isActive
      ? "bg-accent text-accent-foreground"
      : "text-muted-foreground hover:bg-accent/50 hover:text-accent-foreground",
  )}
>
  <span class="flex items-center gap-2">
    {#if icon}
      {@const Icon = icon}
      <Icon class="h-4 w-4" />
    {/if}
    {label}
  </span>
  {#if badgeCount != null && badgeCount > 0}
    <span
      class="inline-flex items-center rounded-full bg-primary px-2 py-0.5 text-xs font-semibold text-primary-foreground"
    >
      {badgeCount}
    </span>
  {/if}
</a>
