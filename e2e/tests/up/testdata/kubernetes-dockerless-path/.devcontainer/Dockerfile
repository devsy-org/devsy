FROM ubuntu:24.04

RUN mkdir -p /opt/devsy-dockerless-path/bin /opt/devsy-dockerless-path/docker-config

COPY devsy-dockerless-path-check /opt/devsy-dockerless-path/bin/devsy-dockerless-path-check
RUN chmod 0755 /opt/devsy-dockerless-path/bin/devsy-dockerless-path-check

ENV PATH=/opt/devsy-dockerless-path/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ENV DOCKER_CONFIG=/opt/devsy-dockerless-path/docker-config
