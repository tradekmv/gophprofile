# GophProfile

Микросервис для аватарок. Юзер загружает фото, а другие сервисы могут забрать его по `X-User-ID`. Это финальный проект Яндекс.Практикума (Go-Advanced), спринт 11.

## Что внутри

- **HTTP API** на `chi` — загрузка, получение, удаление аватарок
- **PostgreSQL** хранит метаданные (uuid, user_id, mime, размер, статусы)
- **MinIO/S3** хранит файлы
- **Kafka** — очередь событий между сервером и воркером
- **Worker** — отдельный процесс, читает события из Kafka, делает миниатюры 100x100 и 300x300, обновляет статус в БД

## Структура проекта

```
gophprofile/
├── cmd/server/         # HTTP API
├── cmd/worker/         # Kafka consumer + ресайз
├── cmd/migrate/        # миграции БД
├── internal/
│   ├── api/            # хендлеры chi
│   ├── broker/         # Sarama publisher + consumer
│   ├── config/         # envconfig
│   ├── domain/         # Avatar + события
│   ├── repository/     # pgx + S3
│   ├── services/       # бизнес-логика
│   └── worker/         # обработчик + idempotency
├── pkg/imgutil/        # magic bytes + размеры
├── pkg/httperr/        # типы ошибок API
├── pkg/logger/         # zerolog
├── migrations/          # SQL миграции
├── docker/              # Dockerfile.server + Dockerfile.worker
├── scripts/smoke.sh     # e2e curl-тест
└── docker-compose.yml
```

## Как запустить

```bash
cp .env.example .env
docker compose up -d          # поднимет postgres, minio, kafka, server, worker
```

Подожди ~15 сек пока всё станет `healthy`. Затем:

```bash
make smoke                     # e2e: upload → worker → thumbnails → delete → 404
```

API будет на `http://localhost:18080`, web-UI на `http://localhost:18080/web/`.

## API эндпоинты

Всем (кроме `/health`, `/web/*`) нужен заголовок `X-User-ID`.

| Метод | Путь | Что делает |
|---|---|---|
| POST | `/api/v1/avatars` | multipart-поле `file`, до 10 МБ, JPEG/PNG/WebP |
| GET | `/api/v1/avatars/{id}` | `?size=100x100\|300x300\|original` |
| GET | `/api/v1/avatars/{id}/metadata` | JSON с метаданными и списком миниатюр |
| DELETE | `/api/v1/avatars/{id}` | soft delete (только владелец) |
| GET | `/api/v1/users/{user_id}/avatar` | последняя аватарка юзера |
| GET | `/api/v1/users/{user_id}/avatars` | список аватарок юзера |
| GET | `/health` | `{"status":"ok","components":{...}}` |
| GET | `/web/*` | SPA из шаблона |

## Примеры

```bash
# Загрузить
curl -X POST http://localhost:18080/api/v1/avatars \
  -H "X-User-ID: alice" \
  -F "file=@testdata/sample.jpg"

# Через 2 сек проверить, что воркер обработал
curl http://localhost:18080/api/v1/avatars/<id>/metadata

# Скачать миниатюру
curl -o thumb.jpg "http://localhost:18080/api/v1/avatars/<id>?size=100x100"

# Удалить
curl -X DELETE http://localhost:18080/api/v1/avatars/<id> -H "X-User-ID: alice"
```

## Стек

| Слой | Библиотека |
|---|---|
| HTTP | `go-chi/chi/v5` |
| БД | PostgreSQL + `jackc/pgx/v5` + `golang-migrate` |
| Хранилище | S3 / MinIO + `aws-sdk-go-v2` |
| Брокер | Kafka + `IBM/sarama` |
| Картинки | `disintegration/imaging` |
| Логи | `zerolog` |
| Тесты | `testify` + `testcontainers-go` |

## Тесты

```bash
make test             # все (нужен Docker, ~30 сек через testcontainers)
make test-coverage    # + coverage.html
make lint             # golangci-lint
```

Покрытие ~54% по проекту.

## Команды Makefile

- `up` / `down` — поднять/опустить стек через docker compose
- `build` — собрать бинари в `./bin/`
- `test` — прогнать тесты
- `lint` — golangci-lint
- `smoke` — e2e тест
- `run-server` / `run-worker` — запустить локально (нужен `.env`)

## Как это работает

1. Юзер загружает JPEG/PNG/WebP через `POST /api/v1/avatars`
2. Server проверяет magic bytes, сохраняет файл в MinIO/S3, создаёт запись в PostgreSQL со статусом `processing_status=pending`
3. Server публикует `AvatarUploadEvent` в Kafka-топик `avatar-events`
4. Worker читает событие, скачивает оригинал, ресайзит в 100x100 и 300x300, сохраняет миниатюры в S3, обновляет статус на `completed`
5. Другие сервисы могут запросить аватарку через `GET /api/v1/users/{user_id}/avatar`

Идемпотентность: каждое сообщение имеет `message-id`, воркер хранит их в `ProcessedStore` (in-memory с TTL 24ч) и не обрабатывает дубликаты.

## Наблюдаемость (Observability)

В проекте реализованы три столпа наблюдаемости через **OpenTelemetry**: распределённая трассировка, метрики и структурированное логирование.

