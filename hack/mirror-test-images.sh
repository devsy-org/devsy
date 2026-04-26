#!/usr/bin/env bash
set -euo pipefail

# Mirror upstream images used by e2e tests into ghcr.io/devsy-org/test-images/.
# Run this whenever an upstream tag is bumped or a new image is added.
#
# Prerequisites:
#   go install github.com/google/go-containerregistry/cmd/crane@latest
#   echo "$GHCR_TOKEN" | crane auth login ghcr.io -u "$GHCR_USER" --password-stdin

crane copy golang:1                            ghcr.io/devsy-org/test-images/go:1
crane copy ubuntu:latest                       ghcr.io/devsy-org/test-images/base:ubuntu
crane copy alpine:latest                       ghcr.io/devsy-org/test-images/base:alpine
crane copy python:latest                       ghcr.io/devsy-org/test-images/python:latest
crane copy node:lts-alpine                     ghcr.io/devsy-org/test-images/node:lts-alpine
crane copy postgres:latest                     ghcr.io/devsy-org/test-images/postgres:latest
crane copy nginxinc/nginx-unprivileged:latest  ghcr.io/devsy-org/test-images/nginx-unprivileged:latest
crane copy docker:dind                         ghcr.io/devsy-org/test-images/docker:dind
