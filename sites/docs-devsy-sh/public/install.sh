#!/bin/sh
set -eu

info() {
    printf '%s\n' "$*"
}

warn() {
    printf 'warning: %s\n' "$*" >&2
}

fatal() {
    printf 'error: %s\n' "$*" >&2
    exit 1
}

need() {
    command -v "$1" >/dev/null 2>&1 || fatal "required command '$1' was not found; install it and re-run this script"
}

detect_os() {
    case "$(uname -s)" in
        Linux) printf 'linux' ;;
        Darwin) printf 'darwin' ;;
        MINGW* | MSYS* | CYGWIN*)
            fatal "this script supports macOS and Linux; for Windows see https://devsy.sh/docs/getting-started/install#install-devsy-cli"
            ;;
        *) fatal "unsupported operating system: $(uname -s)" ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64 | amd64) printf 'amd64' ;;
        arm64 | aarch64) printf 'arm64' ;;
        *) fatal "unsupported CPU architecture: $(uname -m)" ;;
    esac
}

sha256_of() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    else
        return 1
    fi
}

maybe_verify_checksum() {
    if [ ! -s "$3" ]; then
        info "This release does not publish checksums; skipping checksum verification."
        return 0
    fi
    expected=$(awk -v asset="$2" '$NF == asset {print $1}' "$3" | head -n 1)
    if [ -z "$expected" ]; then
        warn "checksums.txt has no entry for $2; skipping checksum verification"
        return 0
    fi
    if ! actual=$(sha256_of "$1"); then
        warn "no SHA-256 tool found (sha256sum or shasum); skipping checksum verification"
        return 0
    fi
    if [ "$actual" != "$expected" ]; then
        fatal "checksum mismatch for $2: expected $expected, got $actual; the download may be corrupted or tampered with, aborting"
    fi
    info "Checksum verified."
}

choose_install_dir() {
    if [ -n "${DEVSY_INSTALL_DIR:-}" ]; then
        printf '%s' "$DEVSY_INSTALL_DIR"
    elif [ -w /usr/local/bin ] || command -v sudo >/dev/null 2>&1; then
        printf '/usr/local/bin'
    else
        printf '%s/.local/bin' "$HOME"
    fi
}

main() {
    need curl
    need uname

    os=$(detect_os)
    arch=$(detect_arch)
    asset="devsy-$os-$arch"

    base=${DEVSY_RELEASE_BASE_URL:-https://github.com/devsy-org/devsy/releases}
    version=${DEVSY_VERSION:-}
    if [ -n "$version" ]; then
        download_base="$base/download/$version"
    else
        download_base="$base/latest/download"
    fi

    tmpdir=$(mktemp -d)
    trap 'rm -rf "$tmpdir"' EXIT

    info "Downloading devsy ${version:-latest} for $os/$arch..."
    if ! curl -fSL --connect-timeout 15 -o "$tmpdir/$asset" "$download_base/$asset"; then
        fatal "download failed: $download_base/$asset (check that the release exists and your network is up)"
    fi

    curl -fsSL -o "$tmpdir/checksums.txt" "$download_base/checksums.txt" 2>/dev/null || true
    maybe_verify_checksum "$tmpdir/$asset" "$asset" "$tmpdir/checksums.txt"

    install_dir=$(choose_install_dir)
    if [ ! -w "$install_dir" ] && command -v sudo >/dev/null 2>&1; then
        sudo mkdir -p "$install_dir"
        sudo install -c -m 0755 "$tmpdir/$asset" "$install_dir/devsy" || fatal "could not install to $install_dir"
    else
        mkdir -p "$install_dir" 2>/dev/null || true
        if [ ! -w "$install_dir" ]; then
            fatal "$install_dir is not writable and sudo is unavailable; set DEVSY_INSTALL_DIR to a writable directory"
        fi
        install -c -m 0755 "$tmpdir/$asset" "$install_dir/devsy" || fatal "could not install to $install_dir"
    fi
    info "Installed $install_dir/devsy"

    case ":$PATH:" in
        *":$install_dir:"*) ;;
        *) warn "$install_dir is not on your PATH; add it with: export PATH=\"$install_dir:\$PATH\"" ;;
    esac

    if "$install_dir/devsy" --version >/dev/null 2>&1; then
        info "$("$install_dir/devsy" --version)"
    fi
    info "Next steps: https://devsy.sh/docs/getting-started/quickstart"
}

main "$@"
