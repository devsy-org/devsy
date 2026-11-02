# Troubleshooting

## Workspace not found

List workspaces before you create one. Match the exact identity or source. An
outdated name does not permit a new environment. Check that MCP and CLI use the
same context, provider, and owner. After a creation timeout, the operation can
still be running.

## Configuration exists but commands cannot run

MCP `workspace_status` confirms saved configuration, not live state. Use CLI
`workspace status <name> --result-format json` or `describe` to check live
state. Resume a stopped workspace with `workspace_start` when the task needs it.
Without the CLI, `workspace_start` resumes the workspace but does not check
state.

Read runtime and command errors. A configuration lookup does not show
readiness. Check events and logs. `workspace ping` works only for
Platform workspaces. Logs do not support proxy providers. Do not default to delete,
recreate, or reset.

## Creation timed out

Check the expected identity with status, or find it with list. Creation can
continue on the server. Check live state and lifecycle events when available.
Saved configuration does not mean setup finished. Limit polling to the task. If the outcome is unknown, report it with the
identity you found.

Retry only when the original operation is known to have ended without success.
Do not use another name or CLI create instead.

## Exec failed, timed out, or was truncated

Check MCP `isError`, the optional `error`, both streams, and `exit_code`.
Separate runtime or transport problems from a nonzero project exit. `timed_out`
is not a normal process failure. `clamped` means the timeout was shortened.
Partial output does not show that the command finished. Check side effects
before you rerun a timed-out command.

`truncated` means the stream keeps only its tail. Use narrower output, the
relevant log section, or a read-only filter. Do not rerun a mutating command to
recover logs. Do not say you reviewed complete logs.

## Provider unavailable

List the configured providers and check the selected or default provider. Check
its configuration and docs. Do not guess options, remove the provider, or add a
duplicate. When you use CLI troubleshoot, read its `Errors` array and redact the
output before you share it. A successful exit can still include partial errors.

## MCP unavailable

Do not say that tool calls were made. If shell access exists, check
`devsy version` and use the CLI in the same scope. If Devsy is
missing, or the installed version lacks `mcp serve`, report that prerequisite.
MCP setup depends on the client. See
[Devsy MCP setup](https://devsy.sh/docs/developing-in-workspaces/mcp-server).
Installing the skill does not configure MCP, install Devsy, or set up a
provider.
