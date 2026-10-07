// Package images makes thumbnails and responsive WebP variants of uploads.
package images

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"

	_ "image/gif"
	_ "image/png"

	"github.com/gen2brain/webp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// MaxPixels caps decoded image size (40 MP, ~160 MB as RGBA) so a small
// "decompression bomb" upload cannot exhaust worker memory.
const MaxPixels = 40_000_000

// ErrTooLarge is returned for images over MaxPixels; retrying cannot help.
var ErrTooLarge = errors.New("image too large")

// ThumbnailWidth is the width thumbnails are scaled down to.
const ThumbnailWidth = 480

// Processed is everything made from one upload. Variants maps each
// requested width to a WebP no wider than the original (never upscaled).
type Processed struct {
	Thumbnail []byte // JPEG
	Variants  map[int][]byte
}

// ErrInvalid is returned for data that does not decode as a supported image.
var ErrInvalid = errors.New("invalid image")

// Validate fully decodes an upload so files that only fake an image header
// are refused before they are stored.
func Validate(data []byte) error {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return ErrInvalid
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > MaxPixels {
		return ErrTooLarge
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return ErrInvalid
	}
	return nil
}

// Process decodes a JPEG, PNG, GIF or WebP image once and encodes the JPEG
// thumbnail and the WebP variants. Re-encoding also drops EXIF metadata.
func Process(r io.Reader, widths []int) (Processed, error) {
	// Read the header first and refuse oversized images before allocating.
	var head bytes.Buffer
	cfg, _, err := image.DecodeConfig(io.TeeReader(r, &head))
	if err != nil {
		return Processed{}, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return Processed{}, fmt.Errorf("%w: %dx%d exceeds %d pixels", ErrTooLarge, cfg.Width, cfg.Height, MaxPixels)
	}
	src, _, err := image.Decode(io.MultiReader(&head, r))
	if err != nil {
		return Processed{}, err
	}
	var out Processed
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, scale(src, ThumbnailWidth), &jpeg.Options{Quality: 80}); err != nil {
		return out, err
	}
	out.Thumbnail = bytes.Clone(buf.Bytes())
	out.Variants = make(map[int][]byte, len(widths))
	for _, w := range widths {
		buf.Reset()
		if err := webp.Encode(&buf, scale(src, w), webp.Options{Quality: 78, Method: 4}); err != nil {
			return out, err
		}
		out.Variants[w] = bytes.Clone(buf.Bytes())
	}
	return out, nil
}

// Thumbnail returns only the JPEG thumbnail.
func Thumbnail(r io.Reader) ([]byte, error) {
	p, err := Process(r, nil)
	return p.Thumbnail, err
}

// scale returns src at most width wide, keeping the aspect ratio.
func scale(src image.Image, width int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > width {
		w, h = width, max(1, h*width/w)
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}
