# Workspace workflows

## Find and inspect

Call `workspace_list` when the target is uncertain. Match the user-provided name
first, then the source/project if unambiguous. Several workspaces can share a
source intentionally; do not select a mutable target arbitrarily. Inspect saved
configuration with `workspace_status`, and live state with CLI
`devsy workspace status <name> --result-format json` when needed.

## Create for a repository and run work

1. List existing workspaces and reuse a suitable one when consistent with intent.
2. Determine source and, if selection matters, list providers. Use an exact
   configured provider name, or retain the configured default.
3. If no suitable workspace exists or another environment was requested, create
   with `workspace_create`; otherwise resume the selected workspace. Use a
   distinct explicit name for an additional isolated environment from the same
   source. A host-local source path must be accessible to the host running Devsy.
4. Use the returned configuration/identity. If work is requested, execute using
   argv after successful creation or resume. Discover the project's build/test
   commands.
5. Report the workspace identity, command outcome, and any incomplete result.

## Resume and execute

Discover/inspect an existing target; `workspace_start` resumes it. If live state
is unknown and CLI is available, check it first. MCP status alone cannot answer
whether it is stopped. When resuming is authorized and CLI is unavailable,
`workspace_start` is the available resume operation; do not claim live state
was inspected. Execute direct argv, inspect stderr and exit status, and report
truncation/timeouts separately. Starting does not imply recreating or resetting.

## Stop compute

Use `workspace_stop`. It retains workspace configuration and data for resume.
When verification matters, use CLI live status. Do not delete to shut down.

## Delete

Establish explicit deletion intent and the exact target; inspect when ambiguous.
Use normal deletion by default unless force is explicitly requested and
authorized. Do not infer deletion from vague cleanup or repair. Verify
configuration removal with list, and investigate incomplete provider cleanup if reported. Force requires a specific reason and authority
for its consequences, including remote deletion of imported resources.

## Recover after a create timeout

Do not retry create through MCP or CLI immediately. Check the expected name
with `workspace_status`; use list/source matching if the name is uncertain.
Saved configuration can appear while setup is still running. Check live state
and recent operation events when available. Bound polling to the active task;
a single not-found response is not proof that an in-flight create failed.
Only retry when the first operation has demonstrably ended without success.
If its outcome cannot be determined, report uncertainty and the discovered
identity rather than create a second workspace.

## Recreate or reset after an explicit request

These are CLI-only operations today:

```sh
devsy workspace up my-workspace --recreate --ide-launch skip
devsy workspace up my-workspace --reset --ide-launch skip
```

Recreate reapplies devcontainer/build changes. Only project-path or mounted
volume data survives; other container changes are lost. It is rejected for
existing-container workspaces. Reset rebuilds from clean source state (fresh
Git/local-folder source) and should be treated as discarding workspace changes.
Neither is a default troubleshooting step; establish target and intended data
loss before invoking. Do not promise backups or universal data preservation.
