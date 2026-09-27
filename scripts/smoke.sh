#!/usr/bin/env bash
# End-to-end smoke test for GophProfile.
#
# Prereqs: `make up` is running (postgres, minio, kafka, migrate, kafka-init,
# minio-init, server, worker all healthy). Uses `curl`, `jq`, and `xxd`.
#
# Exits non-zero on any failure.

set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
USER_ID="${USER_ID:-smoke-user-1}"
SAMPLE="${SAMPLE:-testdata/sample.jpg}"

red()    { printf '\033[31m%s\033[0m\n' "$*"; }
green()  { printf '\033[32m%s\033[0m\n' "$*"; }
section(){ printf '\n\033[1;36m=== %s ===\033[0m\n' "$*"; }

if [[ ! -f "$SAMPLE" ]]; then
  red "Sample image not found: $SAMPLE"
  exit 1
fi

section "1. health check"
HEALTH=$(curl -sf "$BASE_URL/health")
echo "$HEALTH"
STATUS=$(echo "$HEALTH" | jq -r .status)
if [[ "$STATUS" != "ok" ]]; then
  red "health is not ok: $STATUS"
  exit 1
fi
green "health OK"

section "2. upload avatar"
UPLOAD=$(curl -sf -X POST "$BASE_URL/api/v1/avatars" \
  -H "X-User-ID: $USER_ID" \
  -F "file=@$SAMPLE")
echo "$UPLOAD"
AVATAR_ID=$(echo "$UPLOAD" | jq -r .id)
if [[ -z "$AVATAR_ID" || "$AVATAR_ID" == "null" ]]; then
  red "no avatar id in upload response"
  exit 1
fi
green "uploaded avatar id=$AVATAR_ID"

section "3. wait for worker to process (≤10s)"
DEADLINE=$(( $(date +%s) + 10 ))
STATUS="pending"
while [[ $(date +%s) -lt $DEADLINE ]]; do
  META=$(curl -sf "$BASE_URL/api/v1/avatars/$AVATAR_ID/metadata" || echo "")
  if [[ -n "$META" ]]; then
    STATUS=$(echo "$META" | jq -r .processing_status)
    [[ "$STATUS" == "completed" ]] && break
  fi
  sleep 1
done
echo "$META" | jq .
if [[ "$STATUS" != "completed" ]]; then
  red "processing did not complete in time (status=$STATUS)"
  exit 1
fi
green "processing completed"

THUMB_COUNT=$(echo "$META" | jq '.thumbnails | length')
if [[ "$THUMB_COUNT" -lt 2 ]]; then
  red "expected at least 2 thumbnails, got $THUMB_COUNT"
  exit 1
fi
green "thumbnails: $THUMB_COUNT"

section "4. fetch thumbnail (100x100)"
# GET (not HEAD — chi router returns 405 for HEAD on GET-only routes).
# Note: server uses chunked encoding for binary, so trust the file size not
# Content-Length.
TMP_THUMB=$(mktemp)
HEADERS=$(curl -sf -D - -o "$TMP_THUMB" "$BASE_URL/api/v1/avatars/$AVATAR_ID?size=100x100")
echo "$HEADERS" | head -10
CT=$(echo "$HEADERS" | grep -i '^content-type:' | awk '{print $2}' | tr -d '\r')
SIZE=$(stat -c %s "$TMP_THUMB" 2>/dev/null || echo 0)
ETAG=$(echo "$HEADERS" | grep -i '^etag:' | awk '{print $2}' | tr -d '\r')
rm -f "$TMP_THUMB"
if [[ "$CT" != "image/jpeg" ]]; then
  red "expected image/jpeg, got $CT"
  exit 1
fi
if [[ "$SIZE" -le 0 ]]; then
  red "expected non-empty body, got $SIZE bytes"
  exit 1
fi
if [[ -z "$ETAG" ]]; then
  red "expected ETag header"
  exit 1
fi
green "thumbnail served: $CT, body $SIZE bytes, ETag $ETAG"

section "5. fetch user avatar"
TMP_USER=$(mktemp)
USER_HEAD=$(curl -sf -D - -o "$TMP_USER" "$BASE_URL/api/v1/users/$USER_ID/avatar")
USER_SIZE=$(stat -c %s "$TMP_USER" 2>/dev/null || echo 0)
rm -f "$TMP_USER"
if [[ "$USER_SIZE" -le 0 ]]; then
  red "user avatar not served"
  exit 1
fi
green "user avatar served: body $USER_SIZE bytes"

section "6. list user avatars"
LIST=$(curl -sf "$BASE_URL/api/v1/users/$USER_ID/avatars")
COUNT=$(echo "$LIST" | jq '.items | length')
if [[ "$COUNT" -lt 1 ]]; then
  red "expected at least 1 avatar, got $COUNT"
  exit 1
fi
green "listed $COUNT avatars"

section "7. delete avatar"
curl -sf -X DELETE "$BASE_URL/api/v1/avatars/$AVATAR_ID" -H "X-User-ID: $USER_ID"
echo "deleted (no content)"
green "delete OK"

section "8. fetch after delete should be 404"
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/api/v1/avatars/$AVATAR_ID")
if [[ "$STATUS" != "404" ]]; then
  red "expected 404, got $STATUS"
  exit 1
fi
green "404 OK"

echo
green "ALL SMOKE TESTS PASSED"
