package dockerwsl

// Fixed Linux-side locations. Cleanup trusts only these literals, never a path
// read from the state file.
const (
	ciUser   = "devsyci"
	rootUser = "root"
	sshPort  = "2222"

	dockerSocketPath = "/var/run/docker.sock"
	dockerRuntimeDir = "/run/devsy-ci"
	dockerPidfile    = dockerRuntimeDir + "/dockerd.pid"
	sshdPidfile      = dockerRuntimeDir + "/sshd.pid"
	linuxLogDir      = "/tmp/devsy-ci"
	dockerLogFile    = linuxLogDir + "/dockerd.log"
	sshdLogFile      = linuxLogDir + "/sshd.log"
	sshdConfigDir    = "/etc/ssh/devsy-ci"
	sshdConfigFile   = sshdConfigDir + "/sshd_config"
	sshdHostKeyPub   = sshdConfigDir + "/host_ed25519.pub"

	// dockerdMarker and sshdMarker must appear on the owned process's command
	// line; stopOwnedScript refuses to signal any other process.
	dockerdMarker = "--pidfile=" + dockerPidfile
	sshdMarker    = sshdConfigFile

	// Hex ports as they appear in /proc/net/tcp: 2222 and 2375.
	sshPortHex       = "08AE"
	dockerTCPPortHex = "0947"

	// Exit statuses shared between scripts and the Go side.
	scriptExitConflict = 10
	scriptExitBadInput = 11
	scriptExitNotOwned = 4
)

// preflightScript reports what already exists, so setup can refuse to touch a
// daemon or account it did not create.
const preflightScript = `set -u
dockerd_running=absent
for comm in /proc/[0-9]*/comm; do
  if IFS= read -r name 2>/dev/null < "$comm" && [ "$name" = dockerd ]; then
    dockerd_running=present
    break
  fi
done
echo "dockerd_running=$dockerd_running"
if [ -e /var/run/docker.sock ]; then echo socket=present; else echo socket=absent; fi
if dpkg-query -W -f='${Status}' docker-ce 2>/dev/null | grep -q 'install ok installed'; then
  echo docker_installed=yes
else
  echo docker_installed=no
fi
if [ -d /run/systemd/system ]; then echo systemd=yes; else echo systemd=no; fi
if id -u devsyci >/dev/null 2>&1; then echo ci_user=present; else echo ci_user=absent; fi
if [ -e /run/devsy-ci ] || [ -e /etc/ssh/devsy-ci ] || [ -e /tmp/devsy-ci ]; then
  echo leftovers=yes
else
  echo leftovers=no
fi
`

// installPackagesScript installs Docker Engine from Docker's signed apt
// repository and prints a package inventory.
const installPackagesScript = `set -eu
export DEBIAN_FRONTEND=noninteractive
apt_opts='-o Acquire::Retries=3 -o DPkg::Lock::Timeout=120'
apt-get $apt_opts update -q
apt-get $apt_opts install -y -q ca-certificates curl openssh-server
install -m 0755 -d /etc/apt/keyrings
curl -fsSL --retry 3 --max-time 60 https://download.docker.com/linux/ubuntu/gpg \
  -o /etc/apt/keyrings/docker.asc
chmod a+r /etc/apt/keyrings/docker.asc
arch=$(dpkg --print-architecture)
codename=$(. /etc/os-release && echo "$VERSION_CODENAME")
repo=https://download.docker.com/linux/ubuntu
echo "deb [arch=$arch signed-by=/etc/apt/keyrings/docker.asc] $repo $codename stable" \
  > /etc/apt/sources.list.d/docker.list
apt-get $apt_opts update -q
apt-get $apt_opts install -y -q docker-ce docker-ce-cli containerd.io docker-buildx-plugin
dpkg-query -W -f='package ${Package}=${Version}\n' \
  docker-ce docker-ce-cli containerd.io docker-buildx-plugin openssh-server
`

// stopInstallerDaemonScript stops a docker service the package install started.
// Preflight proved no docker socket existed beforehand, so any daemon here is
// this fixture's. A stopped daemon can leave its socket file behind, which
// would read as a live daemon to the readiness checks that follow, so the
// script removes it once no dockerd process remains.
const stopInstallerDaemonScript = `set -u
if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
  systemctl stop docker.service docker.socket >/dev/null 2>&1
fi
if [ -e /var/run/docker.sock ] && command -v service >/dev/null 2>&1; then
  service docker stop >/dev/null 2>&1
fi
dockerd_running() { pgrep -x dockerd >/dev/null; }
i=0
while dockerd_running && [ "$i" -lt 15 ]; do sleep 1; i=$((i+1)); done
if dockerd_running; then
  echo "installer dockerd still running after stopping it" >&2
  pgrep -ax dockerd >&2
  exit 1
fi
rm -f /var/run/docker.sock
`

