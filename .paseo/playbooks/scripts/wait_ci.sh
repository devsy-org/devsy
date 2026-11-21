#!/bin/sh
set -eu
exec mise run playbooks:wait-ci -- "$@"
