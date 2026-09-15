package imgutil

import (
	"bytes"
	"errors"
	"testing"
)

func TestDetectMimeType(t *testing.T) {
	t.Parallel()
	// Use a payload long enough so bufio.Peek(16) succeeds without EOF.
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01, 0x02, 0x03, 0x04, 0x05}
	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52}
	webp := []byte{'R', 'I', 'F', 'F', 0x10, 0x00, 0x00, 0x00, 'W', 'E', 'B', 'P', 'V', 'P', '8', ' '}
	tests := []struct {
		name    string
		data    []byte
		want    string
		wantErr error
	}{
		{"jpeg", jpeg, "image/jpeg", nil},
		{"png", png, "image/png", nil},
		{"webp", webp, "image/webp", nil},
		{"empty", []byte{}, "", ErrUnsupportedImage},
		{"garbage", append([]byte("not an image"), make([]byte, 16)...), "", ErrUnsupportedImage},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := DetectMimeType(bytes.NewReader(tt.data))
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tt.want {
				t.Errorf("got = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseSize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in        string
		w, h      int
		wantErr   bool
	}{
		{"100x100", 100, 100, false},
		{"300x300", 300, 300, false},
		{"original", 0, 0, false},
		{"", 0, 0, false},
		{"100x", 0, 0, true},
		{"abc", 0, 0, true},
		{"-1x10", 0, 0, true},
		{"0x0", 0, 0, true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			w, h, err := ParseSize(tt.in)
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if !tt.wantErr && (w != tt.w || h != tt.h) {
				t.Errorf("got (%d,%d), want (%d,%d)", w, h, tt.w, tt.h)
			}
		})
	}
}
