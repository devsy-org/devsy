<script lang="ts">
let { name, class: className = "size-8" }: { name: string; class?: string } =
  $props()

const ICON_MAP: Record<string, string> = {
  python: "/icons/languages/python.svg",
  node: "/icons/languages/nodejs.svg",
  "node.js": "/icons/languages/nodejs.svg",
  nodejs: "/icons/languages/nodejs.svg",
  javascript: "/icons/languages/nodejs.svg",
  go: "/icons/languages/go.svg",
  golang: "/icons/languages/go.svg",
  rust: "/icons/languages/rust.svg",
  java: "/icons/languages/java.svg",
  php: "/icons/languages/php.svg",
  "c++": "/icons/languages/cpp.svg",
  cpp: "/icons/languages/cpp.svg",
  ".net": "/icons/languages/dotnet.svg",
  dotnet: "/icons/languages/dotnet.svg",
  "c#": "/icons/languages/dotnet.svg",
  csharp: "/icons/languages/dotnet.svg",
  ruby: "/icons/languages/ruby.svg",
}

const src = $derived.by(() => {
  const lower = name.toLowerCase()
  if (ICON_MAP[lower]) return ICON_MAP[lower]
  for (const [key, value] of Object.entries(ICON_MAP)) {
    if (lower.includes(key)) return value
  }
  return null
})
</script>

{#if src}
  <img {src} alt="{name} icon" class={className} />
{:else}
  <span
    class="flex items-center justify-center rounded-md bg-muted text-xs font-bold {className}"
  >
    {name.slice(0, 2).toUpperCase()}
  </span>
{/if}
