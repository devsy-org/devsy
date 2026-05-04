#!/bin/bash
set -e

echo "Installing current-feature"

cat >/usr/local/bin/legacy-resolved <<'EOF'
#!/bin/bash
echo "legacy-id-resolved-successfully"
EOF

chmod +x /usr/local/bin/legacy-resolved
