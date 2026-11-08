#!/usr/bin/env bash
# tuistory end-to-end check that a read-only key cannot create a workspace
# through `devsy workspace create` (VAL-CLI-009).
#
# The CLI logs in through tuistory, then the stored key is replaced with a key
# created through the gateway with scopes [read]. Every devsy command runs as a
# real terminal session. Evidence is written to $EVIDENCE_DIR.
#
# Requires: tuistory, curl, jq, kubectl, a devsy binary, and a running gateway.
#
#   GATEWAY          gateway URL                   (default http://localhost:8180)
#   DEVSY_BIN        devsy binary                  (default dist/devsy-e2e)
#   TEMPLATE         WorkspaceTemplate name        (default smoke-template)
#   KUBECONFIG_FILE  kubeconfig of the k3d cluster (required)
#   KUBE_CONTEXT     kubectl context               (default k3d-devsy)
#   WS_NAMESPACE     namespace of Workspace CRs    (default default)
set -euo pipefail

GATEWAY="${GATEWAY:-http://localhost:8180}"
DEVSY_BIN="$(cd "$(dirname "${DEVSY_BIN:-dist/devsy-e2e}")" && pwd)/$(basename "${DEVSY_BIN:-dist/devsy-e2e}")"
TEMPLATE="${TEMPLATE:-smoke-template}"
KUBECONFIG_FILE="${KUBECONFIG_FILE:?set KUBECONFIG_FILE to the k3d kubeconfig}"
KUBE_CONTEXT="${KUBE_CONTEXT:-k3d-devsy}"
WS_NAMESPACE="${WS_NAMESPACE:-default}"
export PATH="$HOME/.bun/bin:$PATH"

RUN="$(date +%s)-$RANDOM"
EVIDENCE_DIR="${EVIDENCE_DIR:-$(pwd)/dist/platform-e2e/readonly-denied-$RUN}"
WORK="$(mktemp -d)"
SESSION_PREFIX="wsro-$RUN"
EMAIL="cli-ro-$RUN@example.test"
PASSWORD="pw-$RUN-$RANDOM$RANDOM"
WS_NAME="ws-ro-$RUN"
mkdir -p "$EVIDENCE_DIR"

export DEVSY_HOME="$WORK/home"
mkdir -p "$DEVSY_HOME"
CONFIG="$DEVSY_HOME/config.yaml"

fail() { echo "FAIL: $*" >&2; exit 1; }
log() { echo "[wsro] $*"; }
kc() { kubectl --kubeconfig "$KUBECONFIG_FILE" --context "$KUBE_CONTEXT" "$@"; }

cleanup() {
	local status=$?
	tuistory -s "$SESSION_PREFIX-login" close >/dev/null 2>&1 || true
	kc -n "$WS_NAMESPACE" delete workspace "$WS_NAME" --ignore-not-found --wait=false >/dev/null 2>&1 || true
	rm -rf "$WORK"
	return "$status"
}
trap cleanup EXIT

# The runner prints the exit status on its own line so a transcript shows it.
RUNNER="$WORK/run.sh"
cat >"$RUNNER" <<'EOF'
#!/bin/sh
"$@"
echo "EXIT=$?"
EOF
chmod +x "$RUNNER"

api() { curl -sS -H 'Content-Type: application/json' "$@"; }

# tui NAME TIMEOUT_MS ARGS...: run devsy in a terminal, wait for EXIT=n, save the
# transcript to $EVIDENCE_DIR/NAME.txt, and set TUI_EXIT.
tui() {
	local name="$1" timeout="$2"
	shift 2
	local session="$SESSION_PREFIX-$name"
	tuistory launch "$RUNNER $DEVSY_BIN $*" -s "$session" --cols 200 --rows 40 \
		--env "DEVSY_HOME=$DEVSY_HOME" --env NO_COLOR=1 >/dev/null
	tuistory -s "$session" wait '/EXIT=[0-9]+/' --timeout "$timeout" >/dev/null ||
		fail "$name: command did not finish"
	tuistory -s "$session" snapshot --trim >"$EVIDENCE_DIR/$name.txt"
	tuistory -s "$session" close >/dev/null 2>&1 || true
	TUI_EXIT="$(grep -Eo 'EXIT=[0-9]+' "$EVIDENCE_DIR/$name.txt" | tail -1 | cut -d= -f2)"
}

gateway_field() {
	awk -v field="$1" '/^gateway:/ {g=1; next} g && $1 == field":" {gsub(/"/, "", $2); print $2; exit}' "$CONFIG"
}

log "creating test user and org ($EMAIL)"
api -X POST "$GATEWAY/v1/auth/signup" \
	-d "$(jq -n --arg e "$EMAIL" --arg p "$PASSWORD" '{email: $e, password: $p}')" >/dev/null
