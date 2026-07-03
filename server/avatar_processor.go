package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"strings"

	"github.com/tinode/chat/server/logs"
)

const (
	avatarMaxSide  = 200
	avatarMaxBytes = 50_000 // 50KB target
)

// processAvatarPhoto checks if public.photo contains inline base64 data
// and replaces it with a compressed version.
func processAvatarPhoto(public map[string]any) map[string]any {
	if public == nil {
		return public
	}

	photoRaw, ok := public["photo"]
	if photoRaw == nil {
		return public
	}

	// Case 1: photo is a string — legacy format, skip
	if _, isStr := photoRaw.(string); isStr {
		return public
	}

	// Case 2: photo is a map (object)
	photo, ok := photoRaw.(map[string]any)
	if !ok {
		return public
	}

	// If photo already has "ref" — it's an S3 link, nothing to do
	if _, hasRef := photo["ref"]; hasRef {
		return public
	}

	// If no "data" field — nothing to compress
	dataStr, _ := photo["data"].(string)
	if dataStr == "" {
		return public
	}

	// Decode base64
	rawBytes, err := base64.StdEncoding.DecodeString(dataStr)
	if err != nil {
		rawBytes, err = base64.RawURLEncoding.DecodeString(dataStr)
		if err != nil {
			logs.Warn.Printf("avatar: failed to decode base64: %v", err)
			return public
		}
	}

	// Already small enough
	if len(rawBytes) <= avatarMaxBytes {
		return public
	}

	// Decode image
	mimeType, _ := photo["type"].(string)
	img, _, err := imageDecode(bytes.NewReader(rawBytes), mimeType)
	if err != nil {
		logs.Warn.Printf("avatar: failed to decode image: %v", err)
		return public
	}

	// Resize
	resized := resizeImage(img, avatarMaxSide)

	// Re-encode as JPEG
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, resized, &jpeg.Options{Quality: 70}); err != nil {
		logs.Warn.Printf("avatar: failed to encode JPEG: %v", err)
		return public
	}

	compressed := buf.Bytes()
	if len(compressed) >= len(rawBytes) {
		return public
	}

	// Replace photo with compressed version
	newPhoto := make(map[string]any, len(photo))
	for k, v := range photo {
		newPhoto[k] = v
	}
	newPhoto["type"] = "image/jpeg"
	newPhoto["data"] = base64.StdEncoding.EncodeToString(compressed)
	public["photo"] = newPhoto

	logs.Info.Printf("avatar: compressed %d -> %d bytes (%.0f%% reduction)",
		len(rawBytes), len(compressed), float64(len(compressed))/float64(len(rawBytes))*100)

	return public
}

func imageDecode(r *bytes.Reader, mimeType string) (image.Image, string, error) {
	switch {
	case strings.Contains(mimeType, "png"):
		img, err := png.Decode(r)
		return img, "image/png", err
	default:
		img, err := jpeg.Decode(r)
		if err != nil {
			r.Seek(0, 0)
			img, err = png.Decode(r)
			return img, "image/png", err
		}
		return img, "image/jpeg", err
	}
}

func resizeImage(src image.Image, maxSide int) image.Image {
	bounds := src.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	if w <= maxSide && h <= maxSide {
		return src
	}

	var newW, newH int
	if w > h {
		newW = maxSide
		newH = h * maxSide / w
	} else {
		newH = maxSide
		newW = w * maxSide / h
	}

	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	// Nearest-neighbor scaling using standard library only
	srcNRGBA := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(srcNRGBA, srcNRGBA.Bounds(), src, bounds.Min, draw.Src)

	for y := 0; y < newH; y++ {
		for x := 0; x < newW; x++ {
			srcX := x * w / newW
			srcY := y * h / newH
			dst.Set(x, y, srcNRGBA.At(srcX, srcY))
		}
	}

	return dst
}
