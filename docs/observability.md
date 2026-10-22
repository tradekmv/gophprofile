# Observability (Sprint 12)

Production-grade стек для трассировки, метрик и алертинга сервиса GophProfile.
Построен на OpenTelemetry (OTel) + Prometheus + Grafana + Alertmanager.

## TL;DR

```bash
make up         # поднять стек + импортировать дашборды
make e2e-obs    # проверить весь observability-стек end-to-end
```

Если `make e2e-obs` показывает ✅ — всё работает. Если ❌ — секция «Troubleshooting»
ниже подскажет, куда смотреть.

## Архитектура

```
┌─────────────┐       gRPC :4317        ┌──────────────┐
│  gophprofile│ ──────────────────────▶ │              │
│   server    │       HTTP :4318        │ otel-collector│
│             │ ──────────────────────▶ │              │
└─────────────┘                         └──────┬───────┘
                                                │
                            ┌───────────────────┼───────────────────┐
                            ▼                                       ▼
                    ┌──────────────┐                        ┌──────────────┐
                    │ debug (stdout)│                        │ prometheus    │
                    │  traces      │                        │ exporter :8889│
                    └──────────────┘                        └──────┬───────┘
                                                                   │
┌─────────────┐       gRPC :4317        ┌──────────────┐           │
│  gophprofile│ ──────────────────────▶ │              │           │
│   worker    │       HTTP :4318        │ otel-collector│           │
│             │ ──────────────────────▶ │              │           │
└─────────────┘                         └──────────────┘           │
                                                                   ▼
                                                           ┌──────────────┐
┌─────────────┐  pull :9095 (server)   ┌──────────────┐  │  prometheus   │
│  gophprofile│ ──────────────────────▶│              │  │  :9090        │
│   server    │  pull :9096 (worker)   │  prometheus  │──│               │
└─────────────┘                         └──────────────┘  └──────┬───────┘
                                                                  ▼
   ┌─────────────┐  PromQL   ┌──────────────┐  ┌──────────────────┐
   │  alertmanager│◀─────────│   grafana     │──│  dashboards (3)  │
   │  :9093      │  alerts  │  :3000        │  │  alert rules (4) │
   └─────────────┘           └──────────────┘  └──────────────────┘
```

## Компоненты

| Компонент         | Версия                  | Роль                                 |
|-------------------|-------------------------|--------------------------------------|
| otel-collector    | contrib:0.143.0         | Сбор и маршрутизация traces/metrics  |
| jaeger            | all-in-one:1.62.0       | UI для трейсов (только просмотр)     |
| prometheus        | v3.1.0                  | Pull-модель метрик                   |
| grafana           | 12.3.1                  | Визуализация + alert rules           |
| alertmanager      | v0.27.0                 | Маршрутизация алертов                |

### Сигналы

| Сигнал     | Endpoint           | Протокол   | Примечание                                    |
|------------|--------------------|------------|-----------------------------------------------|
| traces     | :4317              | OTLP/gRPC  | Спаны HTTP → DB (pgx) → S3 → Worker           |
| metrics    | :4318/v1/metrics   | OTLP/HTTP  | otelhttp-метрики; бизнес — напрямую через :9095/9096 |
| logs       | :4317 + stdout     | OTLP/gRPC  | Двойной канал: (1) otelslog в otel-collector → Loki, (2) zerolog в stdout (fallback) |

### Двойной канал логов (текущее состояние)

В приложении **два независимых логгера**:

1. **zerolog** (через `pkg/logger.L()`) — пишет JSON в stdout контейнера.
   Этот канал читает `otel-collector` через механизм stdout-чтения и/или собирается
   внешним сборщиком (loki docker driver, promtail, fluentbit).

2. **otelslog** (через `slog.SetDefault` в `observability.Init`) — отправляет
   логи по OTLP/gRPC в `otel-collector:4317`. otel-collector пробрасывает их в Loki.

Для production: рекомендуется настроить **promtail** или **loki docker driver**
для сбора stdout-логов, чтобы zerolog-записи тоже попадали в Loki с теми же
labels, что и otelslog-записи. Альтернатива — миграция на единый `log/slog`
через `otelslog.NewHandler`, чтобы убрать zerolog-параллельный канал.

