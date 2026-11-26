# Workspace workflows

## Find and inspect

Call `workspace_list` when the target is uncertain. Match the user's name first.
Then match the source or project, if only one fits. Several workspaces can share
a source on purpose, so do not pick one at random. Check saved configuration
with `workspace_status` and live state with
`devsy workspace status <name> --result-format json`.

## Create for a repository and run work

1. List existing workspaces. Reuse one that fits the intent.
2. Identify the source. If the provider matters, list providers and use an exact
   configured name, or keep the default.
3. If no workspace fits, or the user asked for another environment, create one
   with `workspace_create`. Otherwise resume the selected workspace. For an extra
   isolated environment from the same source, use a distinct explicit name. A
   host-local source path must be reachable from the host running Devsy.
4. If the user requested work, run it with argv after creation or resume
   succeeds. Find the project's build and test commands.
5. Report the workspace identity, the command result, and any incomplete result.

## Resume and execute

Find the target, then resume it with `workspace_start`. If the CLI is
available, check live state first, because MCP status cannot show whether the
workspace is stopped. Without the CLI, do not say that live state was checked.

Run direct argv commands. Check stderr and the exit status. Report truncation and
timeouts separately.

## Stop compute

Use `workspace_stop`. It keeps the workspace configuration and data for resume.
To verify, check live state with the CLI. Do not delete a workspace to stop it.

## Delete

Confirm the deletion intent and the exact target. Use normal deletion by default.
Use force only when the user asks for it and is authorized. Do not infer
deletion from vague cleanup or repair. Check with list that the configuration is
gone, and investigate any incomplete cleanup the provider reports. Force needs a
specific reason, including for remote deletion of imported resources.

## Recover after a create timeout

Do not retry create through MCP or CLI right away. Check the expected name with
`workspace_status`, or use list and source matching if the name is uncertain.
Saved configuration can appear while setup still runs. Check live state and
recent operation events. Limit polling to the active task. One not-found
response does not show that a create failed.

Retry only when the first operation has ended without success. If the outcome is
unknown, report that and the identity you found. Do not create a second
workspace.

## Recreate or reset after an explicit request

These operations use the CLI only:

```sh
devsy workspace up my-workspace --recreate --ide-launch skip
devsy workspace up my-workspace --reset --ide-launch skip
```

Recreate reapplies devcontainer or build changes. Only project-path or mounted
volume data survives. Recreate is rejected for existing-container workspaces.
Reset rebuilds from a clean source state (a fresh Git or local-folder source)
and discards workspace changes.

Neither is a default troubleshooting step. Confirm the target and the data loss
first. Do not promise backups.
