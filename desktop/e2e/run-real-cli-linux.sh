#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

if [[ ${1-} == --namespace ]]; then
    shift
    root=$1 evidence=$2 desktop=$3 node=$4 go=$5 uid=$6 gid=$7 host_netns=$8 host_pidns=$9 revision=${10}
    mount --make-rprivate /
    mount -t proc -o nosuid,nodev,noexec proc /proc
    mount -t tmpfs -o mode=1777,nosuid,nodev tmpfs /tmp
    mount -t tmpfs -o mode=0755,nosuid,nodev tmpfs /run
    if [[ $(readlink -f /var/run) != /run ]]; then
        mount -t tmpfs -o mode=0755,nosuid,nodev tmpfs /var/run
    fi
    mountpoint -q /tmp
    mountpoint -q /run
    mountpoint -q "$(readlink -f /var/run)"
    sockets=$(ss -H -x -a)
    [[ -z $(find /tmp /run /var/run -type s -print -quit) && -z $sockets ]]
    ip link set lo up
    exec setpriv --reuid "$uid" --regid "$gid" --clear-groups --no-new-privs \
        --bounding-set=-all --inh-caps=-all --ambient-caps=-all \
        env -i PATH="$(dirname "$node"):/usr/local/bin:/usr/bin:/bin" \
        HOME="$root/home" DEVSY_HOME="$root/home/.devsy" DEVSY_CONFIG="$root/home/.devsy/config.yaml" \
        XDG_CONFIG_HOME="$root/xdg-config" XDG_CACHE_HOME="$root/xdg-cache" \
        XDG_DATA_HOME="$root/xdg-data" XDG_STATE_HOME="$root/xdg-state" \
        XDG_RUNTIME_DIR="$root/xdg-runtime" TMPDIR="$root/tmp" \
        DOCKER_CONFIG="$root/docker" GNUPGHOME="$root/gnupg" \
        DEVSY_DISABLE_DAEMON=true DEVSY_DISABLE_TELEMETRY=true DEVSY_LOG_FORMAT=text DEVSY_RESULT_FORMAT=text MISE_AUTO_INSTALL=0 GOTOOLCHAIN=local GOENV=off \
        REAL_CLI_ROOT="$root" REAL_CLI_EVIDENCE="$evidence" \
        REAL_CLI_ELECTRON="$desktop/node_modules/electron/dist/electron" \
        DEVSY_CLI_PATH="$desktop/resources/bin/devsy" \
        /bin/bash "$desktop/e2e/run-real-cli-linux.sh" --run "$desktop" "$node" "$go" "$uid" "$gid" "$host_netns" "$host_pidns" "$revision"
fi

if [[ ${1-} == --run ]]; then
    shift
    desktop=$1 node=$2 go=$3 uid=$4 gid=$5 host_netns=$6 host_pidns=$7 revision=$8
    [[ $(id -u) == "$uid" && $(id -g) == "$gid" && $uid != 0 ]]
    [[ $(readlink /proc/self/ns/net) != "$host_netns" ]]
    [[ $(readlink /proc/self/ns/pid) != "$host_pidns" && $(readlink /proc/self/ns/pid) == "$(readlink /proc/1/ns/pid)" ]]
    sockets=$(ss -H -x -a)
    [[ -z $(find /tmp /run /var/run -type s -print -quit) && -z $sockets ]]
    awk '/^Cap(Inh|Prm|Eff|Bnd|Amb):/ { if ($2 !~ /^0+$/) exit 1; n++ } /^NoNewPrivs:/ { if ($2 != 1) exit 1; p++ } END { if (n != 5 || p != 1) exit 1 }' /proc/self/status
    ip -j link show >"$REAL_CLI_EVIDENCE/links.json"
    ip -j -4 route show table all >"$REAL_CLI_EVIDENCE/routes-v4.json"
    ip -j -6 route show table all >"$REAL_CLI_EVIDENCE/routes-v6.json"
    "$node" - "$REAL_CLI_EVIDENCE" <<'NODE'
