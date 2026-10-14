#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
    echo "usage: run-ci.sh <ginkgo-label> <expected-spec-count>" >&2
    exit 2
fi
label=$1
expected_count=$2
if [[ ! $label =~ ^[A-Za-z0-9][A-Za-z0-9-]*$ ]]; then
    echo "invalid Ginkgo label: $label" >&2
    exit 2
fi
if [[ ! $expected_count =~ ^[1-9][0-9]*$ ]]; then
    echo "expected-spec-count must be a positive integer" >&2
    exit 2
fi
if [[ -z ${RUNNER_TEMP:-} || ! -d ${RUNNER_TEMP} ]]; then
    echo "RUNNER_TEMP must name an existing directory" >&2
    exit 1
fi
: "${TEST_TIMEOUT:?TEST_TIMEOUT must be set by the workflow}"
: "${DEVSY_REQUIRE_MICROSANDBOX:?DEVSY_REQUIRE_MICROSANDBOX must be set by the workflow}"

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$script_dir/../.." && pwd)
report="${RUNNER_TEMP}/microsandbox-${label}.json"
test_log="${RUNNER_TEMP}/microsandbox-${label}.log"
command=(./e2e.test -test.v -ginkgo.v -test.timeout "$TEST_TIMEOUT"
    -ginkgo.fail-on-empty -ginkgo.flake-attempts=1
    --ginkgo.label-filter="$label" -ginkgo.json-report="$report")
if [[ $label == up-provider-microsandbox-managed-recreate ]]; then
    command=(../hack/run-managed-policy-e2e.sh "${command[@]}")
fi
sudo_environment=(
    "DEVSY_REQUIRE_MICROSANDBOX=$DEVSY_REQUIRE_MICROSANDBOX"
    "GH_USERNAME=$GH_USERNAME"
    "GH_ACCESS_TOKEN=$GH_ACCESS_TOKEN"
    "GH_CREDENTIAL_USERNAME=$GH_CREDENTIAL_USERNAME"
    "KUBECONFIG=${KUBECONFIG:-$HOME/.kube/config}"
    "PATH=$PATH"
)
if [[ -n ${DOCKER_HOST:-} ]]; then
    sudo_environment+=("DOCKER_HOST=${DOCKER_HOST}")
fi
if [[ $label == up-provider-microsandbox-managed-recreate ]]; then
    sudo_environment+=(
        DEVSY_REQUIRE_MSB_MANAGED_POLICY_FIXTURE=true
        "RUNNER_TEMP=${RUNNER_TEMP}"
    )
fi

cd "$repo_root/e2e" || exit 1
set +e
sudo "${sudo_environment[@]}" "${command[@]}" 2>&1 | tee "$test_log"
pipeline_status=("${PIPESTATUS[@]}")
set -e
test_status=${pipeline_status[0]}
log_status=${pipeline_status[1]}

chown_status=0
if [[ -e $report ]]; then
    sudo chown "$(id -u):$(id -g)" "$report" || chown_status=$?
fi
report_status=0
if [[ $chown_status -ne 0 ]]; then
    echo "::error::could not make Ginkgo report readable for artifact upload" >&2
    report_status=$chown_status
else
    python3 "$script_dir/verify_report.py" \
        --report "$report" --label "$label" --expected-count "$expected_count" ||
        report_status=$?
fi

if [[ $test_status -ne 0 ]]; then
    exit "$test_status"
fi
if [[ $log_status -ne 0 ]]; then
    exit "$log_status"
fi
exit "$report_status"
