# Troubleshooting

## Workspace not found

List before creating. Match exact identity or source; an outdated name does not
authorize a new environment. Check that MCP and CLI use the same context,
provider, and owner. For a timed-out creation, allow for an in-flight operation:
a single failed lookup does not prove nonexistence is final.

## Configuration exists but commands cannot run

MCP `workspace_status` confirms saved configuration, not live state. Use CLI
`workspace status <name> --result-format json` or describe to probe live state.
Resume a stopped workspace with `workspace_start` when the task requires it.
If CLI is unavailable, start is the available resume action, not a state probe.
Inspect runtime/command errors; do not claim a successful configuration lookup
proved readiness. Check events/logs as needed. `workspace ping` is Pro-only,
and logs do not support proxy providers. Do not default to delete/recreate/reset.

## Creation timed out

Check expected identity with status, or discover with list. Creation may continue
server-side. Inspect live state and lifecycle events when available; newly
persisted configuration does not mean post-create setup finished. Poll only
within a reasonable active-task window. If the outcome remains unknown, report
it and retain the discovered identity. Retry only when the original operation
is known to have ended without success; do not use another name or CLI create
to bypass uncertainty.

## Exec failed, timed out, or was truncated

Check MCP `isError`, optional `error`, both streams, and `exit_code`.
Distinguish runtime/transport problems from the project's nonzero exit.
`timed_out` is separate from normal process failure; `clamped` explains a reduced
request timeout. Partial output can still help, but is not proof of completion.
Assess side effects and actual state before rerunning a timed-out command.

`truncated` means the stream is incomplete (tail retention). Use narrower output,
a relevant log section, or a read-only filter rather than rerunning a mutating
command just to recover logs. Do not claim complete logs were reviewed.

## Provider unavailable

List configured providers and inspect the selected/default provider. Check its
configuration and specific documentation rather than guess options, remove it,
or add a duplicate. When using CLI troubleshoot, review its `Errors` array and
redact the output before sharing; successful process exit can still carry
partial diagnostic failures.

## MCP unavailable

Do not claim tool calls were made. If shell access exists, check `devsy version`
and use CLI within the same authorized scope. If Devsy is missing or the
installed version lacks `mcp serve`, report that prerequisite. Persistent MCP
configuration is client-specific; consult
[Devsy MCP setup](https://devsy.sh/docs/developing-in-workspaces/mcp-server)
and the client's current setup instructions. Installing the skill alone does
not configure MCP, install Devsy, or establish a provider.
