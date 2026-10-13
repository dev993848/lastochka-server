package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

// encodeTestPNG builds a PNG of the given size filled with a high-entropy
// deterministic pattern (incompressible enough for size tests).
func encodeTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8((x*7919 + y*104729) % 256)
			img.Set(x, y, color.RGBA{R: v, G: 255 - v, B: uint8((x * y) % 256), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func photoMap(data, mime string) map[string]any {
	return map[string]any{"photo": map[string]any{"data": data, "type": mime}}
}

func TestProcessAvatarPhotoPassthrough(t *testing.T) {
	if got := processAvatarPhoto(nil); got != nil {
		t.Error("nil must pass through")
	}
	empty := map[string]any{}
	if got := processAvatarPhoto(empty); len(got) != 0 {
		t.Error("map without photo must pass through")
	}
	legacy := map[string]any{"photo": "http://example.com/a.jpg"}
	processAvatarPhoto(legacy)
	if _, ok := legacy["photo"].(string); !ok {
		t.Error("legacy string photo must pass through")
	}
	ref := map[string]any{"photo": map[string]any{"ref": "s3key", "type": "image/jpeg"}}
	processAvatarPhoto(ref)
	if _, hasData := ref["photo"].(map[string]any)["data"]; hasData {
		t.Error("S3 ref photo must pass through")
	}
	broken := photoMap("!!!not-base64!!!", "image/png")
	processAvatarPhoto(broken)
	if broken["photo"].(map[string]any)["data"] != "!!!not-base64!!!" {
		t.Error("undecodable base64 must pass through")
	}

	// Small valid image passes through untouched.
	small := base64.StdEncoding.EncodeToString(encodeTestPNG(t, 10, 10))
	smallPhoto := photoMap(small, "image/png")
	processAvatarPhoto(smallPhoto)
	if smallPhoto["photo"].(map[string]any)["data"] != small {
		t.Error("small image must pass through untouched")
	}
}

func TestProcessAvatarPhotoCompresses(t *testing.T) {
	raw := encodeTestPNG(t, 500, 500)
	if len(raw) <= avatarMaxBytes {
		t.Fatalf("test precondition: image should exceed %d bytes, got %d", avatarMaxBytes, len(raw))
	}
	public := photoMap(base64.StdEncoding.EncodeToString(raw), "image/png")

	got := processAvatarPhoto(public)
	photo := got["photo"].(map[string]any)
	if photo["type"] != "image/jpeg" {
		t.Errorf("expected recompressed image/jpeg, got %v", photo["type"])
	}
	compressed, err := base64.StdEncoding.DecodeString(photo["data"].(string))
	if err != nil {
		t.Fatalf("compressed data is not base64: %v", err)
	}
	if len(compressed) >= len(raw) {
		t.Errorf("compressed %d bytes not smaller than original %d", len(compressed), len(raw))
	}
	img, err := jpeg.Decode(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("compressed data is not a valid JPEG: %v", err)
	}
	if b := img.Bounds(); b.Dx() > avatarMaxSide || b.Dy() > avatarMaxSide {
		t.Errorf("compressed dims %v exceed max side %d", b, avatarMaxSide)
	}
}

func TestResizeImage(t *testing.T) {
	wide := image.NewRGBA(image.Rect(0, 0, 400, 100))
	if got := resizeImage(wide, 200); got.Bounds().Dx() != 200 || got.Bounds().Dy() != 50 {
		t.Errorf("wide resize: got %v, want 200x50", got.Bounds())
	}
	tall := image.NewRGBA(image.Rect(0, 0, 100, 400))
	if got := resizeImage(tall, 200); got.Bounds().Dx() != 50 || got.Bounds().Dy() != 200 {
		t.Errorf("tall resize: got %v, want 50x200", got.Bounds())
	}
	small := image.NewRGBA(image.Rect(0, 0, 50, 50))
	if got := resizeImage(small, 200); got.Bounds() != small.Bounds() {
		t.Errorf("small image must be returned as-is, got %v", got.Bounds())
	}
}

func TestImageDecodeFormats(t *testing.T) {
	pngBytes := encodeTestPNG(t, 8, 8)
	if _, format, err := imageDecode(bytes.NewReader(pngBytes), "image/png"); err != nil || format != "image/png" {
		t.Errorf("png decode: format=%q err=%v", format, err)
	}
	var jbuf bytes.Buffer
	if err := jpeg.Encode(&jbuf, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	if _, format, err := imageDecode(bytes.NewReader(jbuf.Bytes()), "image/jpeg"); err != nil || format != "image/jpeg" {
		t.Errorf("jpeg decode: format=%q err=%v", format, err)
	}
	// Unknown mime with PNG bytes falls back to PNG via retry.
	if _, format, err := imageDecode(bytes.NewReader(pngBytes), "image/webp"); err != nil || format != "image/png" {
		t.Errorf("fallback decode: format=%q err=%v", format, err)
	}
	if _, _, err := imageDecode(bytes.NewReader([]byte("garbage")), "image/jpeg"); err == nil {
		t.Error("garbage bytes must fail decoding")
	}
}
