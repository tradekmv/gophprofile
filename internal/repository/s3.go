package repository

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Sentinel-ошибки S3-репозитория.
var (
	ErrNotFoundInBucket = errors.New("object not found in bucket")
)

// ObjectStorage — интерфейс персистентности для S3-совместимых хранилищ.
type ObjectStorage interface {
	Upload(ctx context.Context, key string, body io.Reader, size int64, contentType string) error
	UploadBytes(ctx context.Context, key string, data []byte, contentType string) error
	Download(ctx context.Context, key string) (io.ReadCloser, string, error)
	Delete(ctx context.Context, keys ...string) error
}

// S3Config — конфигурация S3-клиента.
type S3Config struct {
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	UseSSL    bool
}

// S3Storage — реализация на aws-sdk-go-v2.
type S3Storage struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

// NewS3Storage создаёт клиент (с path-style адресацией, обязательной для MinIO).
func NewS3Storage(ctx context.Context, cfg S3Config) (*S3Storage, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("bucket name is required")
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}

	creds := credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, "")

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(creds),
	)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = true // обязательно для MinIO
		if !cfg.UseSSL {
			o.EndpointOptions.DisableHTTPS = true
		}
	})

	return &S3Storage{
		client:  client,
		presign: s3.NewPresignClient(client),
		bucket:  cfg.Bucket,
	}, nil
}

// Upload кладёт объект в bucket.
func (s *S3Storage) Upload(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	if key == "" {
		return errors.New("empty key")
	}
	in := &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(contentType),
	}
	if size > 0 {
		in.ContentLength = aws.Int64(size)
	}
	if _, err := s.client.PutObject(ctx, in); err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	return nil
}

// Download скачивает объект и возвращает его тело + content-type.
func (s *S3Storage) Download(ctx context.Context, key string) (io.ReadCloser, string, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var notFound *s3types.NoSuchKey
		if errors.As(err, &notFound) {
			return nil, "", ErrNotFoundInBucket
		}
		return nil, "", fmt.Errorf("get %s: %w", key, err)
	}
	ct := ""
	if out.ContentType != nil {
		ct = *out.ContentType
	}
	return out.Body, ct, nil
}

// UploadBytes — обёртка для байтов в памяти.
func (s *S3Storage) UploadBytes(ctx context.Context, key string, data []byte, contentType string) error {
	return s.Upload(ctx, key, bytes.NewReader(data), int64(len(data)), contentType)
}

// Delete удаляет объекты; отсутствующие ключи игнорируются.
func (s *S3Storage) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	objects := make([]s3types.ObjectIdentifier, 0, len(keys))
	for _, k := range keys {
		if k == "" {
			continue
		}
		objects = append(objects, s3types.ObjectIdentifier{Key: aws.String(k)})
	}
	if len(objects) == 0 {
		return nil
	}
	_, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: aws.String(s.bucket),
		Delete: &s3types.Delete{
			Objects: objects,
			Quiet:   aws.Bool(true),
		},
	})
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}

// EnsureBucket создаёт bucket, если его нет. Уже существующий — OK.
func (s *S3Storage) EnsureBucket(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)})
	if err == nil {
		return nil
	}
	// Bucket отсутствует — создаём.
	_, cerr := s.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(s.bucket)})
	if cerr != nil {
		// Если уже создан параллельно — ок.
		if strings.Contains(cerr.Error(), "BucketAlready") || strings.Contains(cerr.Error(), "BucketAlreadyOwnedByYou") {
			return nil
		}
		return fmt.Errorf("create bucket %s: %w", s.bucket, cerr)
	}
	return nil
}
