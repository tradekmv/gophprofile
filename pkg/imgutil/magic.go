// Package imgutil — вспомогательные функции для работы с изображениями.
package imgutil

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Поддерживаемые MIME-типы и их сигнатуры (magic bytes).
var signatures = []struct {
	mime   string
	prefix []byte // сигнатура для проверки
	offset int    // 0 для JPEG/PNG, 8 для WebP (там "WEBP" идёт после "RIFF"+размер)
}{
	{"image/jpeg", []byte{0xFF, 0xD8, 0xFF}, 0},
	{"image/png", []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, 0},
	{"image/webp", []byte("WEBP"), 8},
}

// ErrUnsupportedImage возвращается, если формат не распознан по magic bytes.
var ErrUnsupportedImage = errors.New("unsupported image type")

// DetectMimeType читает первые 16 байт из r, определяет формат по magic bytes
// и возвращает MIME-тип. Reader остаётся валидным для последующего чтения.
func DetectMimeType(r io.Reader) (string, error) {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}

	// Peek может вернуть меньше байт с ошибкой если источник короткий —
	// этого всё равно достаточно для сравнения сигнатур (например, PNG).
	header, _ := br.Peek(16)
	if len(header) == 0 {
		return "", ErrUnsupportedImage
	}

	for _, sig := range signatures {
		if len(header) < sig.offset+len(sig.prefix) {
			continue
		}
		match := true
		for i, b := range sig.prefix {
			if header[sig.offset+i] != b {
				match = false
				break
			}
		}
		if match {
			return sig.mime, nil
		}
	}
	return "", ErrUnsupportedImage
}

// ParseSize парсит строки "100x100", "300x300" или "original" в width/height.
// Для неизвестных значений возвращает ErrUnsupportedImage.
func ParseSize(s string) (width, height int, err error) {
	lower := strings.ToLower(strings.TrimSpace(s))
	if lower == "original" || lower == "" {
		return 0, 0, nil
	}
	parts := strings.SplitN(lower, "x", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("%w: %q", ErrUnsupportedImage, s)
	}
	w, errW := strconv.Atoi(parts[0])
	h, errH := strconv.Atoi(parts[1])
	if errW != nil || errH != nil {
		return 0, 0, fmt.Errorf("%w: %q", ErrUnsupportedImage, s)
	}
	if w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("%w: %q", ErrUnsupportedImage, s)
	}
	return w, h, nil
}
