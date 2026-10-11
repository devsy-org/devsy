FROM ghcr.io/devsy-org/test-images/base:alpine
RUN addgroup -g 31001 dlparity && adduser -D -u 31001 -G dlparity dlparity \
    && mkdir -p /opt/devsy-dockerless-parity/bin /opt/devsy-dockerless-parity/docker-config \
    && printf '%s\n' dockerfile-parity > /usr/local/share/devsy-dockerless-marker
COPY devsy-dockerless-parity-check /opt/devsy-dockerless-parity/bin/devsy-dockerless-parity-check
RUN chmod 0755 /opt/devsy-dockerless-parity/bin/devsy-dockerless-parity-check
ENV PATH=/opt/devsy-dockerless-parity/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ENV DOCKER_CONFIG=/opt/devsy-dockerless-parity/docker-config
ENV DEVSY_DOCKERLESS_IMAGE=from-image
