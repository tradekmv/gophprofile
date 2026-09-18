// Package httperr — единый формат JSON-ошибок для HTTP-хендлеров.
package httperr

import (
	"errors"
	"fmt"
	"net/http"
)

// Error — структурированная ошибка API с HTTP-кодом.
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"error"`
	Details string `json:"details,omitempty"`
}

// Error возвращает текстовое представление для логирования.
func (e *Error) Error() string {
	if e.Details == "" {
		return e.Code
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Details)
}

// Готовые типизированные ошибки. Для контекста оборачивайте через WithDetails.
var (
	ErrBadRequest         = &Error{Status: http.StatusBadRequest, Code: "bad_request"}
	ErrUnauthorized       = &Error{Status: http.StatusUnauthorized, Code: "unauthorized"}
	ErrForbidden          = &Error{Status: http.StatusForbidden, Code: "forbidden"}
	ErrNotFound           = &Error{Status: http.StatusNotFound, Code: "not_found"}
	ErrPayloadTooLarge    = &Error{Status: http.StatusRequestEntityTooLarge, Code: "payload_too_large"}
	ErrUnsupportedMedia   = &Error{Status: http.StatusUnsupportedMediaType, Code: "unsupported_media_type"}
	ErrInternal           = &Error{Status: http.StatusInternalServerError, Code: "internal_error"}
	ErrServiceUnavailable = &Error{Status: http.StatusServiceUnavailable, Code: "service_unavailable"}
)

// WithDetails возвращает копию err с добавленными деталями.
// Если err — это *Error, переданные details прикрепляются как есть.
// Если err — неизвестная ошибка, она не интерпретируется как HTTP-ошибка;
// вызывающий код должен сам построить подходящий *Error с нейтральным Details
// (или оставить его пустым для 5xx, чтобы не раскрывать внутренние детали).
func WithDetails(err error, details string) *Error {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		clone := *apiErr
		clone.Details = details
		return &clone
	}
	return &Error{Status: http.StatusInternalServerError, Code: "internal_error"}
}

// As извлекает *Error из err, иначе возвращает внутреннюю ошибку без раскрытия
// внутренних деталей клиенту. Для 5xx в Details попадает только нейтральный текст,
// реальная ошибка должна логироваться вызывающим кодом.
func As(err error) *Error {
	if err == nil {
		return nil
	}
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return &Error{Status: http.StatusInternalServerError, Code: "internal_error"}
}
