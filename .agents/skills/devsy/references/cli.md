# CLI fallback and extended capabilities

Use the equivalent MCP tool first. Use the CLI when MCP is unavailable or cannot
express the behavior. If the installed version differs from this reference, run
`devsy version` and check the command `--help`. When you switch from MCP, pass
the matching global flags so the context, provider, and owner stay the same.

## Fallback mappings

| MCP tool | CLI |
| --- | --- |
| `workspace_list` | `devsy workspace list --result-format json` |
| `workspace_status` | `devsy workspace describe <name> --result-format json` (also probes live state) |
| `workspace_create` | `devsy workspace up <source> --ide-launch skip` |
| `workspace_start` | `devsy workspace up <name> --ide-launch skip` (verify the name exists first) |
| `workspace_stop` | `devsy workspace stop <name>` |
| `workspace_delete` | `devsy workspace delete <name>` |
| `workspace_exec` | `devsy workspace exec <name> -- <cmd> [args...]` |
| `provider_list` | `devsy provider list --result-format json` |
| `provider_add` | `devsy provider add <source>` |
| `provider_use` | `devsy provider use <name>` |
| `provider_delete` | `devsy provider delete <name>` |

`workspace up` creates or resumes a workspace. An unknown name becomes a
source, so never use it as a lookup. Pass `--id <name>` to name a new workspace
and `--provider <configured-name>` to select its provider. `--ide-launch skip`
keeps the host editor or browser closed. Omit it when the user asks for an IDE.

Prefer JSON output when the command supports it:

```sh
devsy workspace list --result-format json
devsy workspace status my-workspace --result-format json
devsy workspace describe my-workspace --result-format json
devsy workspace events my-workspace --limit 20 --result-format json
```

CLI `status` returns live `state`. `describe` adds live state to the full
configuration. MCP `workspace_status` returns configuration only. Do not scrape
tables when JSON exists. Exec stdout and stderr are command streams. Adding
`--result-format json` does not give them MCP's bounded exec response or
timeout limits.

## Diagnostics

- `devsy workspace events <name> --limit 20 --result-format json`: recent
  lifecycle journal for pending or failed creation, start, and stop.
- `devsy workspace logs <name>`: agent logs. Proxy providers do not support
  them. Set a host timeout.
- `devsy workspace ping <name>`: hidden command for Devsy Platform workspaces
  only. It does not check container health.
- `devsy workspace troubleshoot <name>`: hidden command. It prints JSON with
  config, provider, and workspace information, plus an optional `Errors` array.
  Read the `Errors` array even when the process exits with success. Redact
  sensitive output before you share it.

## Additional operations

Use these when the user asks or the task needs them. Check `--help` for flags.

| Command | Purpose and constraint |
| --- | --- |
| `devsy workspace build <name-or-source>` | Build or prebuild the workspace environment. Flags and configuration decide whether it publishes. Check help and the requested destination first. |
| `devsy workspace rename <current-name> <new-name>` | Change the workspace name. Refresh discovery afterward. |
| `devsy workspace set-ide <name> <ide>` | Change the configured IDE. |
| `devsy workspace export <name>` | Hidden command. Exports configuration JSON, not a data backup. |
| `devsy workspace import --data '<exported-json>'` | Hidden command. Imports configuration. Check identities and provider references first. |
| `devsy workspace task list` | List background tasks, such as `up --detach` work. |
| `devsy workspace task get <task-id>` | Show a task's status. |
| `devsy workspace task logs <task-id> --follow` | Follow a detached operation. Bound the wait on the host. |
| `devsy workspace task cancel <task-id>` | Cancel a task process. Confirm intent first. |
| `devsy workspace task rm <task-id>` | Delete finished task state, not the workspace. |
| `devsy workspace ssh <name>` | Interactive SSH session. For one-shot automation, use MCP exec or CLI exec. |
| `devsy workspace ssh <name> --command 'command'` | Run a non-interactive SSH command. |

Recreate and reset use `workspace up <name> --recreate` and
`workspace up <name> --reset`. Recreate loses container changes outside project
paths and mounts. Reset discards workspace source changes. Confirm intent and
target first. Neither is routine recovery.

## Providers and errors

`provider add` makes the new provider the default unless you pass `--use=false`.
With `--use=false`, the provider is installed but not initialized. Before you
use it, run `devsy provider init <configured-name>` with the required repeatable
`--option KEY=VALUE` settings. Init does not change the default. Check the
provider status and the default before you create a workspace. If the user asked
only for installation, report that initialization is pending. Take option values
from provider docs or metadata.

Keep the process exit status and any Devsy error code and hint. Separate project
command failures from Devsy runtime or transport failures. Do not hide
stderr, log credentials, or rerun a mutating command after an uncertain
result.
