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

let alreadyLatest = $derived(
  !!currentVersion && !!latestVersion && currentVersion === latestVersion,
)

let title = $derived.by(() => {
  if (alreadyLatest) return `'${providerName}' is already up to date`
  if (latestVersion) return `Update '${providerName}' to ${latestVersion}?`
  if (currentVersion) return `Update '${providerName}' (currently ${currentVersion})?`
  return `Update '${providerName}'?`
})

let description = $derived.by(() => {
  if (alreadyLatest) {
    return `${providerName} is already on ${currentVersion}. Updating will re-fetch the same version.`
  }
  if (currentVersion && latestVersion) {
    return `This will update ${providerName} from ${currentVersion} to ${latestVersion}.`
  }
  if (currentVersion) {
    return `Currently on ${currentVersion}. The latest version isn't available — this will run the provider's update command.`
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
