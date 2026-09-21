#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
kc="$root/deploy/keycloak/keycloak-24.0.5.tar.gz"
jar="$root/deploy/sqs/elasticmq-server-all-1.6.16.jar"

if [[ ! -f "$kc" ]]; then
  echo "downloading Keycloak 24.0.5..."
  curl -fL -o "$kc" "https://github.com/keycloak/keycloak/releases/download/24.0.5/keycloak-24.0.5.tar.gz"
fi
if [[ ! -f "$jar" ]]; then
  echo "downloading ElasticMQ 1.6.16..."
  curl -fL -o "$jar" "https://github.com/softwaremill/elasticmq/releases/download/v1.6.16/elasticmq-server-all-1.6.16.jar"
fi
ls -lh "$kc" "$jar"
