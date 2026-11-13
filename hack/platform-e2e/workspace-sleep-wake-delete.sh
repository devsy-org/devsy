#!/usr/bin/env bash
# tuistory end-to-end check of `devsy workspace sleep|wake|delete` against a live
# platform gateway and controller (VAL-CLI-007, VAL-CLI-008).
#
# Every devsy command runs as a real terminal session driven by tuistory. The
# CLI never polls; this script does the waiting.
#
# Requires: tuistory, curl, jq, kubectl, a devsy binary, and a gateway plus
# controller running against the cluster in $KUBECONFIG_FILE. Evidence is written
# to $EVIDENCE_DIR.
#
#   GATEWAY          gateway URL                   (default http://localhost:8180)
#   DEVSY_BIN        devsy binary                  (default dist/devsy-e2e)
#   TEMPLATE         WorkspaceTemplate name        (default smoke-template)
#   KUBECONFIG_FILE  kubeconfig of the k3d cluster (required)
#   KUBE_CONTEXT     kubectl context               (default k3d-devsy)
#   WS_NAMESPACE     namespace of Workspace CRs    (default default)
#   RUNNING_WAIT     seconds to reach Running      (default 120)
#   SLEEPING_WAIT    seconds to reach Sleeping     (default 60)
#   DELETE_WAIT      seconds for the CR to vanish  (default 120)
set -euo pipefail

GATEWAY="${GATEWAY:-http://localhost:8180}"
DEVSY_BIN="$(cd "$(dirname "${DEVSY_BIN:-dist/devsy-e2e}")" && pwd)/$(basename "${DEVSY_BIN:-dist/devsy-e2e}")"
TEMPLATE="${TEMPLATE:-smoke-template}"
KUBECONFIG_FILE="${KUBECONFIG_FILE:?set KUBECONFIG_FILE to the k3d kubeconfig}"
KUBE_CONTEXT="${KUBE_CONTEXT:-k3d-devsy}"
WS_NAMESPACE="${WS_NAMESPACE:-default}"
RUNNING_WAIT="${RUNNING_WAIT:-120}"
SLEEPING_WAIT="${SLEEPING_WAIT:-60}"
DELETE_WAIT="${DELETE_WAIT:-120}"
export PATH="$HOME/.bun/bin:$PATH"

RUN="$(date +%s)-$RANDOM"
EVIDENCE_DIR="${EVIDENCE_DIR:-$(pwd)/dist/platform-e2e/sleep-wake-delete-$RUN}"
WORK="$(mktemp -d)"
SESSION_PREFIX="wssw-$RUN"
EMAIL="cli-sw-$RUN@example.test"
PASSWORD="pw-$RUN-$RANDOM$RANDOM"
WS_NAME="ws-sw-$RUN"
mkdir -p "$EVIDENCE_DIR"

export DEVSY_HOME="$WORK/home"
mkdir -p "$DEVSY_HOME"

fail() { echo "FAIL: $*" >&2; exit 1; }
log() { echo "[wssw] $*"; }
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

# list_pairs FILE: print "name phase" lines from a plain list transcript.
list_pairs() {
	awk 'NF == 2 && $1 != "NAME" && $1 !~ /^EXIT=/ {print $1, $2}' "$1" | sort
}

# wait_list LABEL VAR SECONDS PATTERN: poll `devsy workspace list` until the row for
# $WS_NAME matches PATTERN ("Running", "Sleeping", or "-" for absent). VAR is set to
# the seconds it took.
wait_list() {
	local label="$1" var="$2" limit="$3" want="$4" attempt=0 start=$SECONDS
	while :; do
		attempt=$((attempt + 1))
		tui "$label-$attempt" 30000 workspace list
		[ "$TUI_EXIT" = 0 ] || fail "$label: list exited $TUI_EXIT"
		local row
		row="$(list_pairs "$EVIDENCE_DIR/$label-$attempt.txt" | awk -v n="$WS_NAME" '$1 == n {print $2}')"
		if [ "$want" = "-" ]; then
			[ -z "$row" ] && break
		elif [ "$row" = "$want" ]; then
			break
		fi
		[ $((SECONDS - start)) -lt "$limit" ] ||
			fail "$label: $WS_NAME is '${row:-absent}' after ${limit}s, wanted '$want'"
		sleep 2
	done
	printf -v "$var" '%s' "$((SECONDS - start))"
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

log "devsy workspace create $WS_NAME --template $TEMPLATE"
tui create 30000 workspace create "$WS_NAME" --template "$TEMPLATE"
[ "$TUI_EXIT" = 0 ] || fail "create exited $TUI_EXIT"
wait_list running-initial took "$RUNNING_WAIT" Running
log "$WS_NAME is Running after ${took}s"

# VAL-CLI-007: sleep exits 0 and the list shows Sleeping within 60 seconds.
log "VAL-CLI-007: devsy workspace sleep $WS_NAME"
tui sleep 30000 workspace sleep "$WS_NAME"
[ "$TUI_EXIT" = 0 ] || fail "VAL-CLI-007: sleep exited $TUI_EXIT"
wait_list after-sleep took "$SLEEPING_WAIT" Sleeping
log "VAL-CLI-007: $WS_NAME is Sleeping after ${took}s (limit ${SLEEPING_WAIT}s)"

# VAL-CLI-007: wake exits 0 and the list shows Running within 120 seconds.
log "VAL-CLI-007: devsy workspace wake $WS_NAME"
tui wake 30000 workspace wake "$WS_NAME"
[ "$TUI_EXIT" = 0 ] || fail "VAL-CLI-007: wake exited $TUI_EXIT"
wait_list after-wake took "$RUNNING_WAIT" Running
log "VAL-CLI-007: $WS_NAME is Running after ${took}s (limit ${RUNNING_WAIT}s)"

# VAL-CLI-008: delete exits 0, the name leaves the list, and the CR is NotFound.
log "VAL-CLI-008: devsy workspace delete $WS_NAME"
tui delete 30000 workspace delete "$WS_NAME"
[ "$TUI_EXIT" = 0 ] || fail "VAL-CLI-008: delete exited $TUI_EXIT"
wait_list after-delete took "$DELETE_WAIT" -
log "VAL-CLI-008: $WS_NAME is gone from the list after ${took}s"

start=$SECONDS
while :; do
	if kc -n "$WS_NAMESPACE" get workspace "$WS_NAME" >"$EVIDENCE_DIR/kubectl-get.txt" 2>&1; then
		[ $((SECONDS - start)) -lt "$DELETE_WAIT" ] ||
			fail "VAL-CLI-008: Workspace $WS_NAME still exists after ${DELETE_WAIT}s"
		sleep 2
	else
		grep -q NotFound "$EVIDENCE_DIR/kubectl-get.txt" ||
			fail "VAL-CLI-008: kubectl get failed without NotFound: $(cat "$EVIDENCE_DIR/kubectl-get.txt")"
		break
	fi
done
cat "$EVIDENCE_DIR/kubectl-get.txt"

# A name that no longer exists is an error the CLI shows, with a non-zero exit.
tui delete-again 30000 workspace delete "$WS_NAME"
[ "$TUI_EXIT" != 0 ] || fail "deleting a missing workspace exited 0"
grep -q 'workspace not found' "$EVIDENCE_DIR/delete-again.txt" ||
	fail "missing-workspace error does not show the gateway message"

grep -rEq 'dvsy_[A-Za-z0-9]+_[A-Za-z0-9_-]+' "$EVIDENCE_DIR" && fail "transcript contains an API key"

log "PASS VAL-CLI-007 VAL-CLI-008 (evidence: $EVIDENCE_DIR)"
