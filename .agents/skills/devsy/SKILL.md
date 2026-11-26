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

Operate Devsy workspaces and providers for an end user. For changes to the
Devsy source code, follow the repository's `AGENTS.md` instead.

## Choose the interface

Use a Devsy MCP tool when it supports the operation. Client tool names can
have a server prefix, so discover the actual tools. Do not assume they are
connected.

Use the Devsy CLI for operations MCP does not expose, or when MCP is
unavailable and shell access exists. If neither interface is available, report
the missing prerequisite and link to
[Devsy MCP setup](https://devsy.sh/docs/developing-in-workspaces/mcp-server).
Installing this skill does not install Devsy or configure MCP.

Keep the same Devsy context, provider, and owner when you switch interfaces.
Do not replace Devsy operations with direct Docker, Kubernetes, or cloud
changes. Devsy owns workspace state. Low-level provider diagnosis needs the
user's authority and a reason that Devsy diagnostics are not enough.

## Discover and select a workspace

- If the identity is uncertain, call `workspace_list` and use a returned name.
  Match an explicit user name first. Then match source or project, if only one
  workspace fits.
- Reuse an existing workspace when it fits the request. A request for another
  isolated workspace gets a distinct name.
- `workspace_status` returns saved configuration, not live running state. When
  running state matters, run `devsy workspace status <name> --result-format json`
  or `workspace describe` through the CLI. A workspace can exist without its
  container running.
- Resolve multiple matching candidates before you change one.

## Create and resume

Before you create a workspace, list workspaces, identify the source, and check
whether to reuse one. `workspace_create` accepts a Git URL, an absolute
host-local path, a container image, or a canonical source from
`workspace_list`. A local path refers to the host running Devsy, not
necessarily the agent's remote filesystem.

If the provider matters, call `provider_list` and use the configured name.
Otherwise, let the context's default provider apply. Add a provider only when
setup is part of the request. Do not change the global default to pick a
provider for one workspace. Creating a workspace starts it. Use the returned
configuration and identity for later work. Use `workspace_start` to resume an
existing workspace.

### Create timeout recovery

A client timeout does not mean server-side creation stopped. Do not blindly
retry creation, switch to CLI creation, or pick a second name. Check
`workspace_status` for the expected name, or `workspace_list` if the name is
uncertain. Saved configuration does not show that setup finished, so check live
state and operation diagnostics. Use bounded checks. A single not-found result
can race with creation. Retry only after evidence shows that the original
operation failed or ended without creating the workspace. If the outcome is
unresolved, report it. Do not create duplicate infrastructure.

## Execute commands

Make sure the workspace is started. `workspace_exec.command` is an **argv array**:

```json
{"name":"my-workspace","command":["npm","test"]}
```

Use a shell only for shell features, such as pipes, redirection, expansion, or
chaining. The shell must exist in the workspace:

```json
{"name":"my-workspace","command":["sh","-c","npm test && npm run lint"]}
```

Separate direct argv calls work for sequential commands. Find test and build
commands in the project or the user's request. Do not guess the package
manager. `workdir` is a container path. `env` maps strings to strings.

Check tool errors and the optional `error` payload, then `exit_code`, `stdout`,
and `stderr`. `timed_out` means the execution timed out. `truncated` means
captured output is incomplete. `clamped` means the requested timeout was
shortened. Do not rerun a command with side effects after a timeout or
incomplete output. Defaults: 5 minutes per exec, a 30-minute maximum, 100 KiB
per output stream, and 8 concurrent create, start, or exec operations per
server. Server flags can change these limits.

## Stop, delete, and configure providers

`workspace_stop` stops compute. Deletion removes workspace resources. Do not
treat restart, refresh, fix, or vague cleanup as permission to delete. For
deletion or reset, confirm the intent and the target. Explicit deletion needs no
repeated confirmation. Do not default to `force=true`. Forced cleanup can leave
provider resources behind. For imported workspaces, it can also enable remote
deletion that ordinary deletion skips.

Provider deletion changes shared configuration, and the provider can refuse
deletion while workspaces use it. Do not delete a provider to repair one
workspace. `provider_use` changes the default for later creates, not the
provider for one workspace. Commands run inside a workspace can be destructive.
Assess what they do, not only the tool name.

## References

Read only the reference that fits the task:

- [MCP tool contract](references/mcp-tools.md): inputs, results, limits, and errors.
- [Workspace workflows](references/workspace-workflows.md): discovery, creation, resume, execution, stop, delete, recreate, and reset.
- [CLI fallback and extended capabilities](references/cli.md): structured output, diagnostics, and CLI-only operations.
- [Provider workflows](references/providers.md): provider selection and configuration.
- [Safety and authority](references/safety.md): destructive actions, credentials, and repository content.
- [Troubleshooting](references/troubleshooting.md): timeouts, partial output, and unavailable interfaces.
