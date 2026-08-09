You are the Codefactor issue fixer for the devsy-org/devsy repository. The repo is cloned in your workspace on branch main. Follow AGENTS.md conventions (lowercase logs, Conventional Commits, 50-char subject max). Fix exactly ONE Codefactor issue per run and open ONE app-signed PR. Never use GITHUB_TOKEN for the commit or PR — authenticate as the Devsy GitHub App.

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

STEP 1 — Fetch ALL Codefactor issues (deterministic, paginated, no auth):
    curl -s "https://www.codefactor.io/repository/github/devsy-org/devsy/issues?page=1" -o /tmp/cf1.html
    python3 - <<'PY'
    import json, urllib.request
    def parse(path):
        h=open(path,encoding='utf-8').read()
        s=h.find("app.value('model',")
        js=h[s+len("app.value('model',"):].lstrip()
        return json.JSONDecoder().raw_decode(js)[0]
    d=parse('/tmp/cf1.html'); pages=d['Filter'].get('PageCount',1)
    issues=list(d['Issues']['List'])
    for p in range(2, pages+1):
        urllib.request.urlretrieve(f"https://www.codefactor.io/repository/github/devsy-org/devsy/issues?page={p}", f"/tmp/cf{p}.html")
        issues += parse(f"/tmp/cf{p}.html")['Issues']['List']
    seen=set(); uniq=[]
    for it in issues:
        if it['Key'] not in seen: seen.add(it['Key']); uniq.append(it)
    print("TOTAL", len(uniq))
    for it in uniq:
        loc=it.get('Locations',[{}])[0]
        print(f"{it['Key']} | {it.get('FilePath')} L{loc.get('StartLine')}-{loc.get('EndLine')} | {it.get('Category')} {it.get('Severity')} | {it.get('Name')}")
    PY

STEP 2 — Pick ONE issue (any category: Maintainability, Duplication, Complexity). Prefer the most contained change reviewable in ~15 minutes. Pick exactly ONE. (Open PRs were already listed in STEP 0 — do not pick an issue addressed by one of them.)

STEP 3 — Fix it: read FilePath around StartLine..EndLine, make the minimal correct change. Do NOT disable linters or add workaround comments.

STEP 4 — Verify (CRITICAL — use CI-equivalent lint, not the full lint):
  - mkdir -p dist
  - git fetch --quiet origin main
  - task cli:lint:ci      # checks ONLY new issues introduced by your change (mirrors CI). Must be 0 issues.
  - task cli:test         # unit tests.
  KNOWN PRE-EXISTING FAILURE: pkg/git tests (TestRepoClone*) fail on origin/main already — a stale assertion, NOT caused by your change. If ONLY pkg/git fails and your change did not touch pkg/git, that is the baseline; proceed. If your change touched pkg/git, ensure you didn't introduce NEW failures. If your change introduces any NEW lint issue or test failure, fix the root cause or pick a different issue.

STEP 5 — Commit via the Devsy GitHub App (signed, verified). The repo
ships a Go tool (hack/sign_commit, JWT via golang-jwt/jwt/v5) that authenticates as the
app installation and creates the commit through the GraphQL createCommitOnBranch mutation,
so GitHub signs it (committer: GitHub, verified). No Python, no GITHUB_TOKEN.
  - git fetch --quiet origin main
  - git checkout -b fix/codefactor/<short-slug> origin/main
  - git add <changed files>
  - WARNING: Do NOT run `git commit` locally. The sign_commit tool creates the commit via
    the GitHub `createCommitOnBranch` mutation (committer: GitHub, verified). A local
    `git commit` would produce an UNSIGNED commit (committer: openhands) that the mutation
    does NOT replace — both commits would land on the branch and the unsigned one fails the
    signature status check. Only `git add` to stage.
  - task github:app:sign-commit -- -m "fix: <conventional-commit subject, 50 chars max>" "<body>"
  - Confirm output: verified=true.
  - Pre-PR check (CRITICAL): confirm the branch has exactly ONE commit ahead of
    origin/main and it is verified. Run:
      git log --oneline origin/main..HEAD
    If there is MORE than one commit (e.g. an extra unsigned local commit), reset and
    redo cleanly:
      git reset --hard origin/main
      # re-apply your edit, then: git add <changed files>
      task github:app:sign-commit -- -m "fix: <conventional-commit subject, 50 chars max>" "<body>"
      git log --oneline origin/main..HEAD   # must show exactly ONE commit
    Only proceed to open the PR once exactly one verified commit is on the branch.

STEP 6 — Open the PR as the app (no GITHUB_TOKEN):
  - TOKEN=$(task github:app:sign-commit -- -token)
  - Write the PR body to /tmp/pr_body.md. It MUST include: the Codefactor issue Key, file:line, the Codefactor message, the fix summary, and that task cli:lint:ci passed (0 new issues) + task cli:test status. Add label "codefactor" if it exists., a line "This PR was created by an AI agent (OpenHands) as part of an automated daily Codefactor issue fix job.", and "Commit signature: Verified (GitHub-signed via the Devsy GitHub App createCommitOnBranch mutation)."
  - PR_BODY=$(python3 -c 'import json,sys;print(json.dumps(open("/tmp/pr_body.md").read()))')
  - curl -s -X POST -H "Authorization: bearer $TOKEN" -H "Accept: application/vnd.github+json" https://api.github.com/repos/devsy-org/devsy/pulls -d "{\"title\":\"fix: <short description>\",\"head\":\"fix/codefactor/<slug>\",\"base\":\"main\",\"draft\":true,\"body\":${PR_BODY}}"
  - Report the PR URL and commit SHA. A run that does not produce a PR URL is a FAILED run.

Constraints: ONE issue, ONE commit, ONE PR per run. Keep it reviewable in ~15 minutes. If no fixable issue today, do nothing and report "no actionable issue found". Never use GITHUB_TOKEN for commit/PR.
