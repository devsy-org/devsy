# Devsy MCP tool contract

The stdio server runs as `devsy mcp serve` in its configured Devsy context.
Discover the connected tools and their schemas, because the installed binary
can differ from this reference. Tool names below have no server prefix.

Tool failures return MCP `isError` with a `{code, message}` payload. A missing
result, or a zero `exit_code` in an error result, is not success. Lifecycle and
provider changes return `{ok, message?}`. Create and status return workspace
configuration.

## Workspace tools

| Tool | Required inputs | Optional inputs | Preconditions and next step |
| --- | --- | --- | --- |
| `workspace_list` | None | None | Lists workspaces in the server's scope. Use the returned names in later calls. |
| `workspace_status` | `name` string | None | Target must exist. Returns saved configuration. Use it for identity and create-timeout checks. Use CLI status or describe for live state. |
| `workspace_create` | `source` string | `name`, `provider`, `ide`, `devcontainer_path` strings | Choose the source and provider first. Creates and starts the workspace. Then exec if the user requested work. After a timeout, check status or list before you retry. |
| `workspace_start` | `name` string | `force` boolean (shared schema; ignored for start) | Existing target only. Resumes it, then exec or verify live state. |
| `workspace_stop` | `name` string | `force` boolean (shared schema; ignored for stop) | Existing target only. Stops compute and keeps configuration and data for resume. |
| `workspace_delete` | `name` string | `force` boolean, default false | Destructive. Confirm intent and target. List afterward to verify the configuration is gone. The list does not confirm remote cleanup after force. |
| `workspace_exec` | `name` string, `command` array of strings | `workdir` string, `env` map of strings, `id_labels` array of strings, `timeout_seconds` integer | The workspace must be running on a runtime that supports exec. Check errors, exit status, and output flags. |

`workspace_list` returns `workspaces[]`. Each item has `name` and optional
`provider`, `ide`, `source`, and `last_used` (RFC 3339 timestamp). Pass the
canonical `source` back to create. The list has no live running state.

`workspace_create.source` accepts Git URLs (`https://`, `ssh://`,
`git@host:repo`, and canonical `git:https://...`), absolute host-local paths,
and container image references. `provider` is a configured provider name from
`provider_list`. Omit it to use the context's default. `devcontainer_path` is
relative to the project. Set `ide` only when the user asks. Slow pulls, clones,
and post-create commands can outlast client deadlines while the server keeps
working. Saved configuration can appear before setup finishes.

`workspace_status` returns the saved configuration without probing container or
provider state, so success does not confirm readiness. CLI
`workspace status --result-format json` probes live state.
`workspace describe --result-format json` combines configuration with live
state.

Force-delete can remove local configuration when remote operations fail, which
leaves provider resources behind. It also overrides the remote-deletion skip for
imported workspaces.

## Exec results and limits

Use direct argv, for example `["npm", "test"]`. For shell features, use an
available shell, for example `["sh", "-c", "npm test && npm run lint"]`. A string
such as `"npm test"` is not valid `command` input. `workdir` is inside the
container. `id_labels` select the runtime container. Do not guess them.

Results contain `stdout`, `stderr`, `exit_code`, `duration_ms`, `truncated`, and
optional `timed_out`, `clamped`, `error` (`{code, message}`). Check the MCP
error flag first. A nonzero command exit differs from a transport or runtime
error. Errors can include partial streams. Keep them, but they are not complete
output or success.

| Setting | Default | Effect |
| --- | --- | --- |
| `--exec-timeout-default` | `5m` | Timeout used when the request has no positive timeout. |
| `--exec-timeout-max` | `30m` | Positive request timeouts above this are clamped. The result reports `clamped`. |
| `--exec-output-cap` | `102400` bytes | Each stream is capped separately. Output keeps the tail with a truncation marker. |
| `--mcp-max-concurrent-ops` | `8` | Shared slots for create, start, and exec. Extra requests wait. |

These are server defaults and can differ per installation. The timeout covers
execution. Client deadlines and waits for a server slot can also interrupt a
call. After a timeout or truncated output, do not rerun a command with side
effects.

## Provider tools

| Tool | Required inputs | Optional inputs | Preconditions and next step |
| --- | --- | --- | --- |
| `provider_list` | None | None | Lists provider names, the default, and status. Call it before you select or change a provider. |
| `provider_add` | `source` string | `name` string, `options` map of strings, `use` boolean | Setup must be in scope. Take options from provider metadata or docs. List afterward to verify. |
| `provider_delete` | `name` string | None | Requires clear intent. The delete fails if workspaces use the provider. Check dependent workspaces, then list to verify. |
| `provider_use` | `name` string | None | The target must be configured. Changes the default for later creates that omit `provider`. |

`provider_list` returns `providers[]`. Each item has `name` and optional
`version`, `default`, and `status`. A missing `default` means the provider is not
the default. Provider status does not show workspace health.

`provider_add.source` accepts a registry name (for example `docker` or
`kubernetes`), a GitHub release URL, or a local provider configuration file
path. Options are provider-specific strings. The MCP adapter forwards `--use`
only when `use=true`, and the CLI add defaults to true, so omitting `use` or
passing false can change the default. Check the default before and after.
To keep the old default, use CLI `provider add --use=false`, which MCP
cannot express. That path installs without initializing. Before you use the
provider, run `provider init <configured-name>` with the required
`--option KEY=VALUE` settings, then verify the provider status and default. CLI
init does not change the default.
