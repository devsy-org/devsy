<!--
Use a Conventional Commits title, for example `fix(ssh): handle closed tunnel`
(types in commitlint.config.mjs). Delete sections that do not apply, and leave no placeholders.
-->

## What and why

<!-- What changes, and why? Start with the problem, not the diff. -->

Fixes #
<!-- Use "Fixes #N" to close the issue on merge, or "Refs #N". Write "none" if there is no issue. -->

## Type of change

- [ ] Bug fix
- [ ] New feature
- [ ] Refactor (no behavior change)
- [ ] Docs
- [ ] CI / build / tooling
- [ ] Breaking change (user-visible behavior, CLI flags, config, or provider contract)

## AI assistance

<!-- AI-assisted code is fine. Say how, so reviewers know where to look. -->

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

Behavior changes need integration (e2e) tests. Unit tests alone do not cover changes
to the CLI, providers, workspaces, the agent, or the desktop app.

- [ ] CLI / provider / agent change: added or updated Ginkgo integration tests under `e2e/tests/`
  - Suite / spec: <!-- e.g. e2e/tests/up, "should reuse an existing container" -->
  - Run with: <!-- e.g. mise run cli:test:e2e:focus "reuse an existing container" -->
- [ ] Desktop change: added or updated Playwright e2e tests under `desktop/e2e/`
  - Run with: <!-- e.g. mise run desktop:test:e2e -->
- [ ] Added or updated unit tests (`mise run cli:test` / `mise run desktop:test`)
- [ ] No integration test needed, because: <!-- refactor with existing coverage, docs only, CI only. Name the existing spec that covers it. -->

How I verified it:

<!-- Commands run and what you saw. Paste output for bug fixes. -->

## Checklist

- [ ] `mise run cli:lint:ci` passes (and `mise run desktop:check` for desktop changes)
- [ ] `mise run cli:test` passes
- [ ] Docs under `sites/` updated for user-visible changes
- [ ] No secrets, tokens, or personal paths in code, logs, or screenshots
- [ ] I have signed the [CLA](https://github.com/devsy-org/devsy/blob/main/.github/CLA.md) (the bot will comment if not)

## Notes for reviewers

<!-- Optional: risky areas, design tradeoffs, follow-ups, screenshots for UI changes. -->
