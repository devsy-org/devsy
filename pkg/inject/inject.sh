#!/bin/sh
set -e

INSTALL_DIR={{ .InstallDir }}
INSTALL_FILENAME={{ .InstallFilename }}
INSTALL_PATH="$INSTALL_DIR/$INSTALL_FILENAME"
PREFER_DOWNLOAD={{ .PreferDownload }}
CHMOD_PATH={{ .ChmodPath }}
INSTALLER_MODE=direct
INSTALLER_SHELL=/bin/sh
DOCKERLESS_SHELL=/.dockerless/bin/sh
temp_file=

command_exists() {
    command -v "$@" >/dev/null 2>&1
}

is_arm() {
    case "$(uname -a)" in
        *arm* | *arm64* | *aarch* | *aarch64*) true ;;
        *) false ;;
    esac
}

quote_installer_argument() {
    quote_value=$1
    quoted_argument="'"
    while :; do
        case "$quote_value" in
            *"'"*)
                quoted_argument="$quoted_argument${quote_value%%\'*}'\\''"
                quote_value=${quote_value#*\'}
                ;;
            *) break ;;
        esac
    done
    quoted_argument="$quoted_argument$quote_value'"
}

run_installer_script() {
    installer_code=$1
    shift
    case "$INSTALLER_MODE" in
        direct) "$INSTALLER_SHELL" -c "$installer_code" devsy-install "$@" ;;
        sudo) sudo -n -E "$INSTALLER_SHELL" -c "$installer_code" devsy-install "$@" ;;
        su)
            # su implementations disagree about forwarding arguments after -c.
            quote_installer_argument "$installer_code"
            su_command=" -c $quoted_argument devsy-install"
            quote_installer_argument "$INSTALLER_SHELL"
            su_command="$quoted_argument$su_command"
            for installer_argument; do
                quote_installer_argument "$installer_argument"
                su_command="$su_command $quoted_argument"
            done
            su -s "$INSTALLER_SHELL" -c "$su_command" root
            ;;
    esac
}

cleanup_temp() {
    if [ -n "$temp_file" ]; then
        run_installer_script 'rm -f -- "$1"' "$temp_file" || return 1
        temp_file=
    fi
}

check_noexec() {
    mount_path="$(df "$INSTALL_DIR" | tail -n +2 | rev | cut -d' ' -f1 | rev)"
    if mount | grep "on ${mount_path} " | grep -q noexec; then
        >&2 echo "Error: Installation directory $INSTALL_DIR is mounted with noexec flag"
        return 1
    fi
    return 0
}

handshake() {
    echo "ping"
    read -r DEVSY_PING || return 1
    if [ "$DEVSY_PING" != "pong" ]; then
        >&2 echo "Error: Handshake failed - received '$DEVSY_PING' instead of 'pong'"
        return 1
    fi
}

prepare_temp() {
    cleanup_temp || return 1
    temp_file=$(run_installer_script 'mktemp "$1.XXXXXX"' "$INSTALL_PATH") || {
        >&2 echo "Error: Failed to create a temporary agent in $INSTALL_DIR"
        return 1
    }
}

publish_binary() {
    if [ "$CHMOD_PATH" = "true" ]; then
        run_installer_script 'chmod -- 0755 "$1"' "$temp_file" || {
            >&2 echo "Error: Failed to make temporary agent executable"
            return 1
        }
    fi
    run_installer_script '[ ! -d "$2" ] && mv -f -- "$1" "$2"' "$temp_file" "$INSTALL_PATH" || {
        >&2 echo "Error: Failed to replace agent at $INSTALL_PATH"
        return 1
    }
    temp_file=
}

