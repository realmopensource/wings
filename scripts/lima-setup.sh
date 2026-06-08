#!/usr/bin/env bash
# Build Wings inside the Lima VM and start the systemd service.
set -euo pipefail

INSTANCE="${LIMA_INSTANCE:-wings}"
WINGS_SRC="${WINGS_SRC:-$HOME/Downloads/realmctl/wings}"
# Inside Lima the virtiofs mount uses the macOS path, not $HOME
LIMA_WINGS_SRC="/Users/$(whoami)/Downloads/realmctl/wings"

echo "==> Building Wings in Lima instance '${INSTANCE}'..."

limactl shell "$INSTANCE" -- bash -s <<EOF
set -euo pipefail

WINGS_SRC="${WINGS_SRC}"
LIMA_WINGS_SRC="${LIMA_WINGS_SRC}"

if [ -f "\${LIMA_WINGS_SRC}/wings.go" ]; then
  WINGS_SRC="\${LIMA_WINGS_SRC}"
fi

# Lima host (Mac) is the default gateway from inside the VM
LIMA_HOST_IP=\$(ip route show default | awk '{print \$3}')
if [ -z "\${LIMA_HOST_IP}" ]; then
  LIMA_HOST_IP="host.lima.internal"
fi

if [ ! -f "\${WINGS_SRC}/wings.go" ]; then
  echo "ERROR: wings source not found at \${WINGS_SRC}" >&2
  echo "Check the mount in lima-wings.yaml matches your repo path." >&2
  exit 1
fi

echo "==> Compiling Wings..."
cd "\${WINGS_SRC}"
go build -ldflags="-s -w" -o /tmp/wings wings.go
sudo install -m 755 /tmp/wings /usr/local/bin/wings
/usr/local/bin/wings version

echo "==> Installing config..."
sudo mkdir -p /etc/realm
sudo cp "\${WINGS_SRC}/config.yml" /etc/realm/config.yml

# Wings must reach the panel on the Mac host via Lima's user-v2 network gateway.
sudo sed -i "s|^remote:.*|remote: 'http://\${LIMA_HOST_IP}:8000'|" /etc/realm/config.yml

echo "==> Starting Wings service..."
sudo systemctl enable wings
sudo systemctl restart wings

for i in 1 2 3 4 5; do
  if sudo systemctl is-active wings >/dev/null 2>&1; then
    break
  fi
  sleep 2
done
sudo systemctl is-active wings

VM_IP=\$(hostname -I | awk '{print \$1}')
echo ""
echo "Wings is running."
echo "  VM IP:     \${VM_IP}"
echo "  API:       http://\${VM_IP}:8080"
echo "  SFTP:      \${VM_IP}:2022"
echo "  Panel URL: http://\${LIMA_HOST_IP}:8000 (remote in config.yml)"
echo ""
echo "Panel node FQDN: 127.0.0.1 (via Lima port-forward from Mac)"
echo "VM internal IP:  \${VM_IP} (only reachable inside Lima network)"
EOF

echo ""
echo "==> Verifying API from Mac host..."
VM_IP=$(limactl shell "$INSTANCE" -- hostname -I | awk '{print $1}')
TOKEN_ID=$(limactl shell "$INSTANCE" -- awk '/^token_id:/ {print $2}' /etc/realm/config.yml)
TOKEN=$(limactl shell "$INSTANCE" -- awk '/^token:/ {print $2}' /etc/realm/config.yml)

check_api() {
  local url="$1"
  curl -s -o /dev/null -w "%{http_code}" \
    -H "Authorization: Bearer ${TOKEN_ID}.${TOKEN}" \
    --connect-timeout 5 "${url}/api/system" 2>/dev/null || echo "000"
}

HTTP_CODE=$(check_api "http://${VM_IP}:8080")
if [ "$HTTP_CODE" != "200" ]; then
  HTTP_CODE=$(check_api "http://127.0.0.1:8080")
  VM_IP="127.0.0.1 (port-forward)"
fi

if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "401" ] || [ "$HTTP_CODE" = "403" ]; then
  echo "OK: Wings API reachable at http://${VM_IP}:8080 (HTTP ${HTTP_CODE})"
elif limactl shell "$INSTANCE" -- sudo systemctl is-active wings >/dev/null 2>&1; then
  echo "OK: Wings service is active (API check returned HTTP ${HTTP_CODE})"
else
  echo "WARN: Wings not healthy. Check logs with: make lima-logs"
  exit 1
fi
