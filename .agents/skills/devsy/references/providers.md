# Provider workflows

A configured provider decides where a workspace runs. Registry names such as
`docker` or `kubernetes` can differ from installed configuration names. List
providers and use the exact returned name.

## Select for one workspace

Call `provider_list` when the choice matters. Check `name`, `default`, `status`,
and `version`. Pass the chosen name to `workspace_create.provider`. Omit
`provider` when the default fits. Provider status does not show whether the
workspace container is running.

## Configure

Add a provider only when setup is part of the request. The source can be a
registry name, a GitHub release URL, or a local provider config path. Take
options from provider docs or metadata. Do not guess flag names or repeat
sensitive options in the conversation.

Check the current default before you add. The MCP adapter forwards `--use` only
for `use=true`, and CLI add defaults to true, so a false or omitted MCP `use`
can change the default. To keep the default, run
`devsy provider add <source> --use=false` in the CLI. That installs the provider
without initializing it. Options given to add do not carry over to init.

To use the provider, find the installed name and run
`devsy provider init <configured-name>` with the required repeatable
`--option KEY=VALUE` values. Init does not change the default. Then check the
status and the default with `provider_list` before you create a workspace. If
the user asked only for installation, report that init is still needed.

After an MCP add, check the default and report any change. Do not turn a request
for one workspace into a default change.

Use `provider_use` only when the user asks to change the default. It affects
later creates that omit a provider and does not move existing workspaces.

## Remove or recover

Deleting a provider changes shared configuration and does not repair a
workspace. Confirm the target and intent, and check dependent workspaces. The
delete fails if workspaces use the provider. Do not force-remove the provider or
delete its workspaces to get past that. When a provider fails, diagnose its
configuration, connectivity, or runtime. Do not delete or replace it.
