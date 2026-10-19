// Package observability — хелперы для оборачивания операций в спаны OpenTelemetry.
package observability

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Tracer возвращает глобальный tracer с указанным именем.
func Tracer(name string) trace.Tracer { return otel.Tracer(name) }

// StartSpan стартует дочерний спан с именем и атрибутами и возвращает ctx+span.
// Если глобальный tracer не инициализирован (Disabled=true), возвращает
// исходный ctx и noop-span.
func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer("gophprofile").Start(ctx, name, trace.WithAttributes(attrs...))
}

// RecordError записывает ошибку в спан и устанавливает статус Error.
func RecordError(span trace.Span, err error) {
	if err == nil || !span.SpanContext().IsValid() {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

// WrapDBError классифицирует ошибки pgx (если будет нужен позже).
func WrapDBError(err error) error {
	if err == nil {
		return nil
	}
	return errors.Join(errors.New("db error"), err)
}