TOKEN="$(api -X POST "$GATEWAY/v1/auth/login" \
	-d "$(jq -n --arg e "$EMAIL" --arg p "$PASSWORD" '{email: $e, password: $p}')" | jq -r .token)"
[ -n "$TOKEN" ] && [ "$TOKEN" != null ] || fail "could not log in through the gateway"
api -X POST "$GATEWAY/v1/orgs" -H "Authorization: Bearer $TOKEN" \
	-d "$(jq -n --arg n "Org $RUN" '{name: $n}')" >/dev/null

log "devsy login (tuistory)"
LOGIN_SESSION="$SESSION_PREFIX-login"
tuistory launch "$RUNNER $DEVSY_BIN login $GATEWAY" -s "$LOGIN_SESSION" --cols 200 --rows 40 \
	--env "DEVSY_HOME=$DEVSY_HOME" --env NO_COLOR=1 >/dev/null
tuistory -s "$LOGIN_SESSION" wait 'Email' --timeout 15000 >/dev/null
tuistory -s "$LOGIN_SESSION" type "$EMAIL" >/dev/null
tuistory -s "$LOGIN_SESSION" press enter >/dev/null
tuistory -s "$LOGIN_SESSION" wait 'Password' --timeout 15000 >/dev/null
tuistory -s "$LOGIN_SESSION" type "$PASSWORD" >/dev/null
tuistory -s "$LOGIN_SESSION" press enter >/dev/null
tuistory -s "$LOGIN_SESSION" wait '/EXIT=[0-9]+/' --timeout 30000 >/dev/null || fail "login did not finish"
tuistory -s "$LOGIN_SESSION" snapshot --trim >"$EVIDENCE_DIR/login.txt"
grep -q 'EXIT=0' "$EVIDENCE_DIR/login.txt" || fail "login failed: $(cat "$EVIDENCE_DIR/login.txt")"

ORG_ID="$(gateway_field orgId)"
[ -n "$ORG_ID" ] || fail "config has no orgId after login"

log "creating a read-only key through the gateway (scopes [read])"
RO_KEY="$(api -X POST "$GATEWAY/v1/orgs/$ORG_ID/api-keys" -H "Authorization: Bearer $TOKEN" \
	-d '{"name":"cli-readonly","scopes":["read"]}' | jq -r .key)"
[ -n "$RO_KEY" ] && [ "$RO_KEY" != null ] || fail "could not create the read-only key"

log "swapping the stored key for the read-only key"
# Rewrite in place so the file keeps its 0600 mode.
awk -v key="$RO_KEY" '
	/^gateway:/ {g=1}
	g && /^  apiKey:/ {print "  apiKey: " key; g=0; next}
	{print}
' "$CONFIG" >"$WORK/config.swapped"
cat "$WORK/config.swapped" >"$CONFIG"
[ "$(gateway_field apiKey)" = "$RO_KEY" ] || fail "stored key was not swapped"

log "the read-only key can still list"
tui list 30000 workspace list
[ "$TUI_EXIT" = 0 ] || fail "list with the read-only key exited $TUI_EXIT"

log "VAL-CLI-009: devsy workspace create $WS_NAME --template $TEMPLATE with the read-only key"
tui create 30000 workspace create "$WS_NAME" --template "$TEMPLATE"
[ -n "$TUI_EXIT" ] && [ "$TUI_EXIT" != 0 ] || fail "VAL-CLI-009: create exited '${TUI_EXIT:-?}', expected non-zero"
grep -qi 'permission' "$EVIDENCE_DIR/create.txt" ||
	fail "VAL-CLI-009: create output has no permission error: $(cat "$EVIDENCE_DIR/create.txt")"
grep -q 'lacks write scope' "$EVIDENCE_DIR/create.txt" ||
	fail "VAL-CLI-009: create output does not say the key lacks write scope"
log "create exited $TUI_EXIT with a permission error"

if kc -n "$WS_NAMESPACE" get workspace "$WS_NAME" >"$EVIDENCE_DIR/kubectl-get.txt" 2>&1; then
	fail "VAL-CLI-009: Workspace $WS_NAME exists after a refused create"
fi
grep -q NotFound "$EVIDENCE_DIR/kubectl-get.txt" ||
	fail "VAL-CLI-009: kubectl get failed without NotFound: $(cat "$EVIDENCE_DIR/kubectl-get.txt")"
cat "$EVIDENCE_DIR/kubectl-get.txt"

grep -rEq 'dvsy_[A-Za-z0-9]+_[A-Za-z0-9_-]+' "$EVIDENCE_DIR" && fail "transcript contains an API key"

log "PASS VAL-CLI-009 (evidence: $EVIDENCE_DIR)"
