package images

import (
	"bytes"
	"errors"
	"testing"
)

func TestProcessRejectsDecompressionBomb(t *testing.T) {
	// GIF header declaring 10000x10000 (100 MP) with no pixel data: must be
	// refused from the header alone, before any decode allocation.
	hdr := []byte("GIF89a")
	hdr = append(hdr, 0x10, 0x27, 0x10, 0x27, 0x00, 0x00, 0x00)
	_, err := Process(bytes.NewReader(hdr), []int{320})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}
