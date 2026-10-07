package images

import (
	"bytes"
	"image"
	"image/png"
	"testing"

	"github.com/gen2brain/webp"
)

func pngOf(w, h int) *bytes.Reader {
	var buf bytes.Buffer
	png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h)))
	return bytes.NewReader(buf.Bytes())
}

func TestProcessScalesWithoutUpscaling(t *testing.T) {
	p, err := Process(pngOf(1000, 500), []int{320, 1280})
	if err != nil {
		t.Fatal(err)
	}
	want := map[int][2]int{320: {320, 160}, 1280: {1000, 500}}
	for w, size := range want {
		img, err := webp.Decode(bytes.NewReader(p.Variants[w]))
		if err != nil {
			t.Fatalf("variant %d: %v", w, err)
		}
		if b := img.Bounds(); b.Dx() != size[0] || b.Dy() != size[1] {
			t.Errorf("variant %d = %dx%d, want %v", w, b.Dx(), b.Dy(), size)
		}
	}
	thumb, _, err := image.Decode(bytes.NewReader(p.Thumbnail))
	if err != nil || thumb.Bounds().Dx() != ThumbnailWidth {
		t.Errorf("thumbnail: %v %v", err, thumb.Bounds())
	}
}

func TestProcessRejectsNonImages(t *testing.T) {
	if _, err := Process(bytes.NewReader([]byte("<script>")), []int{320}); err == nil {
		t.Error("want decode error")
	}
}

func TestValidate(t *testing.T) {
	var good bytes.Buffer
	png.Encode(&good, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	if err := Validate(good.Bytes()); err != nil {
		t.Errorf("valid png: %v", err)
	}
	for name, data := range map[string][]byte{
		"fake gif header": []byte("GIF89a<?php system($_GET[1]); ?>"),
		"truncated png":   good.Bytes()[:40],
	} {
		if err := Validate(data); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
