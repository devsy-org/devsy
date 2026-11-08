#!/usr/bin/env bash
# tuistory end-to-end check of `devsy logout` (VAL-CLI-010).
#
# The CLI logs in through tuistory, then `devsy logout` runs as a real terminal
# session. The check passes when logout exits 0, api_keys.revoked_at is set for
# the stored key, the config no longer holds the key, and `devsy workspace list`
# exits non-zero with an authentication error. Evidence is written to
# $EVIDENCE_DIR.
#
# Requires: tuistory, curl, jq, docker, a devsy binary, and a running gateway.
#
#   GATEWAY      gateway URL            (default http://localhost:8180)
#   DEVSY_BIN    devsy binary           (default dist/devsy-e2e)
#   PG_CONTAINER Postgres container     (default devsy-mission-pg)
set -euo pipefail

GATEWAY="${GATEWAY:-http://localhost:8180}"
DEVSY_BIN="$(cd "$(dirname "${DEVSY_BIN:-dist/devsy-e2e}")" && pwd)/$(basename "${DEVSY_BIN:-dist/devsy-e2e}")"
PG_CONTAINER="${PG_CONTAINER:-devsy-mission-pg}"
export PATH="$HOME/.bun/bin:$PATH"

RUN="$(date +%s)-$RANDOM"
EVIDENCE_DIR="${EVIDENCE_DIR:-$(pwd)/dist/platform-e2e/logout-$RUN}"
WORK="$(mktemp -d)"
SESSION_PREFIX="logout-$RUN"
EMAIL="cli-logout-$RUN@example.test"
PASSWORD="pw-$RUN-$RANDOM$RANDOM"
mkdir -p "$EVIDENCE_DIR"

export DEVSY_HOME="$WORK/home"
mkdir -p "$DEVSY_HOME"
CONFIG="$DEVSY_HOME/config.yaml"

fail() { echo "FAIL: $*" >&2; exit 1; }
log() { echo "[logout] $*"; }
psql_one() {
	docker exec "$PG_CONTAINER" psql -U postgres -d devsy_platform -tA -c "$1"
}

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

KEY_REC_ID="$(gateway_field keyRecordId)"
STORED_KEY="$(gateway_field apiKey)"
[ -n "$KEY_REC_ID" ] && [ -n "$STORED_KEY" ] || fail "config has no key after login"

REVOKED_BEFORE="$(psql_one "select revoked_at is not null from api_keys where id = '$KEY_REC_ID'")"
[ "$REVOKED_BEFORE" = f ] || fail "key $KEY_REC_ID is not active before logout (revoked? '$REVOKED_BEFORE')"

log "the stored key can list before logout"
tui list-before 30000 workspace list
[ "$TUI_EXIT" = 0 ] || fail "list before logout exited '${TUI_EXIT:-?}'"

log "VAL-CLI-010: devsy logout"
tui logout 30000 logout
[ "$TUI_EXIT" = 0 ] || fail "VAL-CLI-010: logout exited '${TUI_EXIT:-?}': $(cat "$EVIDENCE_DIR/logout.txt")"

REVOKED_AT="$(psql_one "select revoked_at from api_keys where id = '$KEY_REC_ID'")"
echo "api_keys.revoked_at = $REVOKED_AT" | tee "$EVIDENCE_DIR/psql-revoked-at.txt"
[ -n "$REVOKED_AT" ] || fail "VAL-CLI-010: api_keys.revoked_at is not set for $KEY_REC_ID"

[ "$(stat -f '%Lp' "$CONFIG" 2>/dev/null || stat -c '%a' "$CONFIG")" = 600 ] ||
	fail "config mode is not 600 after logout"
grep -qF "$STORED_KEY" "$CONFIG" && fail "VAL-CLI-010: the key is still in the config"
[ -z "$(gateway_field apiKey)" ] || fail "VAL-CLI-010: config still has apiKey"
[ -z "$(gateway_field keyId)" ] || fail "VAL-CLI-010: config still has keyId"
[ -z "$(gateway_field keyRecordId)" ] || fail "VAL-CLI-010: config still has keyRecordId"
[ -z "$(gateway_field orgId)" ] || fail "VAL-CLI-010: config still has orgId"
log "config holds no key, key ID, or org"

log "the revoked key is refused by the gateway"
CODE="$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $STORED_KEY" "$GATEWAY/v1/workspaces")"
[ "$CODE" = 401 ] || fail "VAL-CLI-010: the revoked key got HTTP $CODE, expected 401"

log "VAL-CLI-010: devsy workspace list after logout"
tui list-after 30000 workspace list
[ -n "$TUI_EXIT" ] && [ "$TUI_EXIT" != 0 ] ||
	fail "VAL-CLI-010: list after logout exited '${TUI_EXIT:-?}', expected non-zero"
grep -qi 'authentication' "$EVIDENCE_DIR/list-after.txt" ||
	fail "VAL-CLI-010: list output has no authentication error: $(cat "$EVIDENCE_DIR/list-after.txt")"

log "logging out again is harmless"
tui logout-again 30000 logout
[ "$TUI_EXIT" = 0 ] || fail "second logout exited '${TUI_EXIT:-?}'"

grep -rEq 'dvsy_[A-Za-z0-9]+_[A-Za-z0-9_-]+' "$EVIDENCE_DIR" && fail "transcript contains an API key"
grep -rqF "$STORED_KEY" "$EVIDENCE_DIR" && fail "evidence contains the stored key"
grep -rqF "$PASSWORD" "$EVIDENCE_DIR" "$DEVSY_HOME" && fail "password found in evidence or config"

log "PASS VAL-CLI-010 (evidence: $EVIDENCE_DIR)"
