<!--
Thanks for contributing. Keep the PR title in Conventional Commits form,
e.g. `fix(ssh): handle closed tunnel` (types: see commitlint.config.mjs).
Delete any section that does not apply, but do not leave placeholders.
-->

## What and why

<!-- What does this change, and why? Lead with the problem, not the diff. -->

Fixes #
<!-- Use "Fixes #N" to auto-close, or "Refs #N". Write "none" if there is no issue. -->

## Type of change

- [ ] Bug fix
- [ ] New feature
- [ ] Refactor (no behavior change)
- [ ] Docs
- [ ] CI / build / tooling
- [ ] Breaking change (user-visible behavior, CLI flags, config, or provider contract)

## AI assistance

<!-- Using AI to write code is fine. Say how, so reviewers know where to look. -->

- [ ] No AI tools were used
- [ ] AI tools were used (fill in below)

| Field | Answer |
| --- | --- |
| Tool and model | <!-- e.g. Claude Code with claude-sonnet-4, Codex gpt-5, Copilot --> |
| Used for | <!-- e.g. wrote the implementation, wrote tests, refactored, drafted docs, debugging only --> |
| Share of change | <!-- e.g. mostly generated, partly generated, suggestions only --> |

- [ ] I read and understand every line of this change and can explain it in review.
- [ ] I ran the code myself; generated tests were checked to fail without the fix.

## Testing

Behavior changes need integration (e2e) test coverage. Unit tests alone are not enough
for anything that touches the CLI, providers, workspaces, the agent, or the desktop app.

- [ ] Added or updated integration tests under `e2e/tests/` (Ginkgo)
  - Suite / spec: <!-- e.g. e2e/tests/up, "should reuse an existing container" -->
  - Run with: <!-- e.g. mise run cli:test:e2e:focus "reuse an existing container" -->
- [ ] Added or updated unit tests
- [ ] Added or updated desktop tests (`mise run desktop:test` / `desktop:test:e2e`)
- [ ] No integration test needed, because: <!-- refactor with existing coverage, docs only, CI only. Name the existing spec that covers it. -->

How I verified it manually:

<!-- Commands run and what you observed. Paste output for bug fixes. -->

## Checklist

- [ ] `mise run cli:lint:ci` passes (and `mise run desktop:check` for desktop changes)
- [ ] `mise run cli:test` passes
- [ ] Docs under `sites/` updated for user-visible changes
- [ ] No secrets, tokens, or personal paths in code, logs, or screenshots
- [ ] I have signed the [CLA](https://github.com/devsy-org/devsy/blob/main/.github/CLA.md) (the bot will comment if not)

## Notes for reviewers

<!-- Optional: risky areas, design tradeoffs, follow-ups, screenshots for UI changes. -->
