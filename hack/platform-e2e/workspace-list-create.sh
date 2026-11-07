#!/usr/bin/env bash
# tuistory end-to-end check of `devsy workspace list` and `devsy workspace create`
# against a live platform gateway (VAL-CLI-005, VAL-CLI-006).
#
# Every devsy command runs as a real terminal session driven by tuistory. The
# CLI never polls; this script does the waiting.
#
# Requires: tuistory, curl, jq, a devsy binary, and a gateway whose controller can
# run the template named by TEMPLATE. Evidence is written to $EVIDENCE_DIR.
#
#   GATEWAY       gateway URL                (default http://localhost:8180)
#   DEVSY_BIN     devsy binary               (default dist/devsy-e2e)
#   TEMPLATE      WorkspaceTemplate name     (default smoke-template)
#   RUNNING_WAIT  seconds to reach Running   (default 120)
set -euo pipefail

GATEWAY="${GATEWAY:-http://localhost:8180}"
DEVSY_BIN="$(cd "$(dirname "${DEVSY_BIN:-dist/devsy-e2e}")" && pwd)/$(basename "${DEVSY_BIN:-dist/devsy-e2e}")"
TEMPLATE="${TEMPLATE:-smoke-template}"
RUNNING_WAIT="${RUNNING_WAIT:-120}"
export PATH="$HOME/.bun/bin:$PATH"

RUN="$(date +%s)-$RANDOM"
EVIDENCE_DIR="${EVIDENCE_DIR:-$(pwd)/dist/platform-e2e/$RUN}"
WORK="$(mktemp -d)"
SESSION_PREFIX="wslc-$RUN"
EMAIL="cli-$RUN@example.test"
PASSWORD="pw-$RUN-$RANDOM$RANDOM"
WS_NAME="ws-cli-$RUN"
WS_NAME_2="ws-cli2-$RUN"
mkdir -p "$EVIDENCE_DIR"

export DEVSY_HOME="$WORK/home"
mkdir -p "$DEVSY_HOME"

fail() { echo "FAIL: $*" >&2; exit 1; }
log() { echo "[wslc] $*"; }

cleanup() {
	local status=$?
	tuistory -s "$SESSION_PREFIX-login" close >/dev/null 2>&1 || true
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

# list_pairs FILE: print "name phase" lines from a plain list transcript.
list_pairs() {
	awk 'NF == 2 && $1 != "NAME" && $1 !~ /^EXIT=/ {print $1, $2}' "$1" | sort
}

stored_key() {
	awk '/^gateway:/ {g=1; next} g && /^  apiKey:/ {gsub(/"/, "", $2); print $2; exit}' \
		"$DEVSY_HOME/config.yaml"
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

# VAL-CLI-006: create exits 0, then the list shows Running within the deadline.
log "VAL-CLI-006: devsy workspace create $WS_NAME --template $TEMPLATE"
tui create 30000 workspace create "$WS_NAME" --template "$TEMPLATE"
[ "$TUI_EXIT" = 0 ] || fail "VAL-CLI-006: create exited $TUI_EXIT"

# wait_running NAME LABEL: poll the CLI list until NAME is Running or the deadline passes.
wait_running() {
	local name="$1" label="$2" attempt=0 deadline=$((SECONDS + RUNNING_WAIT))
	while :; do
		attempt=$((attempt + 1))
		tui "$label-$attempt" 30000 workspace list
		[ "$TUI_EXIT" = 0 ] || fail "VAL-CLI-006: list exited $TUI_EXIT"
		if list_pairs "$EVIDENCE_DIR/$label-$attempt.txt" | grep -qx "$name Running"; then
			break
		fi
		[ "$SECONDS" -lt "$deadline" ] ||
			fail "VAL-CLI-006: $name not Running within ${RUNNING_WAIT}s"
		sleep 2
	done
	log "VAL-CLI-006: $name is Running after $((RUNNING_WAIT - (deadline - SECONDS)))s"
}

wait_running "$WS_NAME" list-wait

# A second workspace makes the VAL-CLI-005 comparison cover more than one row.
log "devsy workspace create $WS_NAME_2 --template $TEMPLATE"
tui create-2 30000 workspace create "$WS_NAME_2" --template "$TEMPLATE"
[ "$TUI_EXIT" = 0 ] || fail "VAL-CLI-006: second create exited $TUI_EXIT"
wait_running "$WS_NAME_2" list-wait-2nd

# VAL-CLI-005: the CLI rows are exactly the gateway's (name, phase) pairs.
log "VAL-CLI-005: compare devsy workspace list with GET /v1/workspaces"
KEY="$(stored_key)"
[ -n "$KEY" ] || fail "no API key in the devsy config"
tui list 30000 workspace list
[ "$TUI_EXIT" = 0 ] || fail "VAL-CLI-005: list exited $TUI_EXIT"
api -H "Authorization: Bearer $KEY" "$GATEWAY/v1/workspaces" >"$EVIDENCE_DIR/gateway-workspaces.json"
jq -r '.[] | "\(.name) \(.phase)"' "$EVIDENCE_DIR/gateway-workspaces.json" | sort >"$WORK/gateway.pairs"
list_pairs "$EVIDENCE_DIR/list.txt" >"$WORK/cli.pairs"
cp "$WORK/gateway.pairs" "$EVIDENCE_DIR/gateway.pairs"
cp "$WORK/cli.pairs" "$EVIDENCE_DIR/cli.pairs"
[ "$(wc -l <"$WORK/gateway.pairs")" -ge 2 ] || fail "VAL-CLI-005: expected at least two workspaces"
diff "$WORK/gateway.pairs" "$WORK/cli.pairs" || fail "VAL-CLI-005: CLI rows differ from the gateway"

for name in "$WS_NAME" "$WS_NAME_2"; do
	api -X DELETE -H "Authorization: Bearer $KEY" "$GATEWAY/v1/workspaces/$name" >/dev/null || true
done

# A key the gateway no longer accepts gives an authentication error that names devsy login.
log "401: revoke the stored key, then devsy workspace list must fail and suggest devsy login"
KEY_RECORD_ID="$(awk '/^gateway:/ {g=1; next} g && /^  keyRecordId:/ {gsub(/"/, "", $2); print $2; exit}' "$DEVSY_HOME/config.yaml")"
ORG_ID="$(awk '/^gateway:/ {g=1; next} g && /^  orgId:/ {gsub(/"/, "", $2); print $2; exit}' "$DEVSY_HOME/config.yaml")"
code="$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE -H "Authorization: Bearer $TOKEN" \
	"$GATEWAY/v1/orgs/$ORG_ID/api-keys/$KEY_RECORD_ID")"
[ "$code" = 204 ] || fail "could not revoke the key (status $code)"
tui list-revoked 30000 workspace list
[ "$TUI_EXIT" != 0 ] || fail "list succeeded with a revoked key"
grep -q "devsy login" "$EVIDENCE_DIR/list-revoked.txt" || fail "401 error does not mention devsy login"

grep -rEq 'dvsy_[A-Za-z0-9]+_[A-Za-z0-9_-]+' "$EVIDENCE_DIR" && fail "transcript contains an API key"

log "PASS VAL-CLI-005 VAL-CLI-006 (evidence: $EVIDENCE_DIR)"
