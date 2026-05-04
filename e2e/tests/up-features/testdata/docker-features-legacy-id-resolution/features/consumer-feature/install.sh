#!/bin/bash
set -e

echo "Installing consumer-feature"

cat >/usr/local/bin/test-legacy-resolution <<'EOF'
#!/bin/bash
if command -v legacy-resolved >/dev/null 2>&1; then
    echo "SUCCESS: legacy ID resolution worked"
    legacy-resolved
else
    echo "FAILURE: legacy-resolved command not found"
    exit 1
fi
EOF

chmod +x /usr/local/bin/test-legacy-resolution
