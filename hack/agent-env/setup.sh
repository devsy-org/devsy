#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${REPO_ROOT}"

echo "==> Installing go-task..."
if ! command -v task &>/dev/null; then
  sudo sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d -b /usr/local/bin
fi

echo "==> Installing prek..."
curl --proto '=https' --tlsv1.2 -LsSf https://github.com/j178/prek/releases/download/v0.5.0/prek-installer.sh | sh
prek install

echo "==> Setting up Go modules..."
export GOTOOLCHAIN=auto
go mod download
go mod verify

echo "==> Installing golangci-lint..."
GOLANGCI_VERSION="$(cat .golangci-version | tr -d '[:space:]')"
curl -sSfL https://golangci-lint.run/install.sh | sh -s "${GOLANGCI_VERSION}"

echo "==> Installing Node dependencies with pnpm..."
pnpm install

if [ -n "${GPG_PRIVATE_KEY:-}" ]; then
  echo "==> Configuring GPG signing..."
  mkdir -p ~/.gnupg
  chmod 700 ~/.gnupg
  echo "allow-loopback-pinentry" >> ~/.gnupg/gpg-agent.conf
  gpgconf --kill gpg-agent || true

  echo "$GPG_PRIVATE_KEY" | gpg --batch --import --passphrase "${GPG_PASSPHRASE:-}" || true

  KEY_ID=$(gpg --list-secret-keys --keyid-format LONG 2>/dev/null | awk '/^sec/ {print $2}' | cut -d'/' -f2 | head -n 1 || true)

  if [ -n "$KEY_ID" ]; then
    git config --global user.signingkey "$KEY_ID"
    git config --global commit.gpgsign true
    git config --global gpg.program gpg
    if [ -n "${GPG_PASSPHRASE:-}" ]; then
      git config --global gpg.pinentryMode loopback
    fi
    echo "GPG signing configured with key ID: $KEY_ID"
  fi
fi

echo "==> Running agent-env Go entrypoint..."
go run ./hack/agent-env

echo "done"
