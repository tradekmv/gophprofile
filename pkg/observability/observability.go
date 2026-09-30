// Package observability — единая точка инициализации OpenTelemetry SDK
// (трейсы, метрики, логи) и graceful shutdown.
//
// Конфигурация — через переменные окружения:
//
//	OTEL_EXPORTER_OTLP_ENDPOINT      — адрес OTel Collector (например, otel-collector:4317)
//	OTEL_EXPORTER_OTLP_INSECURE     — "true" отключает TLS (dev)
//	OTEL_SERVICE_NAME               — имя сервиса (используется как service.name)
//	OTEL_RESOURCE_ATTRIBUTES        — доп. атрибуты (формат k=v,k=v)
//	OTEL_METRICS_EXPORTER / OTEL_TRACES_EXPORTER — "otlp" (по умолчанию) или "none"
//	OTEL_SDK_DISABLED               — "true" полностью отключает SDK (для unit-тестов)
//	METRICS_HTTP_ADDR               — ":9095" — адрес прямого Prometheus /metrics endpoint (fallback)
//
// Если OTEL_EXPORTER_OTLP_ENDPOINT не задан или OTEL_SDK_DISABLED=true,
// Init/Shutdown возвращают no-op — можно безопасно вызывать без Collector.
package observability

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"log/slog"
)

// Shutdown — функция graceful завершения SDK. Возвращает первую ошибку.
type Shutdown func(context.Context) error

// NoopShutdown — пустой shutdown для случая "SDK выключен".
func NoopShutdown(context.Context) error { return nil }

// Config — параметры инициализации.
type Config struct {
	ServiceName    string
	ServiceVersion string
	DeploymentEnv  string
	// OTLPEndpoint — общий endpoint OTel Collector. Используется как fallback
	// для всех сигналов, если per-signal endpoint не задан.
	OTLPEndpoint string
	// OTLPTracesEndpoint — host:port для traces (gRPC). OTEL_EXPORTER_OTLP_TRACES_ENDPOINT.
	OTLPTracesEndpoint string
	// OTLPMetricsEndpoint — host:port для metrics (HTTP). OTEL_EXPORTER_OTLP_METRICS_ENDPOINT.
	OTLPMetricsEndpoint string
	// OTLPLogsEndpoint — host:port для logs (gRPC). OTEL_EXPORTER_OTLP_LOGS_ENDPOINT.
	OTLPLogsEndpoint string
	// Insecure — отключает TLS (dev).
	Insecure bool
	// Disabled — true полностью отключает SDK.
	Disabled bool
	// MetricsHTTPAddr — ":9095" для прямого Prometheus /metrics endpoint (fallback).
	MetricsHTTPAddr string
	// ExtraResourceAttrs — дополнительные атрибуты ресурса (k,v).
	ExtraResourceAttrs []attribute.KeyValue
}

// LoadConfig читает Config из окружения.
func LoadConfig(serviceName string) Config {
	cfg := Config{
		ServiceName:    getEnv("OTEL_SERVICE_NAME", serviceName),
		ServiceVersion: getEnv("OTEL_SERVICE_VERSION", "0.2.0"),
		DeploymentEnv:  getEnv("DEPLOYMENT_ENVIRONMENT", "development"),
		OTLPEndpoint:   os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		// Per-signal endpoint имеет приоритет над общим.
		OTLPTracesEndpoint:  getEnv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")),
		OTLPMetricsEndpoint: getEnv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")),
		OTLPLogsEndpoint:    getEnv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")),
		Insecure:            strings.EqualFold(os.Getenv("OTEL_EXPORTER_OTLP_INSECURE"), "true"),
		Disabled:            strings.EqualFold(os.Getenv("OTEL_SDK_DISABLED"), "true"),
		MetricsHTTPAddr:     os.Getenv("METRICS_HTTP_ADDR"),
	}
	// Парсим OTEL_RESOURCE_ATTRIBUTES в формате k=v,k=v.
	if extra := os.Getenv("OTEL_RESOURCE_ATTRIBUTES"); extra != "" {
		for _, pair := range strings.Split(extra, ",") {
			pair = strings.TrimSpace(pair)
			if i := strings.IndexByte(pair, '='); i > 0 {
				k := strings.TrimSpace(pair[:i])
				v := strings.TrimSpace(pair[i+1:])
				if k != "" && v != "" {
					cfg.ExtraResourceAttrs = append(cfg.ExtraResourceAttrs, attribute.String(k, v))
				}
			}
		}
	}
	return cfg
}

