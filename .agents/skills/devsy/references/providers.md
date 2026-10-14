# Provider workflows

A configured provider determines where a workspace runs. Registry source names
such as `docker` or `kubernetes` are not necessarily the installed configuration
names. List providers and use the exact returned name when selecting one.

## Select for one workspace

Call `provider_list` if selection matters. Inspect `name`, `default`, `status`,
and optional `version`; choose a suitable existing provider. Pass its name to
`workspace_create.provider`. Omit provider when the current default satisfies
the request. A provider's status is not the workspace container's running state.

## Configure

Add a provider only when setup is within the request. Sources can be a registry
name, GitHub release URL, or local provider config path. Provider-specific
options come from documentation/metadata, not guessed flag names. Sensitive
options and provider config should not be echoed into the conversation.

Before adding, inspect the current default. The current MCP adapter forwards
`--use` only for `use=true`, while CLI add itself defaults to true; false/omitted
MCP `use` therefore does not ensure the old default remains. To add without
changing it, use `devsy provider add <source> --use=false` through CLI. This
installs but does not initialize the provider; options supplied to this add path
are not applied through initialization. If setup for use is intended, discover
the installed name and run `devsy provider init <configured-name>`, supplying
required repeatable `--option KEY=VALUE` values to **init**. Init resolves options
and runs provider setup without selecting it as the default. List afterward to
verify status and that the prior default remains before creating a workspace.
If only installation was requested, report that initialization is still needed.
Otherwise verify the actual default after MCP add and report any change. Do
not silently broaden a request for one workspace into a default configuration
change.

Use `provider_use` only when changing the default is requested or clearly needed.
This affects later workspace creates that omit a provider; it does not migrate
existing workspaces.

## Remove or recover

Provider deletion is shared configuration mutation, not workspace repair.
Verify target and intent, inspect dependent workspaces, and expect deletion to
be refused if workspaces use the provider. Do not force-remove it or delete its
workspaces to bypass that refusal. A failing provider calls for diagnosis of
configuration/connectivity/runtime, not automatic deletion or replacement.
