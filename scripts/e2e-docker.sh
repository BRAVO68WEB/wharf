#!/usr/bin/env bash
# Docker CLI end-to-end against a local better-docker-registry:
#   1. pull/push roundtrip (alpine)
#   2. multi-arch buildx push + pull
#   3. 5 GB blob streaming with registry RSS < 256 MB
#
# Phases 1-2 run the registry as a Docker container: the Docker engine lives
# in a VM whose 127.0.0.1 is not the macOS host, and only published container
# ports are reachable at the VM's localhost (plain HTTP is allowed there).
# Phase 3 runs the binary as a host process so its RSS is directly measurable.
#
# Usage: scripts/e2e-docker.sh
# Evidence lands in .omo/evidence/task-22-docker-registry-htmx/.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
EVIDENCE="$ROOT/.omo/evidence/task-22-docker-registry-htmx"
RUN_DIR=$(mktemp -d /tmp/bdr-e2e-docker-XXXXXX)
PORT=${BDR_E2E_PORT:-5050}
REG=127.0.0.1:$PORT
RSS_LIMIT_KB=$((256 * 1024))
REGISTRY_PID=""

cleanup() {
  [[ -n "$REGISTRY_PID" ]] && kill "$REGISTRY_PID" 2>/dev/null || true
  docker rm -f bdr-e2e >/dev/null 2>&1 || true
  rm -rf "$RUN_DIR"
}
trap cleanup EXIT

mkdir -p "$EVIDENCE"
exec > >(tee "$EVIDENCE/e2e-docker.log") 2>&1

echo "== building registry image =="
docker build -t bdr-e2e "$ROOT"
make -C "$ROOT" build
openssl genrsa -out "$RUN_DIR/jwt_private.pem" 2048 2>/dev/null

export REGISTRY_SESSION_SECRET=e2e-session-secret-padding-32b!!
cat >"$RUN_DIR/config.yml" <<EOF
server:
  port: 5000
  read_timeout: 0s
  write_timeout: 0s
  max_upload_size: 0
storage:
  type: filesystem
  path: /var/lib/registry/blobs
database:
  path: /var/lib/registry/registry.db
auth:
  jwt:
    private_key_path: /var/lib/registry/jwt_private.pem
    issuer: better-docker-registry
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

echo "== starting registry container on $REG =="
docker rm -f bdr-e2e >/dev/null 2>&1 || true
docker run -d --name bdr-e2e -p "$PORT:5000" \
  -e REGISTRY_SESSION_SECRET \
  -v "$RUN_DIR/config.yml:/etc/registry/config.yml:ro" \
  -v "$RUN_DIR/jwt_private.pem:/var/lib/registry/jwt_private.pem:ro" \
  bdr-e2e >/dev/null
for _ in $(seq 1 50); do
  curl -sf "http://$REG/healthz" >/dev/null 2>&1 && break
  sleep 0.2
done
curl -sf "http://$REG/healthz" >/dev/null

echo
echo "== phase 1: docker pull/push roundtrip =="
docker pull alpine
docker tag alpine "$REG/t/alpine:1"
docker push "$REG/t/alpine:1"
docker rmi "$REG/t/alpine:1"
docker pull "$REG/t/alpine:1"
echo "roundtrip OK"

echo
echo "== phase 2: multi-arch buildx push =="
mkdir -p "$RUN_DIR/mbuild"
cat >"$RUN_DIR/mbuild/Dockerfile" <<'EOF'
FROM alpine
RUN echo "hello from multi-arch" > /hello
EOF
docker buildx build --platform linux/amd64,linux/arm64 \
  -t "$REG/t/multi:1" --push "$RUN_DIR/mbuild"
docker buildx imagetools inspect "$REG/t/multi:1"
docker pull "$REG/t/multi:1"
echo "multi-arch OK"

echo
echo "== phase 3: 5 GB blob streaming (RSS < 256 MB) =="
docker rm -f bdr-e2e >/dev/null
mkdir -p "$RUN_DIR/blobs"
cat >"$RUN_DIR/config-host.yml" <<EOF
server:
  port: $PORT
  read_timeout: 0s
  write_timeout: 0s
  max_upload_size: 0
storage:
  type: filesystem
  path: $RUN_DIR/blobs
database:
  path: $RUN_DIR/host.db
auth:
  jwt:
    private_key_path: $RUN_DIR/jwt_private.pem
    issuer: better-docker-registry
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
"$ROOT/bin/registry" --config "$RUN_DIR/config-host.yml" --addr "127.0.0.1:$PORT" \
  >"$RUN_DIR/registry.log" 2>&1 &
REGISTRY_PID=$!
for _ in $(seq 1 50); do
  curl -sf "http://$REG/healthz" >/dev/null 2>&1 && break
  sleep 0.2
done

BLOB="$RUN_DIR/big.bin"
echo "generating 5 GiB blob..."
dd if=/dev/zero of="$BLOB" bs=1m count=5120 status=none
DIGEST="sha256:$(shasum -a 256 "$BLOB" | awk '{print $1}')"
echo "blob digest: $DIGEST"

curl -sf -X POST -H "Content-Type: application/octet-stream" \
  -T "$BLOB" \
  "http://$REG/v2/t/big/blobs/uploads/?digest=$DIGEST" -o /dev/null
echo "pushed 5 GiB monolithically"

: >"$RUN_DIR/rss-samples.txt"
(
  while kill -0 "$REGISTRY_PID" 2>/dev/null; do
    rss=$(ps -o rss= -p "$REGISTRY_PID" 2>/dev/null | tr -d ' ')
    [[ -n "$rss" ]] && echo "$rss" >>"$RUN_DIR/rss-samples.txt"
    sleep 0.2
  done
) &
SAMPLER_PID=$!

curl -sf "http://$REG/v2/t/big/blobs/$DIGEST" -o /dev/null
sleep 1
kill "$SAMPLER_PID" 2>/dev/null || true
wait "$SAMPLER_PID" 2>/dev/null || true

MAX_RSS_KB=$(sort -n "$RUN_DIR/rss-samples.txt" | tail -1)
echo "max registry RSS during 5 GiB pull: ${MAX_RSS_KB} KB (limit ${RSS_LIMIT_KB} KB)"
cp "$RUN_DIR/rss-samples.txt" "$EVIDENCE/streaming-rss-samples.txt"
if [[ "$MAX_RSS_KB" -ge "$RSS_LIMIT_KB" ]]; then
  echo "FAIL: RSS exceeded ${RSS_LIMIT_KB} KB"
  exit 1
fi
echo "streaming OK"

echo
echo "== all docker E2E phases passed =="