// prepareRuntimeScript creates the owned runtime and log directories and the
// daemon configuration. dockerd gets exactly one authoritative socket target
// from its command line; daemon.json carries no hosts entry.
const prepareRuntimeScript = `set -eu
mkdir -p /run/devsy-ci /tmp/devsy-ci
chmod 0755 /run/devsy-ci /tmp/devsy-ci
printf '{}\n' > /run/devsy-ci/daemon.json
`

// dockerdLaunchScript is the argument of sh -c for the detached daemon.
const dockerdLaunchScript = "exec /usr/bin/dockerd --host=unix://" + dockerSocketPath +
	" --config-file=" + dockerRuntimeDir + "/daemon.json " + dockerdMarker +
	" >" + dockerLogFile + " 2>&1"

// sshdLaunchScript is the argument of sh -c for the detached sshd.
const sshdLaunchScript = "exec /usr/sbin/sshd -D -e -f " + sshdConfigFile +
	" >" + sshdLogFile + " 2>&1"

// tcpListenScript exits 0 when something listens on the hex port $1 (IPv4 or
// IPv6), 1 when nothing does and 2 when /proc/net/tcp cannot be read.
const tcpListenScript = `set -u
proc=${DEVSY_PROC_ROOT:-/proc}
files=
for f in tcp tcp6; do
  if [ -r "$proc/net/$f" ]; then files="$files $proc/net/$f"; fi
done
[ -n "$files" ] || exit 2
awk -v port=":$1" '
  $2 ~ (port "$") && $4 == "0A" { found = 1 }
  END { exit found ? 0 : 1 }
' $files
`

// createUserScript creates the restricted account, tagging it with the run
// marker so cleanup can prove ownership. $1 user, $2 marker.
const createUserScript = `set -eu
user=$1
marker=$2
if id -u "$user" >/dev/null 2>&1; then
  echo "user $user already exists and is not owned by this run" >&2
  exit 10
fi
useradd --create-home --shell /bin/bash --comment "$marker" --groups docker "$user"
usermod --password '*' "$user"
install -d -m 0700 -o "$user" -g "$user" "/home/$user/.ssh"
`

// configureSSHDScript writes the dedicated sshd configuration and host key.
const configureSSHDScript = `set -eu
dir=/etc/ssh/devsy-ci
mkdir -p /run/sshd /run/devsy-ci /tmp/devsy-ci
install -d -m 0755 "$dir"
rm -f "$dir/host_ed25519" "$dir/host_ed25519.pub"
ssh-keygen -q -t ed25519 -N '' -f "$dir/host_ed25519"
cat > "$dir/sshd_config" <<'EOF'
Port 2222
ListenAddress 0.0.0.0
HostKey /etc/ssh/devsy-ci/host_ed25519
PidFile /run/devsy-ci/sshd.pid
PasswordAuthentication no
KbdInteractiveAuthentication no
PubkeyAuthentication yes
PermitRootLogin no
AllowUsers devsyci
UsePAM no
StrictModes yes
AuthorizedKeysFile .ssh/authorized_keys
EOF
/usr/sbin/sshd -t -f "$dir/sshd_config"
`

// installKeyScript installs the base64-encoded public key $2 for user $1.
const installKeyScript = `set -eu
user=$1
key=$(printf %s "$2" | base64 -d)
case $key in
  "ssh-ed25519 "*) ;;
  *) echo "not an ed25519 public key" >&2; exit 11 ;;
esac
if [ "$(printf %s "$key" | wc -l)" -ne 0 ]; then
  echo "public key spans several lines" >&2
  exit 11
fi
home=/home/$user
printf '%s\n' "$key" > "$home/.ssh/authorized_keys"
chown "$user:$user" "$home/.ssh/authorized_keys"
chmod 0600 "$home/.ssh/authorized_keys"
`

