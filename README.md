# GophProfile

Production-ready Go-микросервис для управления аватарками пользователей.  
**Sprint 13 — Kubernetes и деплой Go-сервисов** (финальный спринт курса Go-Advanced).

Юзер загружает фото через HTTP API → сервер кладёт его в S3 → публикует событие в Kafka →
worker читает событие, делает миниатюры 100x100 и 300x300, обновляет статус в PostgreSQL.

## ✨ Что внутри

- **HTTP API** на `chi` — загрузка, получение, удаление, список аватарок
- **PostgreSQL** хранит метаданные (uuid, user_id, mime, размер, статусы, миниатюры)
- **MinIO/S3** хранит оригиналы и миниатюры
- **Kafka** — очередь событий между server и worker
- **Worker** — отдельный процесс, consumer Kafka, ресайз изображений
- **Observability** — Prometheus, Jaeger, Loki, OpenTelemetry Collector (Sprint 12)
- **Kubernetes manifests** — Deployment, Service, Ingress, HPA, ServiceMonitor, NetworkPolicy, PodDisruptionBudget
- **Helm chart** — values для dev/staging/prod, hooks для миграций БД

## 📋 Требования

- Go 1.25+
- Docker (для локального запуска и сборки образов)
- Kubernetes 1.30+ (для деплоя) или Rancher Desktop / kind / minikube
- Helm 3.x (для деплоя через chart)
- PostgreSQL 16, Apache Kafka 7.x, MinIO/S3 (для локального запуска)

## 🚀 Быстрый старт (локально, через docker-compose)

```bash
# 1. Клонировать репозиторий
git clone https://github.com/tradekmv/gophprofile
cd gophprofile

# 2. Поднять инфраструктуру (PostgreSQL, MinIO, Kafka, OTel, Prometheus, Grafana, ...)
make up

# 3. Собрать и запустить server + worker
make run-server  # в одном терминале
make run-worker  # в другом терминале

# 4. Загрузить тестовую аватарку
AVATAR_ID=$(curl -s -X POST http://localhost:18080/api/v1/avatars \
  -H "X-User-ID: alice" -F "file=@testdata/sample.jpg" | jq -r .id)
echo "id=$AVATAR_ID"

# 5. Через ~10 сек проверить, что worker обработал
curl -s http://localhost:18080/api/v1/avatars/$AVATAR_ID/metadata | jq .
```

UI для просмотра:
- **Grafana**: <http://localhost:3000> (admin/admin) — дашборды GophProfile
- **Prometheus**: <http://localhost:9090> — метрики
- **Jaeger UI**: <http://localhost:16686> — трассировки
- **Loki** (через Grafana Explore) — логи
- **MinIO Console**: <http://localhost:9001> (minioadmin/minioadmin)

## ☸️ Деплой в Kubernetes

### Требования к кластеру

- Установленные **ingress-nginx** controller
- Установленный **Prometheus Operator** (для ServiceMonitor)
- StorageClass для PVC (если используется StatefulSet для БД)

### Установка через Helm

```bash
# 1. Собрать образы и запушить в registry
docker build -f docker/Dockerfile.server -t ghcr.io/your-org/gophprofile-server:0.2.0 .
docker build -f docker/Dockerfile.worker -t ghcr.io/your-org/gophprofile-worker:0.2.0 .
docker build -f docker/Dockerfile.server -t ghcr.io/your-org/gophprofile-migrate:0.2.0 \
    --target builder  # или отдельный Dockerfile для migrate
docker push ghcr.io/your-org/gophprofile-server:0.2.0
docker push ghcr.io/your-org/gophprofile-worker:0.2.0
docker push ghcr.io/your-org/gophprofile-migrate:0.2.0

# 2. Создать namespace
kubectl create namespace gophprofile

# 3. Установить через Helm (dev)
helm install gophprofile ./deploy/helm/gophprofile \
  -f ./deploy/helm/gophprofile/values-dev.yaml \
  --namespace gophprofile

# 4. Установить через Helm (prod)
helm upgrade --install gophprofile ./deploy/helm/gophprofile \
  -f ./deploy/helm/gophprofile/values-prod.yaml \
  --set secret.postgresPassword="YOUR_PASSWORD" \
  --set secret.s3.accessKey="YOUR_KEY" \
  --set secret.s3.secretKey="YOUR_SECRET" \
  --set secret.appSigningKey="$(openssl rand -hex 32)" \
  --namespace gophprofile

# 5. Проверить статус
kubectl --namespace gophprofile get pods
kubectl --namespace gophprofile get svc
kubectl --namespace gophprofile get ingress
kubectl --namespace gophprofile get hpa
```

### Что устанавливается

| Ресурс | Описание |
|--------|----------|
| `Deployment/gophprofile-server` | HTTP API, 2+ реплик с HPA |
| `Deployment/gophprofile-worker` | Kafka consumer, 1+ реплик с HPA |
| `Service/gophprofile-server` | ClusterIP:8080 |
| `Service/gophprofile-server-metrics` | ClusterIP:9095 (для Prometheus) |
| `Service/gophprofile-worker-metrics` | ClusterIP:9096 (для Prometheus) |
| `Ingress` | Внешний HTTPS endpoint (nginx-ingress) |
| `ConfigMap` | Несекретная конфигурация (адреса, размеры) |
| `Secret` | Чувствительные данные (пароли, ключи) |
| `ServiceAccount` | Минимальные права (по умолчанию) |
| `HorizontalPodAutoscaler` | CPU 70%, Memory 80%, 2-10 реплик |
| `PodDisruptionBudget` | minAvailable=1 (не evict всех сразу) |
| `NetworkPolicy` | Только разрешённый ingress/egress |
| `ServiceMonitor` | scrape /metrics каждые 30s |
| `Job/migration` (Helm hook) | Миграции БД перед deploy |

