#!/bin/bash
# Writes only the synthetic test variables; never dump the full environment.
phase="$1"
out="$phase.env.out"
: >"$out"
for name in PLAIN ONE_EQ TWO_EQ COMMA_EQ TRAILING_EQ FILE_ONE_EQ REMOTE_EQ; do
    printf '%s=%s\n' "$name" "${!name}" >>"$out"
done
