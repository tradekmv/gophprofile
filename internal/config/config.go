// Package config — загрузка конфигурации из переменных окружения.
package config

import (
	"fmt"

	"github.com/kelseyhightower/envconfig"
)

// Config хранит все настройки рантайма для бинарей server/worker/migrate.
type Config struct {
	// Сервер
	ServerAddress string `envconfig:"SERVER_ADDRESS" default:":8080"`
	BaseURL       string `envconfig:"BASE_URL"       default:"http://localhost:8080"`
	LogLevel      string `envconfig:"LOG_LEVEL"      default:"info"`
	MaxUploadSize int64  `envconfig:"MAX_UPLOAD_SIZE" default:"10485760"`
	WebDir        string `envconfig:"WEB_DIR"        default:"./web"`

	// PostgreSQL
	PostgresHost string `envconfig:"POSTGRES_HOST"     default:"localhost"`
	PostgresPort int    `envconfig:"POSTGRES_PORT"     default:"5432"`
	PostgresDB   string `envconfig:"POSTGRES_DB"       default:"gophprofile"`
	PostgresUser string `envconfig:"POSTGRES_USER"     default:"app"`
	// Секреты — дефолтов нет: если переменная не задана, сервис должен
	// упасть при старте (fail early). Это безопаснее, чем дефолт вроде "app".
	PostgresPassword string `envconfig:"POSTGRES_PASSWORD" required:"true"`
	DatabaseDSN      string `envconfig:"DATABASE_DSN"      default:""`

	// S3 / MinIO
	S3Endpoint      string `envconfig:"S3_ENDPOINT"      default:"http://localhost:9000"`
	S3Region        string `envconfig:"S3_REGION"        default:"us-east-1"`
	S3Bucket        string `envconfig:"S3_BUCKET"        default:"avatars"`
	S3AccessKey     string `envconfig:"S3_ACCESS_KEY"    required:"true"`
	S3SecretKey     string `envconfig:"S3_SECRET_KEY"    required:"true"`
	S3UseSSL        bool   `envconfig:"S3_USE_SSL"       default:"false"`
	S3PublicBaseURL string `envconfig:"S3_PUBLIC_BASE_URL" default:"http://localhost:9000/avatars"`

	// Kafka
	KafkaBrokers                []string `envconfig:"KAFKA_BROKERS"                default:"localhost:9092"`
	KafkaTopic                  string   `envconfig:"KAFKA_TOPIC"                  default:"avatar-events"`
	KafkaConsumerGroup          string   `envconfig:"KAFKA_CONSUMER_GROUP"         default:"gophprofile-worker"`
	KafkaTopicPartitions        int      `envconfig:"KAFKA_TOPIC_PARTITIONS"       default:"3"`
	KafkaTopicReplicationFactor int16    `envconfig:"KAFKA_TOPIC_REPLICATION_FACTOR" default:"1"`

	// Размеры миниатюр
	ThumbnailSizes []string `envconfig:"THUMBNAIL_SIZES" default:"100x100,300x300"`
}

// Load читает конфиг из окружения.
func Load() (*Config, error) {
	var c Config
	if err := envconfig.Process("", &c); err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if c.DatabaseDSN == "" {
		c.DatabaseDSN = fmt.Sprintf(
			"postgres://%s:%s@%s:%d/%s?sslmode=disable",
			c.PostgresUser, c.PostgresPassword, c.PostgresHost, c.PostgresPort, c.PostgresDB,
		)
	}
	return &c, nil
}

// PgDSN возвращает DSN (всегда непустой).
func (c *Config) PgDSN() string { return c.DatabaseDSN }
