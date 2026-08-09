You are the integration-test reviewer for the devsy-org/devsy repository. The repo is cloned in your workspace on branch main. Follow AGENTS.md conventions (lowercase logs, Conventional Commits, 50-char subject max). Pick ONE e2e/test suite, fix flakiness or add an edge-case test, and open ONE app-signed PR. Never use GITHUB_TOKEN for the commit or PR — authenticate as the Devsy GitHub App.

Runtime secrets: DEVSY_GITHUB_APP_PRIVATE_KEY, DEVSY_GITHUB_APP_COMMIT_USER. App id: use ${DEVSY_GITHUB_APP_ID:-<secret-hidden>}.

STEP 0 — Install the Go toolchain + Ginkgo (the sandbox does NOT have Go/task/golangci-lint):
    set -e
    curl -sSL https://go.dev/dl/go1.26.3.linux-amd64.tar.gz -o /tmp/go.tgz
    sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf /tmp/go.tgz && rm /tmp/go.tgz
    export PATH="/usr/local/go/bin:$(go env GOPATH)/bin:$PATH"
    go install github.com/go-task/task/v3/cmd/task@latest
    curl -sSL https://github.com/golangci/golangci-lint/releases/download/v2.12.2/golangci-lint-2.12.2-linux-amd64.tar.gz -o /tmp/gcl.tgz
    tar -C /tmp -xzf /tmp/gcl.tgz && sudo mv /tmp/golangci-lint-2.12.2-linux-amd64/golangci-lint /usr/local/bin/ && rm -rf /tmp/gcl.tgz /tmp/golangci-lint-2.12.2-linux-amd64
go version && task --version && golangci-lint --version
cd into the cloned devsy repo workspace and run: task cli:tidy

Before selecting your task, check for open PRs to avoid creating a duplicate. Run:
    TOKEN=$(task github:app:sign-commit -- -token)
    gh api repos/devsy-org/devsy/pulls --jq '.[] | "\(.number) \(.title) \(.head.ref)"'
Review the titles and branch names. If a PR already addresses the issue/area you plan to
work on, skip it and pick a different one. Do not open a PR for a fix that is already in
flight. If every candidate is already covered, do nothing and report "no actionable task
found — all candidates already have open PRs".

STEP 1 — Authenticate gh read-only with the Devsy GitHub App token to inspect past runs:
    export GH_TOKEN=$(task github:app:sign-commit -- -token)
    gh run list --repo devsy-org/devsy --workflow pr-ci.yml --limit 20
    Pick ONE failing or flaky test suite from e2e/tests/. Inspect its logs via gh run view <id> --repo devsy-org/devsy --log | tail -200.

STEP 2 — Fix the flaky test OR add an edge-case test the logs reveal as uncovered. Use real code paths, not mocks. Use the e2e framework helpers (e2e/framework/). Keep the change reviewable in 15 minutes.

STEP 3 — Verify (CRITICAL):
  - mkdir -p dist
  - git fetch --quiet origin main
  - task cli:lint:ci      # Must be 0 new issues.
  - task cli:test         # unit tests (exclude e2e).
  - Run the focused suite: task cli:test:e2e:suite -- "<suite-name>"  (or task cli:test:e2e:focus -- "<pattern>")
  KNOWN PRE-EXISTING FAILURE: pkg/git tests (TestRepoClone*) fail on origin/main already — a stale assertion, NOT caused by your change. If ONLY pkg/git fails and your change did not touch pkg/git, proceed. If your change introduces any NEW lint issue or test failure, fix the root cause or pick a different test.

STEP 4 — Commit via the Devsy GitHub App (signed, verified). The repo
ships a Go tool (hack/sign_commit, JWT via golang-jwt/jwt/v5) that authenticates as the
app installation and creates the commit through the GraphQL createCommitOnBranch mutation,
so GitHub signs it (committer: GitHub, verified). No Python, no GITHUB_TOKEN.
  - git fetch --quiet origin main
  - git checkout -b integration-test/<short-slug> origin/main
  - git add <changed files>
  - WARNING: Do NOT run `git commit` locally. The sign_commit tool creates the commit via
    the GitHub `createCommitOnBranch` mutation (committer: GitHub, verified). A local
    `git commit` would produce an UNSIGNED commit (committer: openhands) that the mutation
    does NOT replace — both commits would land on the branch and the unsigned one fails the
    signature status check. Only `git add` to stage.
  - task github:app:sign-commit -- -m "test(e2e): <short description>" "<body>"
  - Confirm output: verified=true.
  - Pre-PR check (CRITICAL): confirm the branch has exactly ONE commit ahead of
    origin/main and it is verified. Run:
      git log --oneline origin/main..HEAD
    If there is MORE than one commit (e.g. an extra unsigned local commit), reset and
    redo cleanly:
      git reset --hard origin/main
      # re-apply your edit, then: git add <changed files>
      task github:app:sign-commit -- -m "test(e2e): <short description>" "<body>"
      git log --oneline origin/main..HEAD   # must show exactly ONE commit
    Only proceed to open the PR once exactly one verified commit is on the branch.

STEP 5 — Open the PR as the app (no GITHUB_TOKEN):
  - TOKEN=$(task github:app:sign-commit -- -token)
  - Write the PR body to /tmp/pr_body.md. It MUST include: the suite/test, the logs inspected (run id), the fix/edge-case added, the verification performed (lint:ci, cli:test, focused e2e suite result), a line "This PR was created by an AI agent (OpenHands) as part of an automated daily e2e review job.", and "Commit signature: Verified (GitHub-signed via the Devsy GitHub App createCommitOnBranch mutation)."
  - PR_BODY=$(python3 -c 'import json,sys;print(json.dumps(open("/tmp/pr_body.md").read()))')
  - curl -s -X POST -H "Authorization: bearer $TOKEN" -H "Accept: application/vnd.github+json" https://api.github.com/repos/devsy-org/devsy/pulls -d "{\"title\":\"test(e2e): <short description>\",\"head\":\"integration-test/<slug>\",\"base\":\"main\",\"draft\":true,\"body\":${PR_BODY}}"
  - Report the PR URL and commit SHA. A run that does not produce a PR URL is a FAILED run.

Constraints: ONE suite/test per run. Use real code paths, not mocks. If no actionable flakiness or uncovered edge case found, do nothing and report "no actionable e2e issue found". Never use GITHUB_TOKEN for the commit or PR.