// ShutdownFunc агрегирует несколько shutdown-вызовов и возвращает первую ошибку.
func ShutdownFunc(shutdowns ...Shutdown) Shutdown {
	return func(ctx context.Context) error {
		var firstErr error
		for _, fn := range shutdowns {
			if fn == nil {
				continue
			}
			if err := fn(ctx); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}
}

// Init инициализирует TracerProvider, MeterProvider, LoggerProvider и
// контекст-пропагацию. Возвращает агрегированный Shutdown.
//
// Если cfg.Disabled или cfg.OTLPEndpoint == "" — возвращает NoopShutdown.
func Init(ctx context.Context, cfg Config) (Shutdown, error) {
	if cfg.Disabled || cfg.OTLPEndpoint == "" {
		return NoopShutdown, nil
	}

	res, err := buildResource(ctx, cfg)
	if err != nil {
		return NoopShutdown, fmt.Errorf("build resource: %w", err)
	}

	tracerShutdown := initTracer(ctx, cfg, res)
	meterShutdown := initMeter(ctx, cfg, res)
	loggerShutdown := initLogger(ctx, cfg, res)

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return ShutdownFunc(tracerShutdown, meterShutdown, loggerShutdown), nil
}

func buildResource(ctx context.Context, cfg Config) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion(cfg.ServiceVersion),
		semconv.DeploymentEnvironment(cfg.DeploymentEnv),
	}
	attrs = append(attrs, cfg.ExtraResourceAttrs...)
	res, err := resource.New(ctx,
		resource.WithAttributes(attrs...),
		resource.WithSchemaURL(semconv.SchemaURL),
	)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func initTracer(ctx context.Context, cfg Config, res *resource.Resource) Shutdown {
	if !isEnabled("OTEL_TRACES_EXPORTER", cfg) {
		return NoopShutdown
	}
	// Используем отдельный endpoint для traces (если задан), иначе общий.
	endpoint := cfg.OTLPTracesEndpoint
	if endpoint == "" {
		endpoint = cfg.OTLPEndpoint
	}
	// gRPC ожидает host:port, не URL. Если пришёл http://host:port — отрезаем схему.
	endpoint = stripScheme(endpoint)
	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithTimeout(5*time.Second),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "observability: failed to create OTLP gRPC trace exporter to %s: %v\n", endpoint, err)
		return NoopShutdown
	}
	fmt.Fprintf(os.Stderr, "observability: trace exporter ready, endpoint=%s\n", endpoint)

	// BatchSpanProcessor для OTLP (async, batched).
	tp := trace.NewTracerProvider(
		trace.WithResource(res),
		trace.WithBatcher(exporter, trace.WithBatchTimeout(2*time.Second), trace.WithMaxQueueSize(2048)),
		trace.WithSampler(trace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	return func(ctx context.Context) error {
		return tp.Shutdown(ctx)
	}
}

func initMeter(ctx context.Context, cfg Config, res *resource.Resource) Shutdown {
	if !isEnabled("OTEL_METRICS_EXPORTER", cfg) {
		return NoopShutdown
	}

	readers := []metric.Reader{}

	// OTLP HTTP exporter (push в Collector).
	// Metrics идут по HTTP (4318) на /v1/metrics. WithEndpointURL не добавляет
	// path автоматически — передаём полный URL.
	if cfg.OTLPMetricsEndpoint != "" {
		url := cfg.OTLPMetricsEndpoint
		if !strings.Contains(url, "://") {
			url = "http://" + url
		}
		// Добавляем /v1/metrics, если path ещё не указан.
		if !strings.Contains(url[len("http://"):], "/") {
			url += "/v1/metrics"
		}
		exporter, err := otlpmetrichttp.New(ctx,
			otlpmetrichttp.WithEndpointURL(url),
			otlpmetrichttp.WithInsecure(),
		)
		if err == nil {
			readers = append(readers, metric.NewPeriodicReader(exporter, metric.WithInterval(15*time.Second)))
		} else {
			fmt.Fprintf(os.Stderr, "observability: failed to create metrics exporter: %v\n", err)
		}
	}

	// Prometheus exporter для прямого pull из приложения (fallback).
	promExporter, err := otelprom.New()
	if err == nil {
		readers = append(readers, promExporter)
	}

	opts := []metric.Option{metric.WithResource(res)}
	for _, r := range readers {
		opts = append(opts, metric.WithReader(r))
	}
	mp := metric.NewMeterProvider(opts...)
	otel.SetMeterProvider(mp)
	return func(ctx context.Context) error {
		return mp.Shutdown(ctx)
	}
}

// stripScheme убирает http:// или https:// — gRPC клиенты ожидают host:port, не URL.
func stripScheme(endpoint string) string {
	if i := strings.Index(endpoint, "://"); i >= 0 {
		return endpoint[i+3:]
	}
	return endpoint
}

// initLogger создаёт OTel LoggerProvider с BatchProcessor и otelslog.Handler.
// Приложение пишет логи через slog, otelslog направляет их в OTel Collector
// по OTLP/gRPC. Collector дальше маршрутизирует в Loki.
func initLogger(ctx context.Context, cfg Config, res *resource.Resource) Shutdown {
	if !isEnabled("OTEL_LOGS_EXPORTER", cfg) {
		return NoopShutdown
	}
	exporter, err := newLogExporter(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "observability: failed to create OTLP log exporter: %v\n", err)
		return NoopShutdown
	}
	lp := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
	)
	handler := otelslog.NewHandler(cfg.ServiceName, otelslog.WithLoggerProvider(lp))
	slog.SetDefault(slog.New(handler))
	return func(ctx context.Context) error {
		return lp.Shutdown(ctx)
	}
}

