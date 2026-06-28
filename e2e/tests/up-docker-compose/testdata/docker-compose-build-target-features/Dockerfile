FROM ghcr.io/devsy-org/test-images/go:1 AS base

RUN echo "base-stage" > /stage-marker.txt

FROM base AS dev

RUN echo "dev-stage" > /stage-marker.txt
RUN mkdir -p /app
WORKDIR /app
