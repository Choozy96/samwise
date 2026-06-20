#!/bin/sh
# One-time Let's Encrypt bootstrap for the nginx (tls profile) reverse proxy.
#
# nginx won't start if its TLS server block points at a cert that doesn't exist
# yet, and certbot can't get a cert until nginx is serving the ACME challenge —
# a chicken-and-egg. This script breaks it: drop in a throwaway self-signed cert
# so nginx can boot, start nginx, then swap in a real Let's Encrypt cert.
#
# Run once on the VPS from the repo root, after setting SITE_ADDRESS (and
# ideally CERTBOT_EMAIL) in .env:
#     sh nginx/init-letsencrypt.sh
# Afterwards `docker compose --profile tls up -d` keeps nginx + auto-renewal
# running. Set CERTBOT_STAGING=1 in .env while testing to avoid rate limits.
set -e

[ -f .env ] && { set -a; . ./.env; set +a; }

if [ -z "$SITE_ADDRESS" ]; then
    echo "ERROR: set SITE_ADDRESS=your-domain in .env first." >&2
    exit 1
fi

domain="$SITE_ADDRESS"
compose="docker compose --profile tls"
live="/etc/letsencrypt/live/$domain"

email_arg="--register-unsafely-without-email"
[ -n "$CERTBOT_EMAIL" ] && email_arg="--email $CERTBOT_EMAIL"
staging_arg=""
[ "${CERTBOT_STAGING:-0}" != "0" ] && staging_arg="--staging"

echo "### 1/4 temporary self-signed cert for $domain (so nginx can start)"
$compose run --rm --entrypoint sh certbot -c "\
    mkdir -p '$live' && \
    openssl req -x509 -nodes -newkey rsa:2048 -days 1 \
        -keyout '$live/privkey.pem' -out '$live/fullchain.pem' -subj '/CN=$domain'"

echo "### 2/4 starting nginx"
$compose up -d nginx

echo "### 3/4 requesting the real certificate from Let's Encrypt"
$compose run --rm --entrypoint sh certbot -c "\
    rm -rf /etc/letsencrypt/live/$domain /etc/letsencrypt/archive/$domain /etc/letsencrypt/renewal/$domain.conf && \
    certbot certonly --webroot -w /var/www/certbot \
        $staging_arg $email_arg -d '$domain' \
        --agree-tos --no-eff-email --non-interactive"

echo "### 4/4 reloading nginx with the real cert"
$compose exec nginx nginx -s reload

echo
echo "Done. Bring the stack up with:  $compose up -d"
echo "Then browse:  https://$domain"