### Трейсы

Трейсы автоматически создаются для всех HTTP-запросов (`otelhttp.NewHandler`), операций в БД (`pgxpool` спаны `db.query`/`db.exec`/`db.query_row`), S3 (`s3.upload_object`/`s3.get_object`/`s3.delete_objects`) и операций воркера по обработке событий. Контекст-пропагация через W3C `traceparent` + Baggage.

### Метрики

Все метрики имеют префикс `avatars_`:

**HTTP (RED):**
- `avatars_http_requests_total{method,route,status}` — counter
- `avatars_http_request_duration_seconds{method,route,status}` — histogram

**Бизнес:**
- `avatars_uploads_total{status}` — counter
- `avatars_upload_duration_seconds{status}` — histogram
- `avatars_upload_errors_total{reason}` — counter

**Worker:**
- `avatars_worker_processed_total{status}` — counter
- `avatars_worker_processing_duration_seconds{status}` — histogram
- `avatars_worker_failed_total{reason}` — counter

### Логи

`slog` через мост `otelslog`. Каждая запись обогащается `service.name`, `service.version`, `deployment.environment` и `trace_id`/`span_id` для корреляции с трейсами. Уровни: info, error, debug.

### Стек для локального запуска

`docker-compose.yml` поднимает:
- **OpenTelemetry Collector** (contrib) на 4317 (gRPC), 4318 (HTTP), 8889 (Prometheus endpoint), 13133 (health)
- **Jaeger** all-in-one на 16686 — UI для трейсов
- **Prometheus** на 9090 — сбор метрик
- **Grafana** на 3000 — визуализация (admin/admin), автоматически подключена к Prometheus и Jaeger

Конфиги: `deploy/otel/otel-collector-config.yaml`, `deploy/otel/prometheus.yml`, `deploy/otel/grafana-provisioning/`.

### Переменные окружения для OTel

| Переменная | Назначение | По умолчанию |
|---|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Адрес OTel Collector (например, `otel-collector:4317`) | пусто (SDK выключен) |
| `OTEL_EXPORTER_OTLP_INSECURE` | Отключить TLS для OTLP | `false` |
| `OTEL_SERVICE_NAME` | Имя сервиса для атрибута `service.name` | `gophprofile-server` / `gophprofile-worker` |
| `OTEL_RESOURCE_ATTRIBUTES` | Доп. атрибуты ресурса (`k=v,k=v`) | пусто |
| `OTEL_SDK_DISABLED` | Полностью отключить SDK | `false` |
| `METRICS_HTTP_ADDR` | Адрес прямого `/metrics` (Prometheus pull, fallback) | `:9095` (server) / `:9096` (worker) |

Для локального запуска без стека наблюдаемости (например, в CI) поставьте `OTEL_SDK_DISABLED=true` или просто не задавайте `OTEL_EXPORTER_OTLP_ENDPOINT`. Все метрики и трейсы корректно игнорируются.

### Использование

После `make up` откройте:
- **Grafana:** http://localhost:3000 — дашборды метрик и трасс
- **Jaeger:** http://localhost:16686 — поиск и визуализация трейсов
- **Prometheus:** http://localhost:9090 — сырой запрос метрик и targets
- **Alertmanager:** http://localhost:9093 — список активных алертов
- **Server /metrics:** http://localhost:18095 — прямой endpoint (если Collector недоступен)
- **Worker /metrics:** http://localhost:18096 — прямой endpoint

### Дашборды и алертинг

После `make up`:
- **Дашборды Grafana** авто-импортируются в папку `GophProfile` через provisioning.
  Если не подхватились — выполните вручную: `make grafana-import`.
  Доступны: **Overview**, **Worker**, **Database & S3**.
- **Алерты Grafana** (4 правила):
  - `HighErrorRate` (HTTP 5xx > 10% за 5 мин) → severity: warning
  - `HighResponseTime` (HTTP p95 > 2s за 5 мин) → severity: critical
  - `WorkerHighFailureRate` (failures > 0.1/s за 10 мин) → severity: warning
  - `WorkerSlowProcessing` (worker p95 > 5s за 5 мин) → severity: warning
- **Prometheus alert rules** (4 правила, `deploy/otel/prometheus-rules.yml`) автоматически
  загружаются Prometheus при старте.
- **Alertmanager** настроен с одним contact point `gophprofile-noop` (webhook на `127.0.0.1:5001`,
  который в dev ничего не делает). Для прода замените в `deploy/otel/alertmanager.yml`
  на реальные receiver'ы (Slack, PagerDuty, email).

Файл конфигурации Alertmanager: `deploy/otel/alertmanager.yml`.
Файл правил Prometheus: `deploy/otel/prometheus-rules.yml`.
Provisioning Grafana: `deploy/otel/grafana-provisioning/`.

## Конфиг через env

Все настройки — из переменных окружения. Полный список в `.env.example`:

```
DATABASE_DSN=postgres://app:app@localhost:5432/gophprofile?sslmode=disable
KAFKA_BROKERS=localhost:9092
KAFKA_TOPIC=avatar-events
S3_ENDPOINT=http://localhost:9000
S3_BUCKET=avatars
MAX_UPLOAD_SIZE=10485760
THUMBNAIL_SIZES=100x100,300x300
```
