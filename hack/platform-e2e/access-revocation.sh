#!/usr/bin/env bash
# tuistory end-to-end checks that the CLI loses access as soon as its credential
# is cut off on the platform.
#
# VAL-CROSS-003: user B joins org A and logs in with `devsy login`. The org A
# owner removes B through DELETE /v1/orgs/{org}/members/{user} (204). B's next
# `devsy workspace list` must exit non-zero with an authentication or permission
# error.
#
# VAL-CROSS-004: a second CLI session logs in with key K. The owner revokes K
# through DELETE /v1/orgs/{org}/api-keys/{id} (204). The next
# `devsy workspace list` must exit non-zero with the re-login hint.
#
# Each scenario runs `devsy workspace list` first and requires exit 0, so a
# failure afterwards cannot come from a broken setup.
#
# Requires: tuistory, curl, jq, kubectl, a devsy binary, and a running gateway.
# See lib.sh for the environment variables.
set -euo pipefail

PREFIX=xrevoke
# shellcheck source=lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

PASSWORD_OWNER="pw-owner-$RUN-$RANDOM$RANDOM"
PASSWORD_B="pw-b-$RUN-$RANDOM$RANDOM"
PASSWORD_C="pw-c-$RUN-$RANDOM$RANDOM"
EMAIL_OWNER="cli-xrevoke-owner-$RUN@example.test"
EMAIL_B="cli-xrevoke-b-$RUN@example.test"
EMAIL_C="cli-xrevoke-c-$RUN@example.test"

# expect_refused NAME LABEL: the transcript NAME must show a non-zero exit and an
# authentication or permission error that points at `devsy login` or names the
# denied permission.
expect_refused() {
	local name="$1" label="$2"
	[ -n "$TUI_EXIT" ] && [ "$TUI_EXIT" != 0 ] ||
		fail "$label: devsy workspace list exited '${TUI_EXIT:-?}', expected non-zero: $(cat "$EVIDENCE_DIR/$name.txt")"
	grep -Eqi 'authentication|permission denied' "$EVIDENCE_DIR/$name.txt" ||
		fail "$label: output has no authentication or permission error: $(cat "$EVIDENCE_DIR/$name.txt")"
	log "$label: exit $TUI_EXIT with an authentication or permission error"
}

# add_member ORG OWNER_TOKEN EMAIL: add an existing user to the org as an admin.
# Only owners and admins can mint the API key that `devsy login` creates.
add_member() {
	local code
	code="$(api -o "$EVIDENCE_DIR/add-member.json" -w '%{http_code}' -X POST "$GATEWAY/v1/orgs/$1/members" \
		-H "Authorization: Bearer $2" -d "$(jq -n --arg e "$3" '{email: $e, role: "admin"}')")"
	[ "$code" = 201 ] || fail "adding $3 to org $1 returned $code: $(cat "$EVIDENCE_DIR/add-member.json")"
}

log "creating the org A owner ($EMAIL_OWNER)"
signup_user "$EMAIL_OWNER" "$PASSWORD_OWNER" "Org A $RUN"
OWNER_TOKEN="$SESSION_TOKEN"
ORG_A="$ORG_ID"

# --- VAL-CROSS-003 ---------------------------------------------------------

log "creating user B ($EMAIL_B) and adding it to org A"
register_user "$EMAIL_B" "$PASSWORD_B"
USER_B="$USER_ID"
add_member "$ORG_A" "$OWNER_TOKEN" "$EMAIL_B"

log "devsy login as B (tuistory)"
cli_login "$EMAIL_B" "$PASSWORD_B"
mv "$EVIDENCE_DIR/login.txt" "$EVIDENCE_DIR/login-b.txt"

tui b-list-before 30000 workspace list
[ "$TUI_EXIT" = 0 ] || fail "B's list before removal exited '${TUI_EXIT:-?}': $(cat "$EVIDENCE_DIR/b-list-before.txt")"

log "VAL-CROSS-003: the owner removes B"
CODE="$(api -o /dev/null -w '%{http_code}' -X DELETE "$GATEWAY/v1/orgs/$ORG_A/members/$USER_B" \
	-H "Authorization: Bearer $OWNER_TOKEN")"
echo "DELETE /v1/orgs/$ORG_A/members/$USER_B -> $CODE" | tee "$EVIDENCE_DIR/remove-member.txt"
[ "$CODE" = 204 ] || fail "VAL-CROSS-003: removing B returned $CODE, expected 204"

tui b-list-after 30000 workspace list
expect_refused b-list-after "VAL-CROSS-003"

# --- VAL-CROSS-004 ---------------------------------------------------------

log "creating user C ($EMAIL_C) and adding it to org A"
register_user "$EMAIL_C" "$PASSWORD_C"
add_member "$ORG_A" "$OWNER_TOKEN" "$EMAIL_C"

# Start a fresh CLI home so C's key K is the only credential on disk.
export DEVSY_HOME="$WORK/home-c"
mkdir -p "$DEVSY_HOME"
CONFIG="$DEVSY_HOME/config.yaml"

log "devsy login as C with key K (tuistory)"
cli_login "$EMAIL_C" "$PASSWORD_C"
mv "$EVIDENCE_DIR/login.txt" "$EVIDENCE_DIR/login-c.txt"
KEY_REC_ID="$(config_gateway_field keyRecordId)"
STORED_KEY="$(config_gateway_field apiKey)"
[ -n "$KEY_REC_ID" ] && [ -n "$STORED_KEY" ] || fail "config has no key after C's login"

tui k-list-before 30000 workspace list
[ "$TUI_EXIT" = 0 ] || fail "list with K before revocation exited '${TUI_EXIT:-?}': $(cat "$EVIDENCE_DIR/k-list-before.txt")"

log "VAL-CROSS-004: the owner revokes K ($KEY_REC_ID)"
CODE="$(api -o /dev/null -w '%{http_code}' -X DELETE "$GATEWAY/v1/orgs/$ORG_A/api-keys/$KEY_REC_ID" \
	-H "Authorization: Bearer $OWNER_TOKEN")"
echo "DELETE /v1/orgs/$ORG_A/api-keys/$KEY_REC_ID -> $CODE" | tee "$EVIDENCE_DIR/revoke-key.txt"
[ "$CODE" = 204 ] || fail "VAL-CROSS-004: revoking K returned $CODE, expected 204"

tui k-list-after 30000 workspace list
expect_refused k-list-after "VAL-CROSS-004"
grep -q "devsy login" "$EVIDENCE_DIR/k-list-after.txt" ||
	fail "VAL-CROSS-004: output has no re-login hint: $(cat "$EVIDENCE_DIR/k-list-after.txt")"

no_key_in_evidence
grep -rqF "$STORED_KEY" "$EVIDENCE_DIR" && fail "evidence contains K"
grep -rqF -e "$PASSWORD_B" -e "$PASSWORD_C" "$EVIDENCE_DIR" && fail "a password is in the evidence"

log "PASS VAL-CROSS-003 and VAL-CROSS-004 (evidence: $EVIDENCE_DIR)"