## Конфигурация

### Переменные окружения (server/worker)

```yaml
# Общий endpoint (fallback для всех сигналов)
OTEL_EXPORTER_OTLP_ENDPOINT: http://otel-collector:4317

# Per-signal endpoints (имеют приоритет над общим)
OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: http://otel-collector:4317   # gRPC
OTEL_EXPORTER_OTLP_METRICS_ENDPOINT: http://otel-collector:4318  # HTTP

OTEL_EXPORTER_OTLP_INSECURE: "true"          # dev (без TLS)
OTEL_SERVICE_NAME: gophprofile-{server|worker}
OTEL_RESOURCE_ATTRIBUTES: deployment.environment=development,service.version=0.2.0

# Включение экспортёров (none — выключить)
OTEL_TRACES_EXPORTER: otlp
OTEL_METRICS_EXPORTER: otlp
```

### Direct Prometheus endpoints

- `gophprofile-server:9095/metrics` — `avatars_*` бизнес-метрики
- `gophprofile-worker:9096/metrics` — worker-метрики
- `otel-collector:8889/metrics` — otelhttp-метрики (`http_server_request_duration_seconds` и др.)

### Endpoint'ы для UI

| Сервис        | URL                          |
|---------------|------------------------------|
| Grafana       | http://localhost:3000 (admin/admin) |
| Prometheus    | http://localhost:9090        |
| Jaeger UI     | http://localhost:16686       |
| Alertmanager  | http://localhost:9093        |
| OTel health   | http://localhost:13133       |

## Что измеряется

### Traces (HTTP → DB → S3 → Worker)

Полная цепочка запроса с общим `trace_id`:

```
POST /api/v1/avatars                          (otelhttp)
  └─ db.exec INSERT INTO avatars              (pgx)
  └─ s3.upload_object                         (S3)
  └─ s3.get_object (health probe)             (S3)
```

Worker'у передаётся `traceparent` через Kafka headers (W3C TraceContext),
так что трейс пробрасывается через всю асинхронную обработку.

### Метрики

**Server** (`avatars_*`, :9095):
- `avatars_http_requests_total{method,route,status}`
- `avatars_http_request_duration_seconds_*`
- `avatars_uploads_total{status}` — успех/ошибка
- `avatars_upload_duration_seconds_*`
- `avatars_upload_errors_total{reason}`
- `avatars_db_connections_open`

**Worker** (`avatars_worker_*`, :9096):
- `avatars_worker_processed_total{status}` — completed/failed
- `avatars_worker_processing_duration_seconds_*`
- `avatars_kafka_consumer_lag`

**otelhttp** (через otel-collector, :8889):
- `http_server_request_duration_seconds_*` (гистограмма)
- `http_server_request_body_size_bytes`
- `http_server_response_body_size_bytes`
- `http_server_active_requests`

### Алерты (Grafana → Alertmanager)

| Алертит                | Условие                                       | Severity |
|------------------------|-----------------------------------------------|----------|
| `HighErrorRate`        | error rate > 5% за 5 мин                     | warning  |
| `HighResponseTime`     | p95 > 1s за 5 мин                            | warning  |
| `WorkerHighFailureRate`| worker failed > 10% за 10 мин                | warning  |
| `WorkerSlowProcessing` | worker p95 > 30s за 10 мин                   | warning  |

Конфиг: `deploy/otel/grafana-provisioning/alerting/rules.yaml`.

## Как проверить работу (e2e)

### 1. `make e2e-obs` — автоматическая проверка

```bash
make e2e-obs
```

Скрипт `scripts/e2e-observability.sh` проверяет 8 пунктов:
1. 10 контейнеров Up
2. OTel traces доходят до otel-collector
3. Структурированные slog-логи
4. Нет OTel-ошибок (duplicate labels, 404, deadline exceeded)
5. 4 Prometheus targets up
6. Бизнес-метрики скрайпятся
7. 3 Grafana dashboard
8. 4 alert rules

### 2. Ручная проверка

