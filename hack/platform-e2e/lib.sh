#!/usr/bin/env bash
# Shared helpers for the tuistory platform e2e scripts that need two orgs. Source
# this file; it does not run anything by itself.
#
# Every devsy command runs as a real terminal session driven by tuistory. The
# caller must set PREFIX (short script tag) before sourcing.
#
#   GATEWAY          gateway URL                   (default http://localhost:8180)
#   DEVSY_BIN        devsy binary                  (default dist/devsy-e2e)
#   TEMPLATE         WorkspaceTemplate name        (default smoke-template)
#   KUBECONFIG_FILE  kubeconfig of the k3d cluster (required)
#   KUBE_CONTEXT     kubectl context               (default k3d-devsy)
#   WS_NAMESPACE     namespace of Workspace CRs    (default default)
#   EVIDENCE_DIR     transcript directory          (default dist/platform-e2e/<PREFIX>-<run>)

: "${PREFIX:?set PREFIX before sourcing lib.sh}"

GATEWAY="${GATEWAY:-http://localhost:8180}"
DEVSY_BIN="$(cd "$(dirname "${DEVSY_BIN:-dist/devsy-e2e}")" && pwd)/$(basename "${DEVSY_BIN:-dist/devsy-e2e}")"
TEMPLATE="${TEMPLATE:-smoke-template}"
KUBECONFIG_FILE="${KUBECONFIG_FILE:?set KUBECONFIG_FILE to the k3d kubeconfig}"
KUBE_CONTEXT="${KUBE_CONTEXT:-k3d-devsy}"
WS_NAMESPACE="${WS_NAMESPACE:-default}"
export PATH="$HOME/.bun/bin:$PATH"

RUN="$(date +%s)-$RANDOM"
EVIDENCE_DIR="${EVIDENCE_DIR:-$(pwd)/dist/platform-e2e/$PREFIX-$RUN}"
WORK="$(mktemp -d)"
SESSION_PREFIX="$PREFIX-$RUN"
mkdir -p "$EVIDENCE_DIR"

export DEVSY_HOME="$WORK/home"
mkdir -p "$DEVSY_HOME"
CONFIG="$DEVSY_HOME/config.yaml"

fail() { echo "FAIL: $*" >&2; exit 1; }
log() { echo "[$PREFIX] $*"; }
kc() { kubectl --kubeconfig "$KUBECONFIG_FILE" --context "$KUBE_CONTEXT" "$@"; }
api() { curl -sS -H 'Content-Type: application/json' "$@"; }

# Workspaces to remove on exit; scripts append with track_workspace.
TRACKED_WORKSPACES=()
track_workspace() { TRACKED_WORKSPACES+=("$1"); }