// stopOwnedScript stops the process named in pidfile $1 only when its command
// line contains both $2 (program name) and $3 (ownership marker). A missing
// pidfile or dead pid is success; a live process that does not match is left
// alone and reported.
const stopOwnedScript = `set -u
pidfile=$1
name=$2
marker=$3
proc=${DEVSY_PROC_ROOT:-/proc}
[ -e "$pidfile" ] || exit 0
pid=$(cat "$pidfile" 2>/dev/null) || pid=
case $pid in
  ''|*[!0-9]*) echo "pidfile $pidfile holds no valid pid" >&2; exit 3 ;;
esac
if ! kill -0 "$pid" 2>/dev/null; then rm -f "$pidfile"; exit 0; fi
cmd=$(tr '\0' ' ' <"$proc/$pid/cmdline" 2>/dev/null) || cmd=
case $cmd in
  *"$name"*"$marker"*) ;;
  *) echo "pid $pid is not the $name owned by devsy-ci" >&2; exit 4 ;;
esac
kill -TERM "$pid" 2>/dev/null
i=0
while kill -0 "$pid" 2>/dev/null && [ "$i" -lt 20 ]; do sleep 1; i=$((i+1)); done
if kill -0 "$pid" 2>/dev/null; then kill -KILL "$pid" 2>/dev/null; sleep 1; fi
if kill -0 "$pid" 2>/dev/null; then echo "pid $pid survived SIGKILL" >&2; exit 5; fi
rm -f "$pidfile"
`

// removeUserScript deletes the account $1 only when its GECOS field equals the
// run marker $2, ending the account's own sessions first. The account is
// identified by uid, so this is never a name-based sweep.
const removeUserScript = `set -u
user=$1
marker=$2
entry=$(getent passwd "$user") || exit 0
gecos=$(printf %s "$entry" | cut -d: -f5)
if [ "$gecos" != "$marker" ]; then
  echo "user $user is not owned by this run" >&2
  exit 4
fi
pkill -TERM -u "$user" 2>/dev/null
sleep 1
pkill -KILL -u "$user" 2>/dev/null
userdel -r "$user" >/dev/null 2>&1
if getent passwd "$user" >/dev/null 2>&1; then
  echo "user $user still exists after userdel" >&2
  exit 6
fi
`

// removeFilesScript deletes the fixed owned directories and the daemon socket
// (only after its owning daemon was stopped).
const removeFilesScript = `set -u
rm -rf /etc/ssh/devsy-ci /run/devsy-ci /tmp/devsy-ci
rm -f /var/run/docker.sock
if [ -e /etc/ssh/devsy-ci ] || [ -e /run/devsy-ci ] || [ -e /tmp/devsy-ci ]; then
  echo "owned directories still exist" >&2
  exit 1
fi
`

// diagnosticsScript preserves up to the previous 8 KiB per daemon log as
// complete bounded lines; PEM blocks and overlong lines are omitted.
const diagnosticsScript = `set -u
for f in /tmp/devsy-ci/dockerd.log /tmp/devsy-ci/sshd.log; do
  echo "== tail $f (up to 8 complete 1KiB lines; PEM omitted)"
  LC_ALL=C awk '
    BEGIN { max_lines = 8; max_line_bytes = 1024; count = 0; in_key = 0 }
    /-----BEGIN [A-Z ]*PRIVATE KEY-----/ {
      # A later BEGIN must not expose the following key body.
      key_tail = $0
      sub(/^.*-----BEGIN [A-Z ]*PRIVATE KEY-----/, "", key_tail)
      in_key = (key_tail !~ /-----END [A-Z ]*PRIVATE KEY-----/)
      next
    }
    in_key {
      if (/-----END [A-Z ]*PRIVATE KEY-----/) in_key = 0
      next
    }
    length($0) > max_line_bytes { next }
    { lines[count % max_lines] = $0; count++ }
    END {
      start = count > max_lines ? count - max_lines : 0
      for (i = start; i < count; i++) print lines[i % max_lines]
    }
  ' "$f" 2>&1
done
echo "== docker socket"
ls -l /var/run/docker.sock 2>&1 | cut -c1-160
echo "== processes (pid,ppid,user,comm; max 6)"
ps -eo pid,ppid,user,comm 2>&1 | grep -E '[d]ockerd|[s]shd' | cut -c1-120 | head -n 6
for f in /proc/net/tcp /proc/net/tcp6; do
  if [ -r "$f" ]; then
    echo "== listeners $f (ssh=08AE,docker-tcp=0947; max 4)"
    awk '$4 == "0A" && ($2 ~ /:08AE$/ || $2 ~ /:0947$/) { if (count++ < 4) print substr($0, 1, 120) }' "$f" 2>&1
  fi
done
`
