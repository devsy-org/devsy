<script lang="ts">
import ConfirmDialog from "$lib/components/layout/ConfirmDialog.svelte"

let {
  providerName,
  currentVersion,
  latestVersion,
  open = $bindable(false),
  loading = false,
  onconfirm,
}: {
  providerName: string
  currentVersion?: string
  latestVersion?: string
  open?: boolean
  loading?: boolean
  onconfirm: () => void
} = $props()

let title = $derived(
  latestVersion
    ? `Update '${providerName}' to ${latestVersion}?`
    : `Update '${providerName}'?`,
)

let description = $derived.by(() => {
  if (currentVersion && latestVersion) {
    return `This will update ${providerName} from ${currentVersion} to ${latestVersion}.`
  }
  return "Check for and install the latest version."
})
</script>

<ConfirmDialog
  bind:open
  {title}
  {description}
  confirmLabel="Update"
  variant="default"
  {loading}
  {onconfirm}
/>