const fs = require('node:fs')
const root = process.argv[2]
const read = name => JSON.parse(fs.readFileSync(`${root}/${name}`, 'utf8'))
const links = read('links.json')
if (links.length !== 1 || links[0].ifname !== 'lo' || !links[0].flags.includes('UP')) process.exit(1)
for (const file of ['routes-v4.json', 'routes-v6.json']) {
  if (read(file).some(route => route.dst === 'default' || route.gateway || (route.dev && route.dev !== 'lo'))) process.exit(1)
}
NODE
    printf 'netns=%s\npidns=%s\nuid=%s gid=%s\nunix_sockets=none\n' "$(readlink /proc/self/ns/net)" "$(readlink /proc/self/ns/pid)" "$uid" "$gid" >"$REAL_CLI_EVIDENCE/boundary.txt"
    grep -E '^(Cap(Inh|Prm|Eff|Bnd|Amb)|NoNewPrivs):' /proc/self/status >>"$REAL_CLI_EVIDENCE/boundary.txt"
    preflight_fail() {
        printf 'preflight: %s\n' "$1" | tee -a "$REAL_CLI_EVIDENCE/preflight.txt" >&2
        exit 1
    }
    [[ -f $REAL_CLI_ELECTRON && ! -L $REAL_CLI_ELECTRON && -x $REAL_CLI_ELECTRON ]] ||
        preflight_fail 'Electron executable missing, symlinked, or not executable'
    [[ -f $DEVSY_CLI_PATH && ! -L $DEVSY_CLI_PATH && -x $DEVSY_CLI_PATH ]] ||
        preflight_fail 'candidate CLI missing, symlinked, or not executable'
    [[ -f $desktop/node_modules/@playwright/test/cli.js ]] ||
        preflight_fail 'local Playwright CLI missing'
    [[ -f $desktop/playwright.real-cli.config.ts ]] ||
        preflight_fail 'real CLI Playwright config missing'
    sha256sum "$DEVSY_CLI_PATH" "$REAL_CLI_ELECTRON" "$desktop/package-lock.json" >>"$REAL_CLI_EVIDENCE/hashes.txt" ||
        preflight_fail 'executable or lockfile hashing failed'
    if ! "$node" - "$desktop" >>"$REAL_CLI_EVIDENCE/preflight.txt" <<'NODE'; then
const fs = require('node:fs')
const dir = process.argv[2]
const values = [
  ['electron_package', () => JSON.parse(fs.readFileSync(`${dir}/node_modules/electron/package.json`, 'utf8')).version],
  ['electron_lock', () => JSON.parse(fs.readFileSync(`${dir}/package-lock.json`, 'utf8')).packages['node_modules/electron'].version],
  ['electron_dist', () => fs.readFileSync(`${dir}/node_modules/electron/dist/version`, 'utf8').trim()],
]
let failed = false
for (const [name, read] of values) {
  let value
  try {
    value = read()
  } catch {
    console.log(`${name}=unreadable`)
    failed = true
    continue
  }
  const safe = typeof value === 'string' && /^v?[0-9.]{1,32}$/.test(value) ? value : 'invalid'
  console.log(`${name}=${safe}`)
  const normalized = name === 'electron_dist' && safe.startsWith('v') ? safe.slice(1) : safe
  if (normalized !== '44.5.1') failed = true
}
if (failed) process.exit(1)
NODE
        preflight_fail 'Electron package, lock, or distribution version invalid; see preflight.txt'
    fi
    if electron_version=$(ELECTRON_RUN_AS_NODE=1 timeout 15s "$REAL_CLI_ELECTRON" -e 'process.stdout.write(process.versions.electron)' 2>/dev/null); then
        [[ $electron_version =~ ^[0-9.]{1,32}$ ]] || preflight_fail 'Electron runtime version output invalid'
        printf 'electron_runtime=%s\n' "$electron_version" >>"$REAL_CLI_EVIDENCE/preflight.txt"
        [[ $electron_version == 44.5.1 ]] || preflight_fail 'Electron runtime version mismatch; see preflight.txt'
    else
        preflight_fail 'Electron runtime version command failed or timed out'
    fi
    timeout 15s "$DEVSY_CLI_PATH" --version >"$REAL_CLI_EVIDENCE/cli-version.txt" ||
        preflight_fail 'candidate CLI version command failed or timed out'
    [[ -s $REAL_CLI_EVIDENCE/cli-version.txt ]] || preflight_fail 'candidate CLI version output empty'
    timeout 15s "$DEVSY_CLI_PATH" context get --help >"$REAL_CLI_EVIDENCE/cli-api.txt" ||
        preflight_fail 'candidate CLI API probe failed or timed out'
    timeout 15s "$go" version -m "$DEVSY_CLI_PATH" >"$REAL_CLI_EVIDENCE/cli-build.txt" ||
        preflight_fail 'candidate CLI build metadata probe failed or timed out'
    grep -Fq 'github.com/devsy-org/devsy' "$REAL_CLI_EVIDENCE/cli-build.txt" ||
        preflight_fail 'candidate CLI module identity mismatch'
    awk -v revision="$revision" '$1 == "build" && $2 ~ /^vcs\.revision=/ { revisions++; if ($2 != "vcs.revision=" revision) exit 1 } $1 == "build" && $2 ~ /^vcs\.modified=/ { modified++; if ($2 != "vcs.modified=false") exit 1 } END { if (revisions != 1 || modified != 1) exit 1 }' "$REAL_CLI_EVIDENCE/cli-build.txt" ||
        preflight_fail 'candidate CLI revision or clean-build metadata mismatch'
    cd "$desktop"
    exec timeout --kill-after=10s 180s xvfb-run -a "$node" "$desktop/node_modules/@playwright/test/cli.js" test --config playwright.real-cli.config.ts --workers=1 --retries=0 --reporter=list --output "$REAL_CLI_EVIDENCE/playwright-output"
