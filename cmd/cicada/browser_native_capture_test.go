//go:build browser || browser_soak

package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
)

// Win32 BI_RGB 32-bit scanlines contain B, G, R, and an unused byte. Preserve
// top-down row order and encode an opaque PNG matching the visible client area.
func windowsScreenshotPNG(width, height int, pixels []byte) ([]byte, error) {
	if width <= 0 || height <= 0 || width > int(^uint(0)>>1)/4/height || len(pixels) != width*height*4 {
		return nil, fmt.Errorf("invalid Windows screenshot pixels: %dx%d, %d bytes", width, height, len(pixels))
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for at := 0; at < len(pixels); at += 4 {
		img.Pix[at], img.Pix[at+1], img.Pix[at+2], img.Pix[at+3] = pixels[at+2], pixels[at+1], pixels[at], 255
	}
	var output bytes.Buffer
	if err := png.Encode(&output, img); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
