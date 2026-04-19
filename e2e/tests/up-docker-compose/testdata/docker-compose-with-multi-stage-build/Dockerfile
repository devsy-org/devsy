FROM ghcr.io/devsy-org/test-images/go:1 AS base

RUN apt-get update && apt-get install -y vim

FROM base

RUN mkdir -p /app
WORKDIR /app
