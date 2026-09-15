#!/usr/bin/env bash
# Run the full stack as background processes inside this shell, then exercise
# it via scripts/smoke.sh. Everything is killed at the end.
set -e

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# Common env
export DATABASE_DSN="postgres://app:app@localhost:5432/gophprofile?sslmode=disable"
export S3_ENDPOINT="http://localhost:9000"
export S3_REGION="us-east-1"
export S3_BUCKET="avatars"
export S3_ACCESS_KEY="gophprof"
export S3_SECRET_KEY="gophprof-secret-1"
export S3_USE_SSL="false"
export S3_PUBLIC_BASE_URL="http://localhost:9000/avatars"
export KAFKA_BROKERS="localhost:9092"
export KAFKA_TOPIC="avatar-events"
export KAFKA_CONSUMER_GROUP="gophprofile-worker"
export SERVER_ADDRESS=":18080"
export BASE_URL="http://localhost:18080"
export LOG_LEVEL="info"
export MAX_UPLOAD_SIZE="10485760"
export THUMBNAIL_SIZES="100x100,300x300"

# Start worker (consumes Kafka)
./bin/worker >/tmp/worker.log 2>&1 &
WORKER_PID=$!

# Start server (HTTP API)
./bin/server >/tmp/server.log 2>&1 &
SERVER_PID=$!

cleanup() {
  kill "$WORKER_PID" "$SERVER_PID" 2>/dev/null || true
  wait 2>/dev/null || true
}
trap cleanup EXIT

# Wait for server to be ready
for i in $(seq 1 30); do
  if curl -sf http://localhost:18080/health >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

if ! curl -sf http://localhost:18080/health >/dev/null 2>&1; then
  echo "server did not become ready"
  echo "--- server log ---"; cat /tmp/server.log
  echo "--- worker log ---"; cat /tmp/worker.log
  exit 1
fi

echo "================== SMOKE TEST =================="
BASE_URL="http://localhost:18080" USER_ID="smoke-$(date +%s)" bash scripts/smoke.sh
echo "================== END SMOKE ==================="

echo "--- worker log tail ---"
tail -30 /tmp/worker.log
