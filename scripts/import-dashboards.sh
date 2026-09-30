#!/bin/bash
# Импортирует dashboards из deploy/otel/grafana-provisioning/dashboards/
# в запущенную Grafana через HTTP API.
#
# Использование:
#   make grafana-import
#   GRAFANA_URL=http://remote:3000 make grafana-import

set -euo pipefail

GRAFANA_URL="${GRAFANA_URL:-http://localhost:3000}"
GRAFANA_USER="${GRAFANA_USER:-admin}"
GRAFANA_PASSWORD="${GRAFANA_PASSWORD:-admin}"
DASHBOARDS_DIR="${DASHBOARDS_DIR:-$(cd "$(dirname "$0")/.." && pwd)/deploy/otel/grafana-provisioning/dashboards}"
PROVISION_DS="${PROVISION_DS:-true}"
PROVISION_RULES="${PROVISION_RULES:-true}"

# Цвета для логов.
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log()  { printf "${GREEN}[import]${NC} %s\n" "$*"; }
warn() { printf "${YELLOW}[import]${NC} %s\n" "$*"; }
err()  { printf "${RED}[import]${NC} %s\n" "$*" >&2; }

# Ждём, пока Grafana станет доступной (до 60 секунд).
log "ожидаю Grafana по адресу $GRAFANA_URL..."
for i in $(seq 1 30); do
  if curl -sf -u "$GRAFANA_USER:$GRAFANA_PASSWORD" "$GRAFANA_URL/api/health" >/dev/null 2>&1; then
    log "Grafana доступна"
    break
  fi
  if [ "$i" -eq 30 ]; then
    err "Grafana не ответила за 60 секунд"
    exit 1
  fi
  sleep 2
done

auth=(-u "$GRAFANA_USER:$GRAFANA_PASSWORD")
base="$GRAFANA_URL/api"

# Создаём папку GophProfile.
log "создаю папку GophProfile"
curl -sf "${auth[@]}" -X POST "$base/folders" \
  -H "Content-Type: application/json" \
  -d '{"uid":"gophprofile","title":"GophProfile"}' >/dev/null 2>&1 || true

# Убеждаемся, что datasource Prometheus существует. Если нет — создаём.
DS_UID="PBFA97CFB590B2093"
if ! curl -sf "${auth[@]}" "$base/datasources/uid/$DS_UID" >/dev/null 2>&1; then
  log "создаю datasource Prometheus (uid=$DS_UID)"
  curl -sf "${auth[@]}" -X POST "$base/datasources" \
    -H "Content-Type: application/json" \
    -d "{\"uid\":\"$DS_UID\",\"name\":\"Prometheus\",\"type\":\"prometheus\",\"access\":\"proxy\",\"url\":\"http://prometheus:9090\",\"isDefault\":true,\"jsonData\":{\"timeInterval\":\"15s\"}}" \
    >/dev/null 2>&1 || warn "не удалось создать datasource"
else
  log "datasource Prometheus уже существует"
fi

# Убеждаемся, что datasource Jaeger существует.
JAEGER_UID="PC9A941E8F2E49454"
if ! curl -sf "${auth[@]}" "$base/datasources/uid/$JAEGER_UID" >/dev/null 2>&1; then
  log "создаю datasource Jaeger (uid=$JAEGER_UID)"
  curl -sf "${auth[@]}" -X POST "$base/datasources" \
    -H "Content-Type: application/json" \
    -d "{\"uid\":\"$JAEGER_UID\",\"name\":\"Jaeger\",\"type\":\"jaeger\",\"access\":\"proxy\",\"url\":\"http://jaeger:16686\",\"jsonData\":{\"serviceMapEnabled\":true,\"tracesToLogsV2\":{\"datasourceUid\":\"prometheus\",\"filterBySpanID\":false,\"filterByTraceID\":false}}}" \
    >/dev/null 2>&1 || warn "не удалось создать Jaeger datasource"
fi

# Импортируем dashboards. На каждый dashboard формируем индивидуальный POST
# с правильной структурой {dashboard, folderUid, overwrite}.
log "импортирую dashboards из $DASHBOARDS_DIR"
total=0
ok=0
fail=0

shopt -s nullglob
for f in "$DASHBOARDS_DIR"/*.json; do
  total=$((total+1))
  name=$(basename "$f")
  # Каждый dashboard в файле — один объект.
  # Поддерживаем три формы:
  #   1. { "items": [ {...}, {...} ] }   (provisioning apiVersion: 1)
  #   2. [ {...}, {...} ]                (массив dashboards)
  #   3. { ... }                          (одиночный dashboard)
  count=$(jq 'if type == "array" then length elif .items then (.items | length) else 1 end' "$f")
  if [ "$count" -eq 0 ]; then
    warn "$name: пустой файл"
    continue
  fi

  for ((i = 0; i < count; i++)); do
    # Извлекаем dashboard-объект.
    payload=$(jq -c --argjson idx "$i" '
      if type == "array" then .[$idx]
      elif .items then .items[$idx]
      else . end
      | {dashboard: ., folderUid: "gophprofile", overwrite: true}
    ' "$f")
    title=$(echo "$payload" | jq -r '.dashboard.title // "(no title)"')

    # Импортируем.
    resp=$(curl -sS "${auth[@]}" -X POST \
      -H "Content-Type: application/json" \
      -d "$payload" \
      "$base/dashboards/import" 2>&1) || true

    # Парсим JSON-ответ, проверяем imported=true.
    if echo "$resp" | jq -e '.imported == true' >/dev/null 2>&1; then
      log "ok: $title"
      ok=$((ok+1))
    else
      err "failed: $title — $resp"
      fail=$((fail+1))
    fi
  done
done
shopt -u nullglob

# Итог.
log "итого: $ok импортировано, $fail ошибок (из $total файлов)"
if [ "$fail" -gt 0 ]; then
  exit 1
fi
