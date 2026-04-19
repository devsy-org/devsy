#!/usr/bin/env bash
# Mirror upstream test images to ghcr.io/devsy-org/test-images/
# to avoid pull rate limits and ensure consistency across CI.
#
# Prerequisites:
#   echo $GHCR_TOKEN | docker login ghcr.io -u USERNAME --password-stdin
#
set -euo pipefail

REGISTRY="ghcr.io/devsy-org/test-images"

declare -A IMAGES=(
  ["mcr.microsoft.com/devcontainers/go:1"]="${REGISTRY}/go:1"
  ["mcr.microsoft.com/devcontainers/base:ubuntu"]="${REGISTRY}/base:ubuntu"
  ["mcr.microsoft.com/devcontainers/base:alpine"]="${REGISTRY}/base:alpine"
  ["mcr.microsoft.com/devcontainers/python:latest"]="${REGISTRY}/python:latest"
  ["docker.io/library/node:lts-alpine"]="${REGISTRY}/node:lts-alpine"
)

for src in "${!IMAGES[@]}"; do
  dst="${IMAGES[$src]}"
  echo "==> Mirroring ${src} -> ${dst}"
  docker pull "${src}"
  docker tag "${src}" "${dst}"
  docker push "${dst}"
done

echo "Done. All test images mirrored to ${REGISTRY}/"