### Graceful shutdown

Server и worker корректно завершаются:
- `terminationGracePeriodSeconds: 30` — K8s ждёт 30 сек перед SIGKILL
- `preStop` hook: `sleep 5` — пауза для завершения in-flight запросов
- HTTP server использует `srv.Shutdown(ctx)` с 15-секундным таймаутом
- Worker останавливает Kafka consumer и завершает обработку текущего сообщения

### Production checklist

Перед деплоем в production:

- [ ] Задать реальные значения для всех `secret.*` полей
- [ ] Создать `imagePullSecret` для приватного registry
- [ ] Включить TLS для Ingress (через cert-manager)
- [ ] Настроить внешний Secret manager (HashiCorp Vault или external-secrets-operator)
- [ ] Создать ServiceAccount с IRSA/IAM (если в AWS/GCP)
- [ ] Включить PodDisruptionBudget
- [ ] Настроить alerting в Prometheus (alerts уже есть в `deploy/otel/grafana-provisioning/alerting/`)
- [ ] Включить NetworkPolicy с namespaceSelector под ваши namespace

## 🧪 Тестирование

```bash
# Unit-тесты с race detector
make test

# Coverage отчёт
make test-coverage

# Smoke-тест end-to-end (требует запущенного стека)
make smoke

# End-to-end observability (контейнеры, OTel, Prometheus, Grafana, Loki, Jaeger)
make e2e-obs
```

## 📊 Observability

Полная документация в [`docs/observability.md`](docs/observability.md).

**Сигналы**:
- **Metrics** (`/metrics`) — `avatars_uploads_total`, `avatars_upload_duration_seconds`, `avatars_storage_bytes`, `avatars_http_requests_total`, `avatars_db_connections_open`, `avatars_worker_processed_total`
- **Traces** — distributed: HTTP → DB (pgx) → S3 → Worker
- **Logs** — JSON в stdout + OTLP в Loki, коррелированы с `trace_id` и `span_id`

**Стек**: Prometheus + Grafana + Jaeger + Loki + OpenTelemetry Collector (см. `deploy/otel/`).

## 🏗️ Архитектура

См. [`docs/architecture.md`](docs/architecture.md) — диаграммы потоков, сетевая модель, чек-лист безопасности.

## 📖 API

Полная OpenAPI 3.0 спецификация в [`docs/api/openapi.yaml`](docs/api/openapi.yaml).  
Можно открыть через [Swagger Editor](https://editor.swagger.io/) или встроить в [Redoc](https://redocly.com/).

**Краткий обзор**:
- `POST /api/v1/avatars` — загрузить аватарку (multipart)
- `GET /api/v1/avatars/{id}?size=100x100` — получить
- `DELETE /api/v1/avatars/{id}` — удалить
- `GET /api/v1/avatars/{id}/metadata` — метаданные
- `GET /api/v1/users/{user_id}/avatar` — текущая аватарка пользователя
- `GET /api/v1/users/{user_id}/avatars` — список
- `GET /health` — health check
- `GET /metrics` — Prometheus metrics

## 📁 Структура проекта

```
gophprofile/
├── cmd/
│   ├── server/         # HTTP API
│   ├── worker/         # Kafka consumer + ресайз
│   └── migrate/        # миграции БД
├── internal/
│   ├── api/            # хендлеры, роутер, метрики
│   ├── broker/         # Sarama publisher + consumer
│   ├── config/         # envconfig
│   ├── domain/         # Avatar + события
│   ├── repository/     # pgx + S3
│   ├── services/       # бизнес-логика
│   └── worker/         # обработчик + idempotency
├── pkg/
│   ├── observability/  # OpenTelemetry SDK
│   ├── logger/         # zerolog
│   ├── httperr/        # типы ошибок API
│   └── imgutil/        # magic bytes + размеры
├── deploy/
│   ├── helm/gophprofile/    # Helm chart для K8s
│   ├── otel/                # Grafana, Prometheus, Jaeger, OTel
│   └── docker/              # Dockerfile.server, Dockerfile.worker
├── docs/
│   ├── api/openapi.yaml     # OpenAPI 3.0 спецификация
│   ├── architecture.md      # Архитектурные диаграммы
│   └── observability.md     # Observability runbook
├── migrations/              # SQL миграции
├── scripts/                 # smoke, e2e-observability
├── docker-compose.yml       # Локальный dev-стек
└── README.md
```

## 🛠️ Makefile targets

```bash
make help              # Список всех targets
make init              # Клонировать web-шаблон
make up                # Запустить docker-compose
make down              # Остановить
make build             # Собрать бинарники server/worker/migrate
make test              # Unit-тесты
make test-coverage     # Coverage отчёт
make lint              # golangci-lint
make smoke             # End-to-end smoke-тест
make e2e-obs           # Observability e2e (Prometheus, Jaeger, Loki)
make grafana-import    # Импортировать Grafana dashboards
```

## 📜 Лицензия

MIT
