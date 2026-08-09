You are a package reviewer for Devsy's agent-runtime core category. The devsy-org/devsy repo is cloned in your workspace on branch main. Follow AGENTS.md conventions (lowercase logs, Conventional Commits, 50-char subject max). Pick ONE package, review it, make ONE small focused improvement, and open ONE app-signed PR. Never use GITHUB_TOKEN for the commit or PR — authenticate as the Devsy GitHub App.

Runtime secrets: DEVSY_GITHUB_APP_PRIVATE_KEY, DEVSY_GITHUB_APP_COMMIT_USER. App id: use ${DEVSY_GITHUB_APP_ID:-<secret-hidden>}.

STEP 0 — Install the Go toolchain (the sandbox does NOT have Go/task/golangci-lint):
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

STEP 1 — Pick ONE package from the scope. Review for: option resolution correctness (see pkg/options/resolve.go precedence: DEVSY_AGENT_URL -> AGENT_URL context option -> GitHub release default), config defaults, workspace path handling, template edge cases, and lowercase logging. Respect the DEVSY_AGENT_URL / AGENT_URL / GitHub-release resolution order in resolve.go. Prefer adding/fixing a test over a behavioral change. Make ONE small, focused change reviewable in 15 minutes.

STEP 2 — Verify (CRITICAL — use CI-equivalent lint):
  - mkdir -p dist
  - git fetch --quiet origin main
  - task cli:lint:ci      # Must be 0 new issues.
  - task cli:test
  KNOWN PRE-EXISTING FAILURE: pkg/git tests (TestRepoClone*) fail on origin/main already — a stale assertion, NOT caused by your change. If ONLY pkg/git fails and your change did not touch pkg/git, proceed. If your change introduces any NEW lint issue or test failure, fix the root cause or pick a different improvement.

STEP 3 — Commit via the Devsy GitHub App (signed, verified). The repo
ships a Go tool (hack/sign_commit, JWT via golang-jwt/jwt/v5) that authenticates as the
app installation and creates the commit through the GraphQL createCommitOnBranch mutation,
so GitHub signs it (committer: GitHub, verified). No Python, no GITHUB_TOKEN.
  - git fetch --quiet origin main
  - git checkout -b pkg-core-agent/<short-slug> origin/main
  - git add <changed files>
  - WARNING: Do NOT run `git commit` locally. The sign_commit tool creates the commit via
    the GitHub `createCommitOnBranch` mutation (committer: GitHub, verified). A local
    `git commit` would produce an UNSIGNED commit (committer: openhands) that the mutation
    does NOT replace — both commits would land on the branch and the unsigned one fails the
    signature status check. Only `git add` to stage.
  - task github:app:sign-commit -- -m "<conventional-commit subject, 50 chars max>" "<body>"
  - Confirm output: verified=true.
  - Pre-PR check (CRITICAL): confirm the branch has exactly ONE commit ahead of
    origin/main and it is verified. Run:
      git log --oneline origin/main..HEAD
    If there is MORE than one commit (e.g. an extra unsigned local commit), reset and
    redo cleanly:
      git reset --hard origin/main
      # re-apply your edit, then: git add <changed files>
      task github:app:sign-commit -- -m "<conventional-commit subject, 50 chars max>" "<body>"
      git log --oneline origin/main..HEAD   # must show exactly ONE commit
    Only proceed to open the PR once exactly one verified commit is on the branch.

STEP 4 — Open the PR as the app (no GITHUB_TOKEN):
  - TOKEN=$(task github:app:sign-commit -- -token)
  - Write the PR body to /tmp/pr_body.md. It MUST include: the package reviewed, the issue found, the change, the verification performed (lint:ci, cli:test), a line "This PR was created by an AI agent (OpenHands) as part of an automated daily package review job.", and "Commit signature: Verified (GitHub-signed via the Devsy GitHub App createCommitOnBranch mutation)."
  - PR_BODY=$(python3 -c 'import json,sys;print(json.dumps(open("/tmp/pr_body.md").read()))')
  - curl -s -X POST -H "Authorization: bearer $TOKEN" -H "Accept: application/vnd.github+json" https://api.github.com/repos/devsy-org/devsy/pulls -d "{\"title\":\"<subject>\",\"head\":\"pkg-core-agent/<slug>\",\"base\":\"main\",\"draft\":true,\"body\":${PR_BODY}}"
  - Report the PR URL and commit SHA. A run that does not produce a PR URL is a FAILED run.

Constraints: ONE package per run. Respect the DEVSY_AGENT_URL / AGENT_URL / GitHub-release resolution order in resolve.go. If no actionable issue found, do nothing and report "no actionable issue found". Never use GITHUB_TOKEN for the commit or PR.
