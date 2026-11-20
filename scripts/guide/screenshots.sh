#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Dave O'Connor
#
# Retakes the screenshots in docs/user-guide.md. It runs andante, two made-up
# serials and Miniflux in Docker on a network of their own, publishes no ports,
# and drives a browser through them with Playwright. Everything is removed after.
#
#   scripts/guide/screenshots.sh            # build andante:dev and retake them all
#   IMAGE=ghcr.io/gerrowadat/andante:0.2.1 scripts/guide/screenshots.sh
#   KEEP=1 scripts/guide/screenshots.sh     # leave the stack up afterwards
set -euo pipefail

cd "$(dirname "$0")/../.."
here=scripts/guide
out=docs/images/guide
image=${IMAGE:-}
playwright=mcr.microsoft.com/playwright/python:v1.49.0-noble
miniflux=miniflux/miniflux:2.3.3
net=andante-guide
token=guide-bootstrap-token-0123456789abcdef
catalogue_token=guide-catalogue-token

if [[ -z $image ]]; then
  make docker >/dev/null
  image=andante:dev
fi

cleanup() {
  docker rm -f guide-postgres guide-catalogue guide-andante guide-comic guide-podcast guide-miniflux guide-proxy >/dev/null 2>&1 || true
  docker network rm "$net" >/dev/null 2>&1 || true
  rm -rf "$sites"
}
sites=$(mktemp -d)
cleanup
if [[ -z ${KEEP:-} ]]; then
  trap cleanup EXIT
fi

python3 "$here/make_site.py" "$sites"
chmod -R a+rX "$sites"
docker network create "$net" >/dev/null

docker run -d --name guide-postgres --network "$net" \
  -e POSTGRES_USER=andante -e POSTGRES_PASSWORD=andante -e POSTGRES_DB=andante \
  -v "$PWD/deploy/compose/dev-catalogue.sql:/docker-entrypoint-initdb.d/catalogue.sql:ro" \
  postgres:18 >/dev/null
until docker exec guide-postgres pg_isready -U andante -d catalogue -h 127.0.0.1 >/dev/null 2>&1; do sleep 1; done
docker exec guide-postgres psql -qU andante -c 'CREATE DATABASE miniflux' >/dev/null

for s in comic:gannetpoint.example podcast:tidetables.example; do
  docker run -d --name "guide-${s%%:*}" --network "$net" --network-alias "${s#*:}" \
    -v "$sites/${s%%:*}:/srv:ro" -w /srv "$playwright" python3 -m http.server 80 >/dev/null
done

# The crawl interval is short because these sites are ours. Never do that to anyone else's.
docker run -d --name guide-catalogue --network "$net" --network-alias catalogue \
  --entrypoint /usr/local/bin/andante-catalogue \
  -e ANDANTE_CATALOGUE_DATABASE_URL='postgres://andante:andante@guide-postgres:5432/catalogue?sslmode=disable' \
  -e ANDANTE_CATALOGUE_LISTEN=:8082 -e ANDANTE_CATALOGUE_TOKEN="$catalogue_token" \
  -e ANDANTE_BASE_URL=https://andante.example \
  -e ANDANTE_CRAWL_ALLOW_PRIVATE=1 -e ANDANTE_CRAWL_INTERVAL=200ms -e ANDANTE_CRAWL_POLL_INTERVAL=1s \
  "$image" >/dev/null
docker run -d --name guide-andante --network "$net" \
  -e ANDANTE_DATABASE_URL='postgres://andante:andante@guide-postgres:5432/andante?sslmode=disable' \
  -e ANDANTE_BASE_URL=https://andante.example -e ANDANTE_SECRET=guide-secret-for-screenshots-only \
  -e ANDANTE_CATALOGUE_URL=http://catalogue:8082/v1 -e ANDANTE_CATALOGUE_TOKEN="$catalogue_token" \
  -e ANDANTE_BOOTSTRAP_TOKEN="$token" -e ANDANTE_DEFAULT_TIMEZONE=Europe/Dublin \
  "$image" >/dev/null
docker run -d --name guide-miniflux --network "$net" --network-alias reader.example \
  -e DATABASE_URL='postgres://andante:andante@guide-postgres:5432/miniflux?sslmode=disable' \
  -e RUN_MIGRATIONS=1 -e CREATE_ADMIN=1 -e ADMIN_USERNAME=sam -e ADMIN_PASSWORD=guide-password \
  -e LISTEN_ADDR=0.0.0.0:80 -e BASE_URL=http://reader.example/ -e FETCHER_ALLOW_PRIVATE_NETWORKS=1 \
  "$miniflux" >/dev/null

# Traefik puts a self-signed certificate in front of andante, because the website wants
# a browser's Sec-Fetch-Site header, which Chrome only sends to https.
cat >"$sites/proxy.yaml" <<'EOF2'
http:
  routers:
    andante: {rule: "PathPrefix(`/`)", service: andante, tls: {}}
  services:
    andante: {loadBalancer: {servers: [{url: "http://guide-andante:8080"}]}}
EOF2
docker run -d --name guide-proxy --network "$net" --network-alias andante.example \
  -v "$sites/proxy.yaml:/proxy.yaml:ro" traefik:v3.7.13 \
  --entrypoints.https.address=:443 --providers.file.filename=/proxy.yaml >/dev/null

mkdir -p "$out"
docker run --rm --network "$net" --user "$(id -u):$(id -g)" -e HOME=/tmp \
  -e BOOTSTRAP_TOKEN="$token" \
  -v "$PWD/$here:/guide:ro" -v "$PWD/$out:/out" \
  "$playwright" sh -c 'pip install -q --user --break-system-packages playwright==1.49.0 && python3 /guide/shoot.py /out'
