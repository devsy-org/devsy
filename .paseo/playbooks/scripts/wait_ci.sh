#!/bin/sh
set -eu

PR="${PLAYBOOK_INPUT_PR_URL:-${1:-}}"
if [ -z "$PR" ]; then
  echo '{"outcome":"failure","summary":"Missing PR URL in PLAYBOOK_INPUT_PR_URL"}'
  exit 0
fi

CHECKS_JSON=$(gh pr checks "$PR" --json name,bucket,state 2>/dev/null || echo "")

if [ -z "$CHECKS_JSON" ] || [ "$CHECKS_JSON" = "[]" ]; then
  exit 75
fi

PENDING=$(echo "$CHECKS_JSON" | jq '[.[] | select(.bucket == "pending")] | length')
if [ "$PENDING" -gt 0 ]; then
  exit 75
fi

FAILURES=$(echo "$CHECKS_JSON" | jq -r '[.[] | select(.bucket == "fail" or .bucket == "cancel")] | .[].name' | tr '\n' ', ' | sed 's/,[[:space:]]*$//')
if [ -n "$FAILURES" ]; then
  echo "{\"outcome\":\"failure\",\"summary\":\"CI status checks failed: $FAILURES\"}"
  exit 0
fi

PASSING=$(echo "$CHECKS_JSON" | jq '[.[] | select(.bucket == "pass")] | length')
if [ "$PASSING" -eq 0 ]; then
  exit 75
fi

echo "{\"outcome\":\"success\",\"summary\":\"All $PASSING necessary CI status checks have passed.\"}"
exit 0
