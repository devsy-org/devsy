# Devsy MCP tool contract

The stdio server runs as `devsy mcp serve` in its configured Devsy context.
Discover the connected tools and their schemas: the installed binary may differ
from this reference. All names below are unprefixed server tool names.

Tool failures use MCP `isError` with a classified `{code, message}` payload.
Do not treat an absent result or a zero-valued `exit_code` in an error result as
success. Lifecycle and provider mutations normally return `{ok, message?}`;
create/status return workspace configuration.

## Workspace tools

| Tool | Required inputs | Optional inputs | Preconditions and next step |
| --- | --- | --- | --- |
| `workspace_list` | None | None | Discover workspaces in the server's scope; use exact returned names for subsequent operations. |
| `workspace_status` | `name` string | None | Target must exist. Returns saved workspace configuration; use for identity and create-timeout discovery, then CLI status/describe if live state matters. |
| `workspace_create` | `source` string | `name`, `provider`, `ide`, `devcontainer_path` strings | Determine source and provider; creates and starts. Use returned workspace configuration, then exec if work is requested. Check status/list after timeout before retry. |
| `workspace_start` | `name` string | `force` boolean (shared schema; ignored for start) | Existing target only; does not intentionally create a missing workspace. Resume, then execute or verify live state. |
| `workspace_stop` | `name` string | `force` boolean (shared schema; ignored for stop) | Existing target; stops compute while retaining workspace configuration/data for resume. Verify live state if needed. |
| `workspace_delete` | `name` string | `force` boolean, default false | Destructive: verify intent/target. List afterward to verify configuration removal; that alone does not prove remote cleanup after force. |
| `workspace_exec` | `name` string, `command` array of strings | `workdir` string, `env` map of strings, `id_labels` array of strings, `timeout_seconds` integer | Workspace must be running with an execution-supported runtime. Inspect errors, exit status, and partial-output flags. |

`workspace_list` returns `workspaces[]` with `name` and optional `provider`,
`ide`, `source`, `last_used` (RFC 3339 timestamp). Source is canonical and can
be passed back to create; the list does not include live running state.

`workspace_create.source` supports Git URLs (`https://`, `ssh://`,
`git@host:repo`, and canonical `git:https://...`), absolute host-local paths,
and container image references. `provider` is a configured provider name from
`provider_list`; omission uses the active context's default. `devcontainer_path`
is relative to the project. Set `ide` only when requested. Slow pulls, clones,
and post-create commands can outlast client deadlines: server-side work may
continue. Saved configuration can become visible before setup completes.

`workspace_status` currently returns `WorkspaceConfig()` without probing
container/provider state. A successful call proves configuration lookup, not
readiness. The CLI `workspace status --result-format json` probes live state;
`workspace describe --result-format json` combines configuration with live state.

Force-delete can remove local configuration when remote operations fail,
leaving provider resources behind. It also overrides the normal remote-deletion
skip for imported workspaces; force is not merely a local cleanup switch.

## Exec results and limits

Use direct argv, e.g. `["npm", "test"]`. For shell features explicitly use an
available shell, e.g. `["sh", "-c", "npm test && npm run lint"]`. A string such
as `"npm test"` is not valid `command` input. `workdir` is inside the container;
`id_labels` select the runtime container and should not be guessed.

Results contain `stdout`, `stderr`, `exit_code`, `duration_ms`, `truncated`, and
optional `timed_out`, `clamped`, `error` (`{code, message}`). Check the MCP error
indicator first. A nonzero command exit differs from a transport/runtime error.
Errors can carry useful partial streams; retain them without treating them as
complete output or success.

| Setting | Default | Effect |
| --- | --- | --- |
| `--exec-timeout-default` | `5m` | Timeout used when the request has no positive timeout. |
| `--exec-timeout-max` | `30m` | Positive request timeouts above this are clamped; result reports `clamped`. |
| `--exec-output-cap` | `102400` bytes | Each stream is capped separately; output retains the tail with a truncation marker. |
| `--max-concurrent-ops` | `8` | Shared slots for create/start/exec; excess requests wait. |

These are configurable server defaults, not guarantees for every installation.
Timeout covers execution behavior; client deadlines and waiting for a server
slot can also interrupt a call. Neither timeout nor truncated output authorizes
a blind retry of a command with side effects.

## Provider tools

| Tool | Required inputs | Optional inputs | Preconditions and next step |
| --- | --- | --- | --- |
| `provider_list` | None | None | Discover configured provider names/default/status before selecting or mutating one. |
| `provider_add` | `source` string | `name` string, `options` map of strings, `use` boolean | Setup must be in scope; obtain provider-specific options from metadata/docs. List afterward to verify addition and default. |
| `provider_delete` | `name` string | None | Unambiguous configuration-deletion intent; may be refused if workspaces still use it. Inspect dependents, then list to verify. |
| `provider_use` | `name` string | None | Target must be configured; changes default for future creates with provider omitted. Verify with list. |

`provider_list` returns `providers[]` with `name` and optional `version`,
`default`, `status`. Missing `default` means it is not marked default.
Provider status is not proof that every workspace is healthy.

`provider_add.source` accepts a registry name (e.g. `docker`, `kubernetes`),
a GitHub release URL, or a local provider configuration file path. Its options
are provider-specific strings. The current MCP adapter only forwards `--use`
when `use=true`; the CLI add default is already true. **Omitting `use` or passing
false does not reliably preserve the old default.** Inspect the default before
and after adding; if preservation is needed, use CLI `provider add --use=false`
as an operation MCP cannot currently express. That CLI path installs without
initializing; before using the provider, run `provider init <configured-name>`
with required `--option KEY=VALUE` settings and verify provider status/default.
Initialization through CLI does not change the default.

Contracts are maintained against `cmd/mcp/tools_workspace.go`,
`tools_exec.go`, `tools_provider.go`, and `serve.go` in the Devsy repository.
