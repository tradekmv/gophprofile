#!/bin/bash
# Ручной provisioning Grafana dashboards/alert rules.
# Запускать вручную после `docker compose up -d` (или дождаться grafana-init).
set -e

GRAFANA_URL="${GRAFANA_URL:-http://localhost:3000}"
USER="${GRAFANA_USER:-admin}"
PASS="${GRAFANA_PASSWORD:-admin}"
DASHBOARDS_DIR="$(dirname "$0")/../deploy/otel/grafana-provisioning/dashboards"
RULES_FILE="$(dirname "$0")/../deploy/otel/grafana-provisioning/alerting/rules.json"

echo "Provisioning Grafana..."

# Ждём пока Grafana станет доступной
for i in $(seq 1 30); do
  if curl -sf -u "$USER:$PASS" "$GRAFANA_URL/api/health" >/dev/null 2>&1; then
    break
  fi
  sleep 2
done

# Папка
curl -sf -u "$USER:$PASS" -X POST "$GRAFANA_URL/api/folders" \
  -H "Content-Type: application/json" \
  -d '{"uid":"gophprofile","title":"GophProfile"}' >/dev/null 2>&1 || true

# Dashboards (YAML provisioning format)
for f in "$DASHBOARDS_DIR"/*.yaml; do
  [ -f "$f" ] || continue
  name=$(basename "$f" .yaml)
  echo "importing $name"
  python3 -c "
import json, subprocess, sys
import yaml
with open('$f') as fh: doc = yaml.safe_load(fh)
dash = doc['items'][0]
payload = {'dashboard': dash, 'overwrite': True, 'folderUid': 'gophprofile'}
r = subprocess.run(
    ['curl', '-sf', '-u', '$USER:$PASS', '-X', 'POST',
     '-H', 'Content-Type: application/json',
     '-d', json.dumps(payload),
     '$GRAFANA_URL/api/dashboards/import'],
    capture_output=True)
sys.exit(0 if r.returncode == 0 else 1)
"
done

echo "done"
