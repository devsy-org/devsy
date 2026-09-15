FROM ghcr.io/devsy-org/test-images/base:ubuntu

# Non-root user, used as remoteUser to exercise non-root workspace operations.
RUN userdel -r vscode 2>/dev/null || true && \
    useradd --create-home --shell /bin/bash devsyuser
