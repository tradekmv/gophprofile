package httperr

import (
	"errors"
	"net/http"
	"testing"
)

func TestWithDetails(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		base       *Error
		details    string
		wantStatus int
		wantCode   string
		wantDetail string
	}{
		{"bad request details", ErrBadRequest, "missing X-User-ID", http.StatusBadRequest, "bad_request", "missing X-User-ID"},
		{"not found details", ErrNotFound, "avatar xyz", http.StatusNotFound, "not_found", "avatar xyz"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := WithDetails(tt.base, tt.details)
			if got.Status != tt.wantStatus {
				t.Errorf("Status = %d, want %d", got.Status, tt.wantStatus)
			}
			if got.Code != tt.wantCode {
				t.Errorf("Code = %q, want %q", got.Code, tt.wantCode)
			}
			if got.Details != tt.wantDetail {
				t.Errorf("Details = %q, want %q", got.Details, tt.wantDetail)
			}
			// Original base must not be mutated.
			if tt.base.Details != "" {
				t.Errorf("original base mutated")
			}
		})
	}
}

func TestAs(t *testing.T) {
	t.Parallel()

	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		if got := As(nil); got != nil {
			t.Errorf("As(nil) = %v, want nil", got)
		}
	})

	t.Run("api error passes through", func(t *testing.T) {
		t.Parallel()
		base := ErrForbidden
		got := As(base)
		if got != base {
			t.Errorf("As(ErrForbidden) did not return the same pointer")
		}
	})

	t.Run("generic error becomes internal without leaking details", func(t *testing.T) {
		t.Parallel()
		got := As(errors.New("boom: internal secret"))
		if got.Status != http.StatusInternalServerError {
			t.Errorf("Status = %d, want 500", got.Status)
		}
		if got.Code != "internal_error" {
			t.Errorf("Code = %q, want internal_error", got.Code)
		}
		if got.Details != "" {
			t.Errorf("Details = %q, want empty (must not leak internal error text)", got.Details)
		}
	})
}
