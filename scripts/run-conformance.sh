#!/usr/bin/env bash
# Run the OCI Distribution Spec conformance suite against a local
# Wharf instance.
#
# Usage: scripts/run-conformance.sh <filesystem|minio>
#
# Evidence lands in .omo/evidence/task-22-docker-registry-htmx/.
set -euo pipefail

BACKEND=${1:?usage: run-conformance.sh <filesystem|minio>}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
EVIDENCE="$ROOT/.omo/evidence/task-22-docker-registry-htmx"
CONF_DIR="${BDR_CONFORMANCE_DIR:-$HOME/.cache/bdr-distribution-spec}"
PORT=${BDR_E2E_PORT:-5050}
RUN_DIR=$(mktemp -d /tmp/bdr-conformance-XXXXXX)
MINIO_PID=""

cleanup() {
  [[ -n "${REGISTRY_PID:-}" ]] && kill "${REGISTRY_PID}" 2>/dev/null || true
  [[ -n "$MINIO_PID" ]] && kill "$MINIO_PID" 2>/dev/null || true
  rm -rf "$RUN_DIR"
}
trap cleanup EXIT

mkdir -p "$EVIDENCE"

echo "== building registry =="
make -C "$ROOT" build

openssl genrsa -out "$RUN_DIR/jwt_private.pem" 2048 2>/dev/null

export REGISTRY_SESSION_SECRET=e2e-session-secret-padding-32b!!
export REGISTRY_S3_ACCESS_KEY=wharf-e2e-access
export REGISTRY_S3_SECRET_KEY=wharf-e2e-secret

if [[ "$BACKEND" == "minio" ]]; then
  echo "== starting minio =="
  mkdir -p "$RUN_DIR/minio-data"
  MINIO_ROOT_USER=$REGISTRY_S3_ACCESS_KEY MINIO_ROOT_PASSWORD=$REGISTRY_S3_SECRET_KEY \
    minio server "$RUN_DIR/minio-data" --address 127.0.0.1:9000 --console-address 127.0.0.1:9001 \
    >"$RUN_DIR/minio.log" 2>&1 &
  MINIO_PID=$!
  for _ in $(seq 1 50); do
    curl -sf http://127.0.0.1:9000/minio/health/live >/dev/null 2>&1 && break
    sleep 0.2
  done
  mc alias set wharf-local http://127.0.0.1:9000 "$REGISTRY_S3_ACCESS_KEY" "$REGISTRY_S3_SECRET_KEY" >/dev/null
  mc mb --ignore-existing wharf-local/registry-blobs >/dev/null
  cat >"$RUN_DIR/config.yml" <<EOF
server:
  port: $PORT
  read_timeout: 0s
  write_timeout: 0s
  max_upload_size: 0
storage:
  type: s3
  s3:
    endpoint: http://127.0.0.1:9000
    region: us-east-1
    bucket: registry-blobs
    access_key_env: REGISTRY_S3_ACCESS_KEY
    secret_key_env: REGISTRY_S3_SECRET_KEY
    force_path_style: true
database:
  path: $RUN_DIR/registry.db
auth:
  jwt:
    private_key_path: $RUN_DIR/jwt_private.pem
    issuer: wharf
    service: registry.docker.io
    token_ttl: 15m
  session:
    secret_env: REGISTRY_SESSION_SECRET
    ttl: 24h
  dev_anonymous: true
gc:
  batch_size: 256
logging:
  level: info
EOF
else
  mkdir -p "$RUN_DIR/blobs"
  cat >"$RUN_DIR/config.yml" <<EOF
server:
  port: $PORT
  read_timeout: 0s
  write_timeout: 0s
  max_upload_size: 0
storage:
  type: filesystem
  path: $RUN_DIR/blobs
database:
  path: $RUN_DIR/registry.db
auth:
  jwt:
    private_key_path: $RUN_DIR/jwt_private.pem
    issuer: wharf
    service: registry.docker.io
    token_ttl: 15m
  session:
    secret_env: REGISTRY_SESSION_SECRET
    ttl: 24h
  dev_anonymous: true
gc:
  batch_size: 256
logging:
  level: info
EOF
fi

echo "== starting registry ($BACKEND) on 127.0.0.1:$PORT =="
"$ROOT/bin/wharf" --config "$RUN_DIR/config.yml" --addr "127.0.0.1:$PORT" \
  >"$RUN_DIR/registry.log" 2>&1 &
REGISTRY_PID=$!
for _ in $(seq 1 50); do
  curl -sf "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1 && break
  sleep 0.2
done
curl -sf "http://127.0.0.1:$PORT/healthz" >/dev/null

if [[ ! -d "$CONF_DIR/.git" ]]; then
  echo "== cloning opencontainers/distribution-spec =="
  git clone --depth 1 https://github.com/opencontainers/distribution-spec.git "$CONF_DIR"
fi

echo "== running conformance suite against $BACKEND =="
set +e
(
  cd "$CONF_DIR/conformance"
  OCI_ROOT_URL="http://127.0.0.1:$PORT" \
  OCI_NAMESPACE=conformance/test \
  OCI_TEST_PULL=1 \
  OCI_TEST_PUSH=1 \
  OCI_DELETE=1 \
  OCI_CROSSMOUNT_NAMESPACE=conformance/other \
  OCI_RESULTS_DIR="$RUN_DIR/results" \
  go run .
) 2>&1 | tee "$EVIDENCE/conformance-$BACKEND.log"
STATUS=${PIPESTATUS[0]}
set -e

cp -R "$RUN_DIR/results" "$EVIDENCE/conformance-$BACKEND-results" 2>/dev/null || true
cp "$RUN_DIR/registry.log" "$EVIDENCE/registry-$BACKEND.log" 2>/dev/null || true

echo "== conformance ($BACKEND) exit status: $STATUS =="
exit "$STATUS"