cleanup() {
	local status=$?
	tuistory -s "$SESSION_PREFIX-login" close >/dev/null 2>&1 || true
	local ws
	for ws in ${TRACKED_WORKSPACES[@]+"${TRACKED_WORKSPACES[@]}"}; do
		kc -n "$WS_NAMESPACE" delete workspace "$ws" --ignore-not-found --wait=false >/dev/null 2>&1 || true
	done
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

# register_user EMAIL PASSWORD: sign up and log in through the gateway, and set
# USER_ID and SESSION_TOKEN. The user belongs to no org yet.
register_user() {
	local email="$1" password="$2"
	USER_ID="$(api -X POST "$GATEWAY/v1/auth/signup" \
		-d "$(jq -n --arg e "$email" --arg p "$password" '{email: $e, password: $p}')" | jq -r .id)"
	[ -n "$USER_ID" ] && [ "$USER_ID" != null ] || fail "could not sign up $email"
	SESSION_TOKEN="$(api -X POST "$GATEWAY/v1/auth/login" \
		-d "$(jq -n --arg e "$email" --arg p "$password" '{email: $e, password: $p}')" | jq -r .token)"
	[ -n "$SESSION_TOKEN" ] && [ "$SESSION_TOKEN" != null ] || fail "could not log in $email through the gateway"
}

# signup_user EMAIL PASSWORD ORG_NAME: register a user, create an org, and set
# USER_ID, SESSION_TOKEN and ORG_ID.
signup_user() {
	local email="$1" password="$2" org_name="$3"
	register_user "$email" "$password"
	ORG_ID="$(api -X POST "$GATEWAY/v1/orgs" -H "Authorization: Bearer $SESSION_TOKEN" \
		-d "$(jq -n --arg n "$org_name" '{name: $n}')" | jq -r .id)"
	[ -n "$ORG_ID" ] && [ "$ORG_ID" != null ] || fail "could not create org $org_name"
}

# cli_login EMAIL PASSWORD: devsy login against the gateway in a tuistory session.
cli_login() {
	local email="$1" password="$2"
	local session="$SESSION_PREFIX-login"
	tuistory launch "$RUNNER $DEVSY_BIN login $GATEWAY" -s "$session" --cols 200 --rows 40 \
		--env "DEVSY_HOME=$DEVSY_HOME" --env NO_COLOR=1 >/dev/null
	tuistory -s "$session" wait 'Email' --timeout 15000 >/dev/null
	tuistory -s "$session" type "$email" >/dev/null
	tuistory -s "$session" press enter >/dev/null
	tuistory -s "$session" wait 'Password' --timeout 15000 >/dev/null
	tuistory -s "$session" type "$password" >/dev/null
	tuistory -s "$session" press enter >/dev/null
	tuistory -s "$session" wait '/EXIT=[0-9]+/' --timeout 30000 >/dev/null || fail "login did not finish"
	tuistory -s "$session" snapshot --trim >"$EVIDENCE_DIR/login.txt"
	tuistory -s "$session" close >/dev/null 2>&1 || true
	grep -q 'EXIT=0' "$EVIDENCE_DIR/login.txt" || fail "login failed: $(cat "$EVIDENCE_DIR/login.txt")"
}

# config_gateway_field FIELD: read a field of the gateway block of the devsy config.
config_gateway_field() {
	awk -v field="$1" '/^gateway:/ {g=1; next} g && $1 == field":" {gsub(/"/, "", $2); print $2; exit}' "$CONFIG"
}

# kube_phase NAME: the Workspace CR phase, or "absent" when the CR is NotFound.
kube_phase() {
	local out
	if out="$(kc -n "$WS_NAMESPACE" get workspace "$1" -o jsonpath='{.status.phase}' 2>&1)"; then
		echo "${out:-Pending}"
	elif grep -q NotFound <<<"$out"; then
		echo absent
	else
		fail "kubectl get workspace $1: $out"
	fi
}

# gateway_phase KEY NAME: the phase GET /v1/workspaces shows for NAME, or "absent".
gateway_phase() {
	local body phase
	body="$(curl -sS -H "Authorization: Bearer $1" "$GATEWAY/v1/workspaces")"
	phase="$(jq -r --arg n "$2" '[.[] | select(.name == $n) | .phase][0] // "absent"' <<<"$body")" ||
		fail "GET /v1/workspaces returned a non-list body"
	echo "$phase"
}

# cli_phase LABEL NAME: the phase `devsy workspace list` shows for NAME, or "absent".
cli_phase() {
	tui "$1" 30000 workspace list
	[ "$TUI_EXIT" = 0 ] || fail "$1: workspace list exited $TUI_EXIT"
	list_pairs "$EVIDENCE_DIR/$1.txt" | awk -v n="$2" '$1 == n {found=$2} END {print found ? found : "absent"}'
}

# create_org_workspace SESSION_TOKEN ORG_ID NAME: create a workspace in an org
# straight through the gateway with a throwaway read+write key, and set
# ORG_KEY_ID to that key's record id.
create_org_workspace() {
	local token="$1" org="$2" name="$3" key
	key="$(api -X POST "$GATEWAY/v1/orgs/$org/api-keys" -H "Authorization: Bearer $token" \
		-d '{"name":"e2e-seed","scopes":["read","write"]}')"
	ORG_KEY="$(jq -r .key <<<"$key")"
	ORG_KEY_ID="$(jq -r .id <<<"$key")"
	[ -n "$ORG_KEY" ] && [ "$ORG_KEY" != null ] || fail "could not create an api key for org $org"
	local code
	code="$(api -o "$EVIDENCE_DIR/seed-$name.json" -w '%{http_code}' -X POST "$GATEWAY/v1/workspaces" \
		-H "Authorization: Bearer $ORG_KEY" \
		-d "$(jq -n --arg n "$name" --arg t "$TEMPLATE" '{name: $n, template: $t}')")"
	[ "$code" = 202 ] || fail "seeding workspace $name returned $code"
	track_workspace "$name"
}

# no_key_in_evidence: fail when any transcript holds something shaped like an API key.
no_key_in_evidence() {
	if grep -rEq 'dvsy_[A-Za-z0-9]+_[A-Za-z0-9_-]+' "$EVIDENCE_DIR"; then
		fail "transcript contains an API key"
	fi
}
