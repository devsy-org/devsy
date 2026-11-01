FROM ghcr.io/devsy-org/test-images/base:ubuntu
USER root
RUN apt-get update && apt-get install -y --no-install-recommends dbus gnome-keyring ca-certificates && rm -rf /var/lib/apt/lists/*
COPY gh /opt/fixture-gh
COPY ghfixture /opt/ghfixture
RUN install -m 755 /opt/fixture-gh /usr/bin/gh && install -m 755 /opt/fixture-gh /usr/local/bin/gh
USER vscode
