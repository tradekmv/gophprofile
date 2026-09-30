package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var dbTracer = otel.Tracer("gophprofile.repository")

// execSpan — обёртка над pool.Exec с трассировкой.
func execSpan(ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) (int64, error) {
	ctx, span := dbTracer.Start(ctx, "db.exec",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.statement", sql),
		),
	)
	defer span.End()
	start := time.Now()
	tag, err := pool.Exec(ctx, sql, args...)
	dur := time.Since(start).Milliseconds()
	span.SetAttributes(attribute.Int64("db.duration_ms", dur))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "exec failed")
		return 0, fmt.Errorf("db exec: %w", err)
	}
	return tag.RowsAffected(), nil
}

// tracedRows — обёртка pgx.Rows, завершающая спан при Close.
type tracedRows struct {
	rows pgx.Rows
	span trace.Span
}

// Close завершает underlying rows и спан.
func (t *tracedRows) Close() {
	if t.rows != nil {
		t.rows.Close()
	}
	if t.span.SpanContext().IsValid() {
		t.span.End()
	}
}

// querySpan запускает запрос и возвращает обёртку (caller обязан вызвать Close).
func querySpan(ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) (*tracedRows, error) {
	ctx, span := dbTracer.Start(ctx, "db.query",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.statement", sql),
		),
	)
	start := time.Now()
	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "query failed")
		span.SetAttributes(attribute.Int64("db.duration_ms", time.Since(start).Milliseconds()))
		span.End()
		return nil, fmt.Errorf("db query: %w", err)
	}
	return &tracedRows{rows: rows, span: span}, nil
}

// queryRowSpan — обёртка для QueryRow с трассировкой через scan-callback.
func queryRowSpan(ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) (pgx.Row, func(...any) error) {
	ctx, span := dbTracer.Start(ctx, "db.query_row",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.statement", sql),
		),
	)
	row := pool.QueryRow(ctx, sql, args...)
	scan := func(dest ...any) error {
		err := row.Scan(dest...)
		if err != nil && err.Error() == pgx.ErrNoRows.Error() {
			span.SetAttributes(attribute.Bool("db.no_rows", true))
		} else if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "scan failed")
		}
		span.End()
		return err
	}
	return row, scan
}