fi

[[ $(uname -s) == Linux && $(id -u) != 0 && ${RUNNER_ENVIRONMENT-} == github-hosted && ${GITHUB_ACTIONS-} == true ]]
: "${RUNNER_TEMP:?}" "${REAL_CLI_EVIDENCE:?}" "${GITHUB_REF:?}" "${GITHUB_SHA:?}" "${GITHUB_RUN_ID:?}" "${GITHUB_RUN_ATTEMPT:?}"
desktop=$(cd "$(dirname "$0")/.." && pwd -P)
runner_temp=$(realpath -e "$RUNNER_TEMP")
case "$runner_temp/" in /tmp/* | /var/run/* | /run/*)
    echo 'RUNNER_TEMP is hidden by the namespace' >&2
    exit 1
    ;;
esac
[[ $REAL_CLI_EVIDENCE == /* && $REAL_CLI_EVIDENCE == "$runner_temp"/* && ! -e $REAL_CLI_EVIDENCE ]]
mkdir -m 700 "$REAL_CLI_EVIDENCE"
ulimit -f 131072
exec > >(tee "$REAL_CLI_EVIDENCE/runner.log") 2>&1
root=$(mktemp -d "$runner_temp/real-cli-root.XXXXXXXX")
namespace_status=not_started
owner="$(id -u):$(id -g)"
cleanup() {
    status=$?
    trap - EXIT
    umask 077
    if actual_owner=$(stat -c '%u:%g' "$root") && [[ $actual_owner == "$owner" ]] && rm -rf -- "$root" && [[ ! -e $root && ! -L $root ]]; then
        result=removed
    else
        result=failed
        status=1
    fi
    printf 'private_root=%s\nowner=%s\nnamespace_command_status=%s\ncleanup=%s\nexit_status=%s\n' "$root" "$owner" "$namespace_status" "$result" "$status" >"$REAL_CLI_EVIDENCE/cleanup.txt" || status=1
    exit "$status"
}
trap cleanup EXIT
mkdir -m 700 "$root"/{home,xdg-config,xdg-cache,xdg-data,xdg-state,xdg-runtime,tmp,docker,gnupg}
mkdir -m 700 "$root/home/.devsy"
node=$(command -v node)
go=$(command -v go)
[[ $node == /* && $go == /* && -x $node && -x $go ]]
checkout=$(git -C "$desktop" rev-parse --show-toplevel)
revision=$(git -C "$checkout" rev-parse --verify HEAD)
checkout_ref=$(git -C "$checkout" symbolic-ref -q HEAD || printf detached)
printf 'runner_environment=%s\ngithub_actions=%s\nrun_id=%s\nrun_attempt=%s\ngithub_ref=%s\ngithub_sha=%s\ncheckout_ref=%s\ncheckout_head=%s\n' \
    "$RUNNER_ENVIRONMENT" "$GITHUB_ACTIONS" "$GITHUB_RUN_ID" "$GITHUB_RUN_ATTEMPT" "$GITHUB_REF" "$GITHUB_SHA" "$checkout_ref" "$revision" >"$REAL_CLI_EVIDENCE/identity.txt"
proof_files=(.github/workflows/desktop-ci.yml desktop/playwright.real-cli.config.ts desktop/e2e/context-options.real-cli.ts desktop/e2e/real-cli-bootstrap.cjs desktop/e2e/run-real-cli-linux.sh)
git -C "$checkout" ls-files --error-unmatch -- "${proof_files[@]}" >/dev/null
for file in "${proof_files[@]}"; do
    [[ -f $checkout/$file && ! -L $checkout/$file ]]
    actual_blob=$(git -C "$checkout" hash-object --no-filters -- "$file")
    expected_blob=$(git -C "$checkout" rev-parse --verify "$revision:$file")
    [[ $actual_blob == "$expected_blob" ]]
done
(cd "$checkout" && sha256sum "${proof_files[@]}") >"$REAL_CLI_EVIDENCE/hashes.txt"
[[ -d $desktop/dist && -n $(find "$desktop/dist" -type f -print -quit) ]]
find "$desktop/dist" -type f -print0 | sort -z | xargs -0 -r sha256sum >>"$REAL_CLI_EVIDENCE/hashes.txt"
host_netns=$(readlink /proc/self/ns/net)
host_pidns=$(readlink /proc/self/ns/pid)
if timeout --kill-after=15s 240s sudo -n -- /usr/bin/env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin \
    unshare --net --mount --pid --fork --kill-child -- /bin/bash "$desktop/e2e/run-real-cli-linux.sh" --namespace \
    "$root" "$REAL_CLI_EVIDENCE" "$desktop" "$node" "$go" "$(id -u)" "$(id -g)" "$host_netns" "$host_pidns" "$revision"; then
    namespace_status=0
else
    namespace_status=$?
    exit "$namespace_status"
fi
