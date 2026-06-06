#!/usr/bin/env bash
# Run on the Wings VPS as root.
set -euo pipefail

DOMAIN="${1:-wings.stijn.wtf}"
WINGS_CONFIG="${WINGS_CONFIG:-/etc/realm/config.yml}"
NGINX_SITE="/etc/nginx/sites-available/${DOMAIN}"

if [[ "$(id -u)" -ne 0 ]]; then
  echo "Run as root on the VPS." >&2
  exit 1
fi

apt-get update
apt-get install -y nginx certbot python3-certbot-nginx

# Disable Wings native SSL; nginx terminates TLS on 443.
if [[ -f "${WINGS_CONFIG}" ]]; then
  if grep -q 'enabled: true' "${WINGS_CONFIG}" && grep -q 'ssl:' -A3 "${WINGS_CONFIG}"; then
    sed -i '/ssl:/,/upload_limit:/ s/enabled: true/enabled: false/' "${WINGS_CONFIG}"
    echo "Set api.ssl.enabled: false in ${WINGS_CONFIG}"
  fi
  systemctl restart wings
fi

cat > "${NGINX_SITE}" <<EOF
map \$http_upgrade \$connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen 80;
    listen [::]:80;
    server_name ${DOMAIN};
    return 301 https://\$host\$request_uri;
}

server {
    listen 443 ssl;
    listen [::]:443 ssl;
    server_name ${DOMAIN};

    ssl_certificate     /etc/letsencrypt/live/${DOMAIN}/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/${DOMAIN}/privkey.pem;
    include /etc/letsencrypt/options-ssl-nginx.conf;
    ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;

    client_max_body_size 100m;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;

        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;

        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \$connection_upgrade;

        proxy_connect_timeout 60s;
        proxy_send_timeout 3600s;
        proxy_read_timeout 3600s;
        proxy_buffering off;
    }
}
EOF

ln -sf "${NGINX_SITE}" "/etc/nginx/sites-enabled/${DOMAIN}"
rm -f /etc/nginx/sites-enabled/default

nginx -t
systemctl enable nginx
systemctl reload nginx

certbot --nginx -d "${DOMAIN}" --non-interactive --agree-tos -m "admin@${DOMAIN#*.}" || {
  echo "Certbot failed. Run manually: certbot --nginx -d ${DOMAIN}" >&2
}

nginx -t && systemctl reload nginx

echo "Done. Verify: curl -I https://${DOMAIN}/api/system"
echo "Panel node: scheme=https, port=443, behind_proxy=yes"
