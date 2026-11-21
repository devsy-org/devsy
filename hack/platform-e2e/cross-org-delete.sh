#!/usr/bin/env bash
# tuistory end-to-end check that a CLI session for org A cannot delete an org B
# workspace (VAL-CROSS-002).
#
# W_B is created in org B straight through the gateway. The CLI is logged in to
# org A and runs `devsy workspace delete W_B`. The command must exit non-zero,
# W_B must still exist on the cluster (and not be terminating), and org B must
# still see it through the gateway.
#
# Requires: tuistory, curl, jq, kubectl, a devsy binary, and a running gateway
# and controller. See lib.sh for the environment variables.
set -euo pipefail

PREFIX=xorg
# shellcheck source=lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

EMAIL_A="cli-xorg-a-$RUN@example.test"
EMAIL_B="cli-xorg-b-$RUN@example.test"
PASSWORD_A="pw-a-$RUN-$RANDOM$RANDOM"
PASSWORD_B="pw-b-$RUN-$RANDOM$RANDOM"
W_B="ws-xorg-b-$RUN"

log "creating org B user ($EMAIL_B) and workspace $W_B"
signup_user "$EMAIL_B" "$PASSWORD_B" "Org B $RUN"
create_org_workspace "$SESSION_TOKEN" "$ORG_ID" "$W_B"
KEY_B="$ORG_KEY"

log "creating org A user ($EMAIL_A)"
signup_user "$EMAIL_A" "$PASSWORD_A" "Org A $RUN"
log "devsy login to org A (tuistory)"
cli_login "$EMAIL_A" "$PASSWORD_A"

kc -n "$WS_NAMESPACE" get workspace "$W_B" >"$EVIDENCE_DIR/kubectl-before.txt" 2>&1 ||
	fail "$W_B does not exist before the delete: $(cat "$EVIDENCE_DIR/kubectl-before.txt")"

log "VAL-CROSS-002: devsy workspace delete $W_B as an org A user"
tui delete 30000 workspace delete "$W_B"
[ -n "$TUI_EXIT" ] && [ "$TUI_EXIT" != 0 ] ||
	fail "VAL-CROSS-002: delete of $W_B exited '${TUI_EXIT:-?}', expected non-zero"
log "delete exited $TUI_EXIT"

kc -n "$WS_NAMESPACE" get workspace "$W_B" >"$EVIDENCE_DIR/kubectl-after.txt" 2>&1 ||
	fail "VAL-CROSS-002: $W_B is gone after the refused delete: $(cat "$EVIDENCE_DIR/kubectl-after.txt")"
cat "$EVIDENCE_DIR/kubectl-after.txt"

DELETION_TS="$(kc -n "$WS_NAMESPACE" get workspace "$W_B" -o jsonpath='{.metadata.deletionTimestamp}')"
[ -z "$DELETION_TS" ] || fail "VAL-CROSS-002: $W_B is terminating (deletionTimestamp $DELETION_TS)"

# The refusal must not have changed anything org B can see.
PHASE_B="$(gateway_phase "$KEY_B" "$W_B")"
[ "$PHASE_B" != absent ] && [ "$PHASE_B" != Deleting ] ||
	fail "VAL-CROSS-002: org B sees $W_B as '$PHASE_B' after the refused delete"
log "org B still sees $W_B (phase $PHASE_B)"

no_key_in_evidence

log "PASS VAL-CROSS-002 (evidence: $EVIDENCE_DIR)"
