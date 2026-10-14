# CLI fallback and extended capabilities

Use an available equivalent MCP tool first. Use CLI when MCP is unavailable or
cannot express the requested behavior. Check `devsy version` and command `--help`
if the installed version differs from this reference. Preserve the MCP server's
context/provider/owner scope when switching, using the corresponding global
flags when set. Do not silently operate in a different context.

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

`workspace up` is create-or-resume, so a missing name can be interpreted as a
source. Never use it as a lookup. Supply `--id <name>` when intentionally naming
a new workspace and `--provider <configured-name>` when selecting its provider.
`--ide-launch skip` avoids launching the host editor/browser for automation;
respect a user request to launch an IDE instead.

Prefer JSON where supported:

```sh
devsy workspace list --result-format json
devsy workspace status my-workspace --result-format json
devsy workspace describe my-workspace --result-format json
devsy workspace events my-workspace --limit 20 --result-format json
```

CLI status returns live `state`; describe adds live state to full configuration.
Unlike these commands, MCP `workspace_status` currently returns configuration
only. Do not scrape decorative tables when JSON exists. Exec stdout/stderr are
command streams, not a JSON result object; adding `--result-format json` does
not turn them into MCP's bounded exec response or reproduce its timeout limits.

## Diagnostics

- `devsy workspace events <name> --limit 20 --result-format json`: recent
  lifecycle journal, useful for pending/failed creation and start/stop operations.
- `devsy workspace logs <name>`: agent logs; unsupported for proxy providers.
  Bound log collection with the host tool's timeout and focus on relevant errors.
- `devsy workspace ping <name>`: hidden diagnostic for **Devsy Pro workspaces
  only**, not a general container-health probe.
- `devsy workspace troubleshoot <name>`: hidden diagnostic that prints JSON
  including config/provider/workspace information and an optional `Errors`
  array. Inspect partial errors even if the process exits successfully. Review
  and redact sensitive output before sharing; it is not a guaranteed clean bill
  of health or a downloadable bundle.

## Additional operations

Use only when requested or necessary; inspect installed `--help` for optional
flags before using them.

| Command | Purpose and constraint |
| --- | --- |
| `devsy workspace build <name-or-source>` | Build/prebuild the workspace environment; publishing behavior depends on flags/configuration. Inspect help and requested destination first. |
| `devsy workspace rename <current-name> <new-name>` | Change workspace identity; refresh discovery afterward. |
| `devsy workspace set-ide <name> <ide>` | Change the configured IDE. |
| `devsy workspace export <name>` | Hidden command exporting configuration JSON, **not a data backup**. |
| `devsy workspace import --data '<exported-json>'` | Hidden configuration import; validate intended identities/provider references first. |
| `devsy workspace task list` | Discover background tasks, such as CLI `up --detach` work. |
| `devsy workspace task get <task-id>` | Inspect a task's status. |
| `devsy workspace task logs <task-id> --follow` | Follow a detached operation; bound the host wait. |
| `devsy workspace task cancel <task-id>` | Cancel a task process; mutation requires intent. |
| `devsy workspace task rm <task-id>` | Delete finished task state; not workspace deletion. |
| `devsy workspace ssh <name>` | Interactive/SSH scenarios; prefer MCP exec or CLI exec for one-shot automation. |
| `devsy workspace ssh <name> --command 'command'` | SSH-specific non-interactive command when needed for the task. |

Recreate/reset use `workspace up <name> --recreate` / `--reset`. Recreate loses
container changes outside project paths/mounts; reset discards workspace source
changes. Verify intent and target rather than using either as routine recovery.

## Providers and errors

CLI `provider add` defaults to changing the current default. Use `--use=false`
when adding without changing that default. This installs without initializing.
Before using the new provider, run `devsy provider init <configured-name>` with
required repeatable `--option KEY=VALUE` settings; pass those options to init,
not just to the uninitialized add path. Init does not change the default. Verify
provider status and the preserved default before creating a workspace. If only
installation was requested, report the pending initialization. Obtain option
values from provider docs/metadata; do not invent them.

Preserve actual process exit status and Devsy error code/hint when available.
Distinguish a project command failure from Devsy runtime/transport failure.
Do not suppress unexpected stderr, log credentials, or automatically rerun
mutating commands after an uncertain result.

Syntax is maintained against `cmd/workspace`, `cmd/workspace/up`,
`cmd/provider`, and `cmd/root.go` in the Devsy repository.
