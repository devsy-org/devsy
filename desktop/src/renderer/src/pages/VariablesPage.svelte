<script lang="ts">
import { Button } from "$lib/components/ui/button/index.js"
import * as Tabs from "$lib/components/ui/tabs/index.js"
import SecretsVariablesPanel from "$lib/components/variables/SecretsVariablesPanel.svelte"
import EnvironmentVariablesPanel from "$lib/components/variables/EnvironmentVariablesPanel.svelte"
import SecretSecurityBanner from "$lib/components/variables/SecretSecurityBanner.svelte"
import SecretSecuritySheet from "$lib/components/variables/SecretSecuritySheet.svelte"
import { querystring, replace } from "$lib/router.js"
import { initSecretProtection } from "$lib/stores/secret-protection.js"

type VariablesTab = "secrets" | "env"
let tab: VariablesTab = $derived(
  new URLSearchParams($querystring).get("tab") === "env" ? "env" : "secrets",
)
let addSecretOpen = $state(false)
let addEnvOpen = $state(false)
let securityOpen = $state(false)

$effect(() => {
  if (tab === "secrets") void initSecretProtection()
  // Switching tabs unmounts its forms and clears pending inputs.
  else addSecretOpen = false
  if (tab !== "env") addEnvOpen = false
})

function selectTab(value: string) {
  if (value === "secrets" || value === "env")
    void replace(`/variables?tab=${value}`)
}
</script>

<div class="flex min-h-0 flex-1 flex-col gap-6 overflow-y-auto">
  <header class="flex flex-wrap items-start justify-between gap-3">
    <div>
      <h1 class="text-2xl font-bold tracking-tight">Workspace Variables</h1>
      <p class="mt-1 text-sm text-muted-foreground">Manage secrets and non-sensitive configuration used by your workspaces.</p>
    </div>
    <Button onclick={() => tab === "secrets" ? (addSecretOpen = true) : (addEnvOpen = true)}>
      {tab === "secrets" ? "Add secret" : "Add variable"}
    </Button>
  </header>

  <Tabs.Root value={tab} onValueChange={selectTab}>
    <Tabs.List aria-label="Variable types">
      <Tabs.Trigger value="secrets" class="data-[state=active]:bg-background data-[state=active]:text-foreground data-[state=active]:shadow-sm dark:data-[state=active]:bg-input/30 dark:data-[state=active]:text-foreground">Secrets</Tabs.Trigger>
      <Tabs.Trigger value="env" class="data-[state=active]:bg-background data-[state=active]:text-foreground data-[state=active]:shadow-sm dark:data-[state=active]:bg-input/30 dark:data-[state=active]:text-foreground">Environment Variables</Tabs.Trigger>
    </Tabs.List>
    <Tabs.Content value="secrets" class="mt-4 space-y-4">
      <SecretSecurityBanner onManageSecurity={() => (securityOpen = true)} />
      <SecretsVariablesPanel bind:addOpen={addSecretOpen} onManageSecurity={() => (securityOpen = true)} />
    </Tabs.Content>
    <Tabs.Content value="env" class="mt-4">
      <EnvironmentVariablesPanel bind:addOpen={addEnvOpen} />
    </Tabs.Content>
  </Tabs.Root>
  <SecretSecuritySheet bind:open={securityOpen} />
</div>
