ARG BASE_IMAGE=ghcr.io/devsy-org/test-images/go
ARG BASE_VERSION=1

FROM ${BASE_IMAGE}:${BASE_VERSION}

ARG BASE_IMAGE
ARG BASE_VERSION
RUN echo "${BASE_IMAGE}:${BASE_VERSION}" > /build-args.txt

RUN mkdir -p /app
WORKDIR /app
