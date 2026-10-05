---
name: devsy
description: Operate Devsy workspaces and providers for end users. Use to discover, create, resume, stop, delete, troubleshoot, or run commands in Devsy workspaces, and configure providers. Not for developing Devsy itself. Prefer Devsy MCP tools and use the CLI for additional operations or when MCP is unavailable.
license: MPL-2.0
compatibility: Requires Devsy installed on the controlling host and access to Devsy MCP via devsy mcp serve or shell access to the devsy CLI. Workspace execution also requires a supported container runtime and a running workspace.
metadata:
  author: devsy-org
  product: devsy
---

# Devsy

Operate Devsy workspaces and providers on behalf of an end user. This skill is
not the contributor guide for changing Devsy itself; for source-development
tasks follow the repository's `AGENTS.md` and normal development instructions.

## Choose the interface

Use the available Devsy MCP tool when it supports the requested operation.
Client tool names may have a server prefix; discover the actual tools rather
than assuming they are connected. Use the Devsy CLI for capabilities MCP does
not expose, or as fallback when MCP is unavailable and shell access exists.
If neither interface is available, report the missing prerequisite and link to
[Devsy MCP setup](https://devsy.sh/docs/developing-in-workspaces/mcp-server).
Installing this skill does not install Devsy or configure MCP.

Keep the same Devsy context, provider, and owner scope when switching interfaces.
Do not silently replace Devsy operations with direct Docker, Kubernetes, or
cloud mutations. Devsy owns workspace state; low-level provider diagnosis needs
appropriate user authority and a reason the Devsy diagnostics are insufficient.

## Discover and select a workspace

- When identity is uncertain, call `workspace_list` and use a returned name.
  Match an explicit user name first, then source/project when unambiguous.
- Reuse an existing suitable workspace when consistent with the request.
  An explicit request for another isolated workspace should get a distinct name.
- Inspect saved configuration with `workspace_status`. Its current result is
  configuration, **not live running state**. When running state matters, use
  `devsy workspace status <name> --result-format json` or `workspace describe`
  through the CLI. Existence does not imply the container is running.
- Resolve multiple matching candidates before mutating one.

## Create and resume

Before creating, list workspaces, determine the source, and consider reuse.
`workspace_create` accepts a Git URL, absolute host-local path, container image,
or canonical source returned by `workspace_list`. A local path belongs to the
host running Devsy, not necessarily the agent's remote filesystem.

If provider selection matters, call `provider_list` and use the configured name.
Otherwise allow the context's default provider. Add a provider only when setup
is part of the user's request; do not change the global default just to select
one provider for one workspace. Creation starts the workspace. Use its returned
configuration and identity for subsequent work. Use `workspace_start` to resume
an existing workspace, after discovery where needed.

### Create timeout recovery

A client timeout does not prove server-side creation stopped. **Do not blindly
retry creation, switch to CLI creation, or pick a second name.** Check
`workspace_status` for the expected name, or `workspace_list` if the name is
uncertain. A newly visible configuration does not by itself prove setup finished;
check live state and operation diagnostics when needed. Use bounded checks
appropriate to the active task. A single not-found result can race creation;
retry only after evidence establishes that the original operation failed or
ended without creating the workspace. Report an unresolved outcome instead of
creating duplicate infrastructure.

## Execute commands

Ensure the workspace is started. `workspace_exec.command` is an **argv array**:

```json
{"name":"my-workspace","command":["npm","test"]}
```

Use a shell only for shell features such as pipes, redirection, expansion, or
chaining, and only when that shell exists in the workspace:

```json
{"name":"my-workspace","command":["sh","-c","npm test && npm run lint"]}
```

Separate direct argv calls are also suitable for sequential commands. Determine
test/build commands from the project or user request; do not guess a package
manager. `workdir` is a container path and `env` maps strings to strings.

Check tool errors and the optional `error` payload as well as `exit_code`,
`stdout`, and `stderr`. `timed_out` means an execution timeout, not a normal
process exit; `truncated` means captured output is incomplete; `clamped` means
the requested timeout was shortened. Do not automatically rerun a command with
side effects after timeout or incomplete output. Defaults are 5 minutes per
exec, a 30-minute maximum, 100 KiB per output stream, and 8 concurrent
create/start/exec operations per server; server flags can change these limits.

## Stop, delete, and configure providers

Stopping compute uses `workspace_stop`; deletion removes workspace resources.
Do not interpret restart, refresh, fix, or vague cleanup as deletion authority.
For deletion or reset, establish unambiguous intent and target. Explicit deletion
can proceed within that authority without repeated confirmation. Do not default
to `force=true`: forced cleanup can leave provider resources behind and, for
imported workspaces, can also enable remote deletion otherwise skipped.

Provider deletion affects shared configuration and may be refused while
workspaces use it. Do not delete a provider to repair one workspace. Treat
`provider_use` as a default change for subsequent creates, not a per-workspace
selection. Commands executed inside a workspace can themselves be destructive;
assess their effects, not just the tool name.

## References

Read only the reference relevant to the task:

- [MCP tool contract](references/mcp-tools.md) — inputs, results, limits, and errors.
- [Workspace workflows](references/workspace-workflows.md) — discovery, creation, resume, execution, stop, delete, recreate/reset.
- [CLI fallback and extended capabilities](references/cli.md) — structured output, diagnostics, and CLI-only operations.
- [Provider workflows](references/providers.md) — provider selection and configuration.
- [Safety and authority](references/safety.md) — destructive actions, credentials, and repository content.
- [Troubleshooting](references/troubleshooting.md) — timeouts, partial output, and unavailable interfaces.
