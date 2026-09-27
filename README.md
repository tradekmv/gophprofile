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
