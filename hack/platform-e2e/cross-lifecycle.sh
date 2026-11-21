#!/usr/bin/env bash
# tuistory end-to-end check of the full CLI lifecycle against the real platform
# (VAL-CROSS-001).
#
# Logged in as an org A user, `devsy workspace create`, `sleep`, `wake` and
# `delete` run in sequence. Each command must exit 0. After each step the script
# waits for the expected state, then reads the state three ways (kubectl phase of
# the Workspace CR, GET /v1/workspaces on the gateway, and `devsy workspace list`)
# and requires all three to agree. The table of states per step goes to
# $EVIDENCE_DIR/state-table.txt.
#
# Requires: tuistory, curl, jq, kubectl, a devsy binary, and a gateway plus
# controller running against the cluster in $KUBECONFIG_FILE. See lib.sh for the
# environment variables.
#
#   RUNNING_WAIT   seconds to reach Running      (default 120)
#   SLEEPING_WAIT  seconds to reach Sleeping     (default 60)
#   DELETE_WAIT    seconds for the CR to vanish  (default 120)
set -euo pipefail

PREFIX=xlife
# shellcheck source=lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

RUNNING_WAIT="${RUNNING_WAIT:-120}"
SLEEPING_WAIT="${SLEEPING_WAIT:-60}"
DELETE_WAIT="${DELETE_WAIT:-120}"

EMAIL="cli-xlife-$RUN@example.test"
PASSWORD="pw-$RUN-$RANDOM$RANDOM"
WS_NAME="ws-xlife-$RUN"
TABLE="$EVIDENCE_DIR/state-table.txt"
track_workspace "$WS_NAME"

printf '%-10s %-8s %-9s %-9s %-9s %s\n' STEP EXIT KUBECTL GATEWAY CLI SECONDS >"$TABLE"

# settle STEP EXIT WANT LIMIT: poll the three views until every one of them shows
# WANT ("Running", "Sleeping", or "absent"), then append a row to the state table.
# A step fails when the views never agree on WANT within LIMIT seconds.
settle() {
	local step="$1" exit_code="$2" want="$3" limit="$4" attempt=0 start=$SECONDS
	local kube gate cli
	while :; do
		attempt=$((attempt + 1))
		kube="$(kube_phase "$WS_NAME")"
		gate="$(gateway_phase "$KEY" "$WS_NAME")"
		cli="$(cli_phase "$step-list-$attempt" "$WS_NAME")"
		if [ "$kube" = "$want" ] && [ "$gate" = "$want" ] && [ "$cli" = "$want" ]; then
			break
		fi
		[ $((SECONDS - start)) -lt "$limit" ] ||
			fail "$step: states never agreed on '$want' within ${limit}s (kubectl=$kube gateway=$gate cli=$cli)"
		sleep 2
	done
	printf '%-10s %-8s %-9s %-9s %-9s %s\n' "$step" "$exit_code" "$kube" "$gate" "$cli" \
		"$((SECONDS - start))" >>"$TABLE"
	log "$step: kubectl=$kube gateway=$gate cli=$cli (agreed after $((SECONDS - start))s)"
}

log "creating org A user ($EMAIL)"
signup_user "$EMAIL" "$PASSWORD" "Org A $RUN"
log "devsy login (tuistory)"
cli_login "$EMAIL" "$PASSWORD"
KEY="$(config_gateway_field apiKey)"
[ -n "$KEY" ] || fail "config has no apiKey after login"

log "devsy workspace create $WS_NAME --template $TEMPLATE"
tui create 30000 workspace create "$WS_NAME" --template "$TEMPLATE"
[ "$TUI_EXIT" = 0 ] || fail "create exited '${TUI_EXIT:-?}'"
settle create "$TUI_EXIT" Running "$RUNNING_WAIT"

log "devsy workspace sleep $WS_NAME"
tui sleep 30000 workspace sleep "$WS_NAME"
[ "$TUI_EXIT" = 0 ] || fail "sleep exited '${TUI_EXIT:-?}'"
settle sleep "$TUI_EXIT" Sleeping "$SLEEPING_WAIT"

log "devsy workspace wake $WS_NAME"
tui wake 30000 workspace wake "$WS_NAME"
[ "$TUI_EXIT" = 0 ] || fail "wake exited '${TUI_EXIT:-?}'"
settle wake "$TUI_EXIT" Running "$RUNNING_WAIT"

log "devsy workspace delete $WS_NAME"
tui delete 30000 workspace delete "$WS_NAME"
[ "$TUI_EXIT" = 0 ] || fail "delete exited '${TUI_EXIT:-?}'"
settle delete "$TUI_EXIT" absent "$DELETE_WAIT"

no_key_in_evidence

echo
cat "$TABLE"
log "PASS VAL-CROSS-001 (evidence: $EVIDENCE_DIR)"
