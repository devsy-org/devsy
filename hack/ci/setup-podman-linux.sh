#!/usr/bin/env bash

set -euo pipefail

mode="${1:-}"
if [[ "$mode" != "rootless" && "$mode" != "rootful" ]]; then
    echo "::error::usage: $0 <rootless|rootful>"
    exit 2
fi

export PATH="/usr/local/bin:$PATH"

if [[ ! -x /usr/local/bin/podman ]]; then
    echo "::error::Expected Podman at /usr/local/bin/podman"
    exit 1
fi
if [[ ! -x /usr/local/bin/crun ]]; then
    echo "::error::Expected bundled crun at /usr/local/bin/crun"
    exit 1
fi

sudo mkdir -p /etc/containers/containers.conf.d
sudo tee /etc/containers/containers.conf.d/99-devsy-ci.conf >/dev/null <<'EOF'
[engine]
runtime = "crun"

[engine.runtimes]
crun = ["/usr/local/bin/crun"]
EOF

if [[ -f /etc/apparmor.d/podman ]]; then
    sudo sed -Ei \
        's!^profile podman /usr/bin/podman !profile podman /usr/{bin,local/bin}/podman !' \
        /etc/apparmor.d/podman
    sudo apparmor_parser -r /etc/apparmor.d/podman
fi

echo "Podman executable: $(command -v podman)"
podman --version
/usr/local/bin/crun --version
cat /etc/os-release
uname -a

if [[ "$mode" == "rootful" ]]; then
    sudo systemctl daemon-reload
    sudo systemctl enable --now podman.socket
    if ! timeout 30 bash -c \
        'until sudo podman --remote --url unix:///run/podman/podman.sock info >/dev/null 2>&1; do sleep 1; done'; then
        echo "::error::podman service did not become ready within 30s"
        sudo systemctl status podman.socket --no-pager || true
        sudo systemctl status podman.service --no-pager || true
        sudo journalctl -u podman.socket --no-pager -n 100 || true
        sudo journalctl -u podman.service --no-pager -n 100 || true
        exit 1
    fi

    runtime_path="$(
        sudo podman --remote --url unix:///run/podman/podman.sock \
            info --format '{{.Host.OCIRuntime.Path}}'
    )"
    echo "Podman OCI runtime: $runtime_path"
    if [[ "$runtime_path" != "/usr/local/bin/crun" ]]; then
        echo "::error::Unexpected Podman OCI runtime: $runtime_path"
        exit 1
    fi

    echo "DOCKER_HOST=unix:///run/podman/podman.sock" >>"$GITHUB_ENV"
    sudo podman --remote --url unix:///run/podman/podman.sock info
    sudo podman --remote --url unix:///run/podman/podman.sock run --rm \
        busybox@sha256:fd8d9aa63ba2f0982b5304e1ee8d3b90a210bc1ffb5314d980eb6962f1a9715d \
        true
else
    runtime_path="$(podman info --format '{{.Host.OCIRuntime.Path}}')"
    echo "Podman OCI runtime: $runtime_path"
    if [[ "$runtime_path" != "/usr/local/bin/crun" ]]; then
        echo "::error::Unexpected Podman OCI runtime: $runtime_path"
        exit 1
    fi

    podman info
    podman run --rm \
        busybox@sha256:fd8d9aa63ba2f0982b5304e1ee8d3b90a210bc1ffb5314d980eb6962f1a9715d \
        true
fi