func newLogExporter(ctx context.Context, cfg Config) (*otlploggrpc.Exporter, error) {
	endpoint := cfg.OTLPLogsEndpoint
	if endpoint == "" {
		endpoint = cfg.OTLPEndpoint
	}
	return otlploggrpc.New(ctx,
		otlploggrpc.WithEndpoint(stripScheme(endpoint)),
		otlploggrpc.WithInsecure(),
	)
}

// isEnabled проверяет OTEL_*_EXPORTER: "none"/"false"/"off" → false.
func isEnabled(envKey string, _ Config) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(envKey)))
	switch v {
	case "none", "false", "off":
		return false
	}
	return true
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// --- Prometheus HTTP /metrics endpoint (fallback) ---

// StartPrometheusHTTP запускает HTTP-сервер на addr (например, ":9095") с /metrics.
// Используется как fallback, когда Prometheus pull-моделью напрямую забирает метрики
// из приложения, минуя OTel Collector.
func StartPrometheusHTTP(addr string) Shutdown {
	if addr == "" {
		return NoopShutdown
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() { _ = srv.ListenAndServe() }()
	return func(_ context.Context) error {
		return srv.Close()
	}
}

// CounterVec / GaugeVec / HistogramVec — обёртки над prometheus.* для удобства
// регистрации пользовательских бизнес-метрик без прямой зависимости от prometheus.

// BusinessMetrics — набор стандартных метрик для HTTP API сервиса.
// Заводятся в cmd/server через NewBusinessMetrics.
type BusinessMetrics struct {
	registry            *prometheus.Registry
	HTTPRequestsTotal   *prometheus.CounterVec
	HTTPRequestDuration *prometheus.HistogramVec
	UploadsTotal        *prometheus.CounterVec
	UploadDuration      *prometheus.HistogramVec
	UploadErrorsTotal   *prometheus.CounterVec
}

// NewBusinessMetrics создаёт и регистрирует бизнес-метрики.
func NewBusinessMetrics() *BusinessMetrics {
	reg := prometheus.NewRegistry()
	m := &BusinessMetrics{registry: reg}
	m.HTTPRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "avatars_http_requests_total", Help: "Total HTTP requests"},
		[]string{"method", "route", "status"},
	)
	m.HTTPRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "avatars_http_request_duration_seconds",
			Help:    "HTTP request duration",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "route", "status"},
	)
	m.UploadsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "avatars_uploads_total", Help: "Total avatar uploads"},
		[]string{"status"},
	)
	m.UploadDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "avatars_upload_duration_seconds",
			Help:    "Avatar upload duration",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"status"},
	)
	m.UploadErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "avatars_upload_errors_total", Help: "Total avatar upload errors"},
		[]string{"reason"},
	)
	reg.MustRegister(
		m.HTTPRequestsTotal,
		m.HTTPRequestDuration,
		m.UploadsTotal,
		m.UploadDuration,
		m.UploadErrorsTotal,
	)
	return m
}

// Registry возвращает prometheus.Registry для регистрации дополнительных
// метрик или для HTTP-хендлера.
func (m *BusinessMetrics) Registry() *prometheus.Registry { return m.registry }

// Handler возвращает http.Handler с /metrics endpoint для prometheus pull-модели.
func (m *BusinessMetrics) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	return mux
}

// expoter alias to keep unused-import off; not needed at runtime.
// (kept for compatibility with earlier prometheus exporter builds)
var _ = otelprom.New
