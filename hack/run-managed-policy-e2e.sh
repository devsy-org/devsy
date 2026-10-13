#!/usr/bin/env bash
set -euo pipefail
umask 077

if [[ ${EUID} -ne 0 ]]; then
    echo "managed-policy fixture must run as root" >&2
    exit 1
fi
if [[ ${DEVSY_REQUIRE_MSB_MANAGED_POLICY_FIXTURE:-} != true ]]; then
    echo "managed-policy E2E wrapper requires the CI expectation marker" >&2
    exit 1
fi
if [[ -z ${RUNNER_TEMP:-} || ! -d ${RUNNER_TEMP} ]]; then
    echo "RUNNER_TEMP must name an existing directory" >&2
    exit 1
fi
if [[ ${1:-} != ./e2e.test ]]; then
    echo "expected the already-built ./e2e.test binary as the first argument" >&2
    exit 1
fi

fixture_root=$(mktemp -d "${RUNNER_TEMP%/}/devsy-managed-policy.XXXXXX")
chmod 700 "$fixture_root"
mkdir -m 700 "$fixture_root/upper" "$fixture_root/work"
for directory in "$fixture_root" "$fixture_root/upper" "$fixture_root/work"; do
    if [[ $(stat -c '%u:%g:%a' "$directory") != "0:0:700" ]]; then
        echo "managed-policy fixture directory has unexpected ownership or mode: $directory" >&2
        exit 1
    fi
done
namespace_id_file="$fixture_root/mount-namespace-id"
parent_mount_namespace=$(readlink /proc/self/ns/mnt)
export RUNNER_TEMP fixture_root namespace_id_file parent_mount_namespace

# Deliberately retain the small private scratch under RUNNER_TEMP for runner
# job teardown. Do not remove overlay upper/work after an abnormal namespace
# exit: an unobserved namespace reference could still use them.
unshare --mount --propagation private -- /bin/bash -s -- "$fixture_root" "$namespace_id_file" "$parent_mount_namespace" "$@" <<'NAMESPACE'
set -euo pipefail
umask 077
fixture_root=$1
namespace_id_file=$2
parent_mount_namespace=$3
shift 3

if [[ ${EUID} -ne 0 ]]; then
  echo "managed-policy namespace must run as root" >&2
  exit 1
fi
current_mount_namespace=$(readlink /proc/self/ns/mnt)
if [[ $current_mount_namespace == "$parent_mount_namespace" ]]; then
  echo "mount namespace did not change" >&2
  exit 1
fi
printf '%s\n' "$current_mount_namespace" > "$namespace_id_file"

# Overlay /etc before creating any managed-policy path so no host file changes.
mount -t overlay overlay -o "lowerdir=/etc,upperdir=$fixture_root/upper,workdir=$fixture_root/work" /etc
if ! awk -v upper="$fixture_root/upper" -v work="$fixture_root/work" '
  $5 == "/etc" {
    separator = 0
    for (i = 1; i <= NF; i++) if ($i == "-") separator = i
    if (separator && $(separator + 1) == "overlay") {
      count = split($(separator + 3), options, ",")
      for (i = 1; i <= count; i++) {
        if (options[i] == "upperdir=" upper) found_upper = 1
        if (options[i] == "workdir=" work) found_work = 1
      }
      if (found_upper && found_work) found = 1
    }
  }
  END { exit !found }
' /proc/self/mountinfo; then
  echo "private /etc overlay does not use this fixture's owned upper and work directories" >&2
  exit 1
fi
if [[ -e /etc/microsandbox || -L /etc/microsandbox ]]; then
  echo "managed-policy fixture requires /etc/microsandbox to be absent in the overlay lower layer" >&2
  exit 1
fi
install -d -o 0 -g 0 -m 755 /etc/microsandbox
policy_tmp=/etc/microsandbox/.managed.json.initial.$$
printf '%s\n' '{"version":1,"overrides":{}}' > "$policy_tmp"
chown 0:0 "$policy_tmp"
chmod 600 "$policy_tmp"
mv -T -- "$policy_tmp" /etc/microsandbox/managed.json

export DEVSY_REQUIRE_MSB_MANAGED_POLICY_FIXTURE=true
export DEVSY_TEST_MSB_MANAGED_POLICY=/etc/microsandbox/managed.json
export DEVSY_TEST_MSB_FIXTURE_ROOT="$fixture_root"
export DEVSY_TEST_MSB_PARENT_MNT_NS="$parent_mount_namespace"
export DEVSY_TEST_MSB_EXPECTED_MNT_NS="$current_mount_namespace"
export DEVSY_TEST_MSB_EXPECTED_UPPER="$fixture_root/upper"
export DEVSY_TEST_MSB_EXPECTED_WORK="$fixture_root/work"
exec "$@"
NAMESPACE