inject_binary() {
    echo "ARM-$(is_arm && printf true || printf false)"
    if ! read -r BINARY_SIZE; then
        >&2 echo "Error: Failed to read binary size"
        return 1
    fi
    case "$BINARY_SIZE" in
        '' | *[!0-9]*)
            >&2 echo "Error: Invalid binary size: $BINARY_SIZE"
            return 1
            ;;
    esac
    prepare_temp || return 1
    run_installer_script 'head -c "$1" > "$2" && [ "$(wc -c < "$2" | tr -d "[:space:]")" = "$1" ]' "$BINARY_SIZE" "$temp_file" || {
        >&2 echo "Error: Failed to receive complete agent binary"
        cleanup_temp || true
        return 1
    }
    publish_binary || {
        cleanup_temp || true
        return 1
    }
}

download_binary() {
    DOWNLOAD_URL={{ .DownloadAmd }}
    if is_arm; then
        DOWNLOAD_URL={{ .DownloadArm }}
    fi
    iteration=1
    max_iteration=3
    while [ "$iteration" -le "$max_iteration" ]; do
        prepare_temp || return 1
        if command_exists curl; then
            run_installer_script 'curl -fsSL --connect-timeout 10 --max-time 120 -o "$2" -- "$1"' "$DOWNLOAD_URL" "$temp_file" && break
            cmd_status=$?
        elif command_exists wget; then
            run_installer_script 'wget -q -T 120 -O "$2" -- "$1"' "$DOWNLOAD_URL" "$temp_file" && break
            cmd_status=$?
        else
            >&2 echo "Error: No download tool found (curl or wget required)"
            cleanup_temp || true
            return 127
        fi
        cleanup_temp || return 1
        >&2 echo "Error: Download attempt $iteration failed (exit code: $cmd_status)"
        if [ "$iteration" -lt "$max_iteration" ]; then
            >&2 echo "       Retrying in 10 seconds"
            sleep 10 || return 1
        fi
        iteration=$((iteration + 1))
    done
    if [ "$iteration" -gt "$max_iteration" ]; then
        >&2 echo "Error: Failed to download devsy after $max_iteration attempts"
        return 1
    fi
    publish_binary || {
        cleanup_temp || true
        return 1
    }
}

setup_sudo() {
    # Dockerless injects before the image filesystem supplies /bin/sh.
    if [ "${DOCKERLESS:-}" = "true" ] && [ ! -x "$INSTALLER_SHELL" ] && [ -x "$DOCKERLESS_SHELL" ]; then
        INSTALLER_SHELL=$DOCKERLESS_SHELL
    fi
    if [ ! -x "$INSTALLER_SHELL" ]; then
        >&2 echo "Error: Agent installation requires executable /bin/sh (or the bundled Dockerless shell in Dockerless containers)"
        return 1
    fi
    if ! run_installer_script 'mkdir -p -- "$1" && probe=$(mktemp "$1/.devsy-write.XXXXXX") && rm -f -- "$probe"' "$INSTALL_DIR" 2>/dev/null; then
        if command_exists sudo; then
            if ! sudo -nl >/dev/null 2>&1; then
                >&2 echo "Error: sudo requires a password. Please configure NOPASSWD"
                return 1
            fi
            INSTALLER_MODE=sudo
        elif command_exists su; then
            INSTALLER_MODE=su
        else
            >&2 echo "Error: Need root access but sudo/su not available"
            return 1
        fi
        run_installer_script 'mkdir -p -- "$1"' "$INSTALL_DIR" || return 1
    fi
}

install_agent() {
    if [ "$PREFER_DOWNLOAD" = "true" ]; then
        if ! download_binary; then
            >&2 echo "Download failed, attempting stdin injection"
            inject_binary || {
                >&2 echo "Error: Both download and injection failed"
                return 1
            }
        fi
    else
        if ! inject_binary; then
            >&2 echo "Injection failed, attempting download"
            download_binary || {
                >&2 echo "Error: Both injection and download failed"
                return 1
            }
        fi
    fi
}

execute_command() {
    echo "done"
    export DEVSY_AGENT_URL={{ .DownloadBase }}
    {{ .Command }}
}

main() {
    handshake || return 1
    if {{ .ExistsCheck }}; then
        setup_sudo || return 1
        check_noexec || return 1
        install_agent || return 1
    fi
    execute_command
}

trap 'cleanup_temp' 0
trap 'exit 1' HUP INT TERM
main
