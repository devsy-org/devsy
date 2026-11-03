<script lang="ts">
import * as Tooltip from "$lib/components/ui/tooltip/index.js"
import WorkspaceDetailPage from "./WorkspaceDetailPage.svelte"

let { params = {} }: { params?: Record<string, string> } = $props()
</script>

<Tooltip.Provider>
  <WorkspaceDetailPage {params} />
</Tooltip.Provider>
