#!/bin/sh
set -eu

printf '%s\n' 'context-build-feature-installed' >/usr/local/bin/context-build-feature
chmod +x /usr/local/bin/context-build-feature
