#!/usr/bin/env bash
set -euo pipefail

if [[ -z ${RUNNER_TEMP:-} || ! -d ${RUNNER_TEMP} ]]; then
    echo "RUNNER_TEMP must name an existing directory" >&2
    exit 1
fi
if [[ -z ${GITHUB_PATH:-} ]]; then
    echo "GITHUB_PATH must name the workflow PATH file" >&2
    exit 1
fi

archive="${RUNNER_TEMP}/microsandbox-linux-x86_64-v0.7.7.tar.gz"
if [[ ! -f "$archive" ]]; then
    curl -fsSL --retry 3 -o "$archive" \
        https://github.com/superradcompany/microsandbox/releases/download/v0.7.7/microsandbox-linux-x86_64.tar.gz
fi
printf '%s  %s\n' \
    b3cc4a5e3f52dfdd938a6f67ac4a9a959ddfe304bab56de4964044b8613f01bb "$archive" |
    sha256sum -c -

staging=$(mktemp -d)
trap 'rm -rf "$staging"' EXIT
tar -xzf "$archive" -C "$staging"
mkdir -p "$HOME/.microsandbox/bin" "$HOME/.microsandbox/lib" "$HOME/.local/bin"
install -m 755 "$staging/msb" "$HOME/.microsandbox/bin/msb"
install -m 644 "$staging/libkrunfw.so.5.6.1" "$HOME/.microsandbox/lib/"
ln -sf libkrunfw.so.5.6.1 "$HOME/.microsandbox/lib/libkrunfw.so.5"
ln -sf libkrunfw.so.5 "$HOME/.microsandbox/lib/libkrunfw.so"
ln -sf "$HOME/.microsandbox/bin/msb" "$HOME/.local/bin/msb"
echo "$HOME/.local/bin" >>"$GITHUB_PATH"
"$HOME/.local/bin/msb" --version
sudo test -r /dev/kvm
sudo test -w /dev/kvm
