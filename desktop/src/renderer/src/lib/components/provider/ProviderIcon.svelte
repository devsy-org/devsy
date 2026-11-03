<script lang="ts">
import { activeContext } from "$lib/stores/contexts.js"
import { effectiveTheme } from "$lib/stores/settings.js"
import { loadProviderIcon } from "$lib/utils/provider-icon-loader.js"
import type { ProviderIconTheme } from "$shared/provider-icon.js"

// `icon`, `iconDark` and `iconRevision` come from an installed provider. A
// catalog entry that is not installed yet passes only its name.
let {
  name,
  icon = "",
  iconDark = "",
  iconRevision = "",
  class: className = "size-8",
}: {
  name: string
  icon?: string
  iconDark?: string
  iconRevision?: string
  class?: string
} = $props()

const ICON_MAP: Record<string, string> = {
  docker: "./icons/providers/docker.svg",
  podman: "./icons/providers/podman.svg",
  apple: "./icons/providers/apple.svg",
  lima: "./icons/providers/lima.svg",
  orbstack: "./icons/providers/orbstack.png",
  aws: "./icons/providers/aws.svg",
  amazon: "./icons/providers/aws.svg",
  gcloud: "./icons/providers/gcp.svg",
  gcp: "./icons/providers/gcp.svg",
  google: "./icons/providers/gcp.svg",
  azure: "./icons/providers/azure.svg",
  microsoft: "./icons/providers/azure.svg",
  kubernetes: "./icons/providers/kubernetes.svg",
  k8s: "./icons/providers/kubernetes.svg",
  k3s: "./icons/providers/kubernetes.svg",
  microsandbox: "./icons/providers/microsandbox.svg",
  microvm: "./icons/providers/microsandbox.svg",
  digitalocean: "./icons/providers/digitalocean.svg",
  ssh: "./icons/providers/ssh.svg",
  civo: "./icons/providers/civo.svg",
  terraform: "./icons/providers/terraform.svg",
}
const PLACEHOLDER = "./icons/providers/provider_placeholder.svg"
const LIMA = ICON_MAP.lima

const builtin = $derived.by(() => {
  const lower = name.toLowerCase()
  if (ICON_MAP[lower]) return ICON_MAP[lower]
  for (const [key, value] of Object.entries(ICON_MAP)) {
    if (lower.includes(key)) return value
  }
  return undefined
})

// Dark mode prefers the dark image and falls back to the light one; light
// mode only ever uses the light image.
const customVariants = $derived.by((): ProviderIconTheme[] => {
  if (!iconRevision) return []
  const variants: ProviderIconTheme[] = []
  if ($effectiveTheme === "dark" && iconDark) variants.push("dark")
  if (icon) variants.push("light")
  return variants
})

interface Resolution {
  key: string
  // undefined: still loading; null: missing or unavailable.
  loaded: Partial<Record<ProviderIconTheme, string | null>>
  failed: string[]
}

// Everything that invalidates loaded images or recorded failures.
const resolutionKey = $derived(
  `${$activeContext}\0${name}\0${iconRevision}\0${$effectiveTheme}`,
)

let resolution = $state<Resolution>({ key: "", loaded: {}, failed: [] })
// A resolution recorded under another key is ignored at once, so a rename,
// revision change or theme switch never paints the previous images.
const live = $derived<Resolution>(
  resolution.key === resolutionKey
    ? resolution
    : { key: resolutionKey, loaded: {}, failed: [] },
)

$effect(() => {
  const key = resolutionKey
  const context = $activeContext
  const providerName = name
  const revision = iconRevision
  resolution = { key, loaded: {}, failed: [] }

  let current = true
  for (const theme of customVariants) {
    void loadProviderIcon({ providerName, context, theme, revision }).then(
      (url) => {
        if (!current) return
        resolution = {
          ...resolution,
          loaded: { ...resolution.loaded, [theme]: url },
        }
      },
    )
  }
  return () => {
    current = false
  }
})

const view = $derived.by(() => {
  const fallback =
    builtin && !live.failed.includes(builtin) ? builtin : PLACEHOLDER
  for (const variant of customVariants) {
    const url = live.loaded[variant]
    if (url === undefined) {
      return { src: fallback }
    }
    if (url !== null && !live.failed.includes(url)) {
      return { src: url }
    }
  }
  return { src: fallback }
})

// Each failing source is recorded once, so the chain advances one candidate
// per failure and the same broken source is never requested again.
function advance() {
  if (live.failed.includes(view.src)) return
  resolution = { ...live, failed: [...live.failed, view.src] }
}
</script>

<img
  src={view.src}
  alt="{name} icon"
  width="32"
  height="32"
  class="{className} {view.src === LIMA ? 'dark:invert' : ''}"
  onerror={advance}
/>
