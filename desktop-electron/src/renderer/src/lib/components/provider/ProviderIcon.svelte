<script lang="ts">
let { name, class: className = "size-8" }: { name: string; class?: string } =
  $props()

const ICON_MAP: Record<string, string> = {
  docker: "/icons/providers/docker.svg",
  aws: "/icons/providers/aws.svg",
  amazon: "/icons/providers/aws.svg",
  gcloud: "/icons/providers/gcp.svg",
  gcp: "/icons/providers/gcp.svg",
  google: "/icons/providers/gcp.svg",
  azure: "/icons/providers/azure.svg",
  microsoft: "/icons/providers/azure.svg",
  kubernetes: "/icons/providers/kubernetes.svg",
  k8s: "/icons/providers/kubernetes.svg",
  k3s: "/icons/providers/kubernetes.svg",
  digitalocean: "/icons/providers/digitalocean.svg",
  ssh: "/icons/providers/ssh.svg",
  civo: "/icons/providers/civo.svg",
  terraform: "/icons/providers/terraform.svg",
}

const src = $derived.by(() => {
  const lower = name.toLowerCase()
  if (ICON_MAP[lower]) return ICON_MAP[lower]
  for (const [key, value] of Object.entries(ICON_MAP)) {
    if (lower.includes(key)) return value
  }
  return "/icons/providers/provider_placeholder.svg"
})
</script>

<img {src} alt="{name} icon" class={className} />
