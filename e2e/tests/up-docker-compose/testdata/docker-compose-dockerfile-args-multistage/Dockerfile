ARG BASE_IMAGE=ghcr.io/devsy-org/test-images/go
ARG BASE_VERSION=1

FROM ${BASE_IMAGE}:${BASE_VERSION} AS builder

ARG BASE_IMAGE
ARG BASE_VERSION
RUN echo "${BASE_IMAGE}:${BASE_VERSION}" > /build-args.txt

FROM ${BASE_IMAGE}:${BASE_VERSION}

COPY --from=builder /build-args.txt /build-args.txt
RUN mkdir -p /app
WORKDIR /app