```bash
# Генерируем нагрузку
AVATAR_ID=$(curl -s -X POST http://localhost:18080/api/v1/avatars \
  -H "X-User-ID: alice" \
  -F "file=@testdata/sample.jpg" | jq -r .id)

# Трейсы в otel-collector
sleep 15
docker logs gophprofile-otel-collector --since 30s | grep '"otelcol.signal": "traces"'

# Метрики в Prometheus
curl -s 'http://localhost:9090/api/v1/query?query=avatars_uploads_total' | jq

# Grafana dashboards
curl -s -u admin:admin 'http://localhost:3000/api/search?query=GophProfile&type=dash-db' | jq
```

## Troubleshooting

### `make e2e-obs` → "✗ no traces in otel-collector"

Проверить:

1. **endpoint формат**: gRPC ожидает `host:port`, не URL.
   - ✅ `otel-collector:4317`
   - ❌ `http://otel-collector:4317` (вызывает dial-блокировку)

2. **container actually running**:
   ```bash
   docker ps | grep otel-collector
   ```

3. **env в server**:
   ```bash
   docker exec gophprofile-server-1 env | grep OTEL
   ```

4. **otel-collector слушает**:
   ```bash
   docker logs gophprofile-otel-collector | grep "Starting GRPC server"
   ```

### `✗ duplicate labels in constant and variable labels for metric "http_server_request_duration_seconds"`

В `deploy/otel/otel-collector-config.yaml` для prometheus exporter должно быть:

```yaml
exporters:
  prometheus:
    resource_to_telemetry_conversion:
      enabled: false  # ← обязательно false
```

### `failed to upload metrics: failed to send metrics to http://otel-collector:4318/: 404`

otel-collector слушает HTTP на :4318, но метрики должны идти на `/v1/metrics`.
В коде `initMeter` собирает URL с path автоматически. Если передан только host:port — добавится `/v1/metrics`. Если ошибка сохраняется — проверить:

```bash
docker logs gophprofile-otel-collector | grep "Starting HTTP server"
# Должно показать endpoint ":4318"
```

### Targets в Prometheus "down"

```bash
# Проверить что target вообще достижим из контейнера prometheus
docker exec gophprofile-prometheus wget -qO- --timeout=3 http://server:9095/metrics | head
```

Если `server:9095` не резолвится — проверить `docker-compose.yml`:
все контейнеры должны быть в одной compose-сети.

### `make up` не импортирует дашборды

`make up` запускает `grafana-import` в конце. Если дашбордов нет — запустить вручную:

```bash
make grafana-import
```

Скрипт `scripts/import-dashboards.sh` использует `jq` + `curl` к Grafana API
и пересоздаёт 3 дашборда по provisioning-манифесту в `deploy/otel/grafana-provisioning/dashboards/`.

## Локальная разработка без Docker

Если нужно запустить server/worker локально (без compose), задать env:

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
export OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://localhost:4317
export OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://localhost:4318
export OTEL_EXPORTER_OTLP_INSECURE=true
export OTEL_SERVICE_NAME=gophprofile-server
export METRICS_HTTP_ADDR=:9095
```

И поднять только otel-collector + зависимости:

```bash
docker compose up -d postgres minio kafka otel-collector prometheus grafana
go run ./cmd/server
```

## Файлы и где что искать

| Что                                | Файл                                           |
|------------------------------------|------------------------------------------------|
| OTel SDK init                      | `pkg/observability/observability.go`            |
| HTTP tracing middleware            | `internal/api/middleware.go` (TracingMiddleware) |
| DB tracing (pgx)                   | `internal/repository/oteltrace.go`             |
| S3 tracing                         | `internal/repository/s3.go` (spans)            |
| Worker tracing                     | `internal/worker/processor.go`                 |
| Business metrics                   | `internal/api/metrics.go`, `internal/worker/metrics.go` |
| otel-collector config              | `deploy/otel/otel-collector-config.yaml`       |
| Prometheus scrape config           | `deploy/otel/prometheus.yml`                   |
| Grafana datasources/dashboards     | `deploy/otel/grafana-provisioning/`            |
| Alert rules                        | `deploy/otel/grafana-provisioning/alerting/rules.yaml` |
| e2e-observability скрипт           | `scripts/e2e-observability.sh`                 |
