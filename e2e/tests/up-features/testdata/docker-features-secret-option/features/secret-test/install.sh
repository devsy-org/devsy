#!/bin/sh
set -e

echo "Installing secret-test feature"
echo "Secret token value: ${SECRETTOKEN}"
echo "Public option value: ${PUBLICOPTION}"

# Write the secret value to a file for verification
echo "${SECRETTOKEN}" >/secret-test-result.txt
