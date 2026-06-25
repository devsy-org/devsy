FROM ghcr.io/devsy-org/test-images/base:ubuntu

# Create a non-root user. This is the configuration that exposes the podman
# bind-mount ownership bug: rootless podman surfaces the host workspace bind
# mount as root-owned inside the container, so a non-root remote user cannot
# chdir into the workspace folder unless the agent chowns it during setup.
RUN useradd --create-home --shell /bin/bash devsyuser
