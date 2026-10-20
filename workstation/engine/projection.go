package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"m31labs.dev/gosx/client/browser"
)

type projectionFetch func(context.Context, browser.FetchRequest) (browser.FetchResponse, error)

// Read-only native projections use the browser's cancellation-safe promise
// boundary. Their local API responses retain the same timeout and decode cap.
func readJSONProjection(ctx context.Context, fetch projectionFetch, url string, timeout time.Duration, maxBytes int, target any) error {
	if ctx == nil {
		return fmt.Errorf("projection context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	response, err := fetch(ctx, browser.FetchRequest{URL: url, Method: "GET"})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("read projection: %w", err)
	}
	if response.Status != 200 {
		return fmt.Errorf("projection unavailable (HTTP %d)", response.Status)
	}
	if len(response.Body) > maxBytes {
		return fmt.Errorf("projection exceeds %d bytes", maxBytes)
	}
	if err := json.NewDecoder(bytes.NewReader(response.Body)).Decode(target); err != nil {
		return err
	}
	return ctx.Err()
}
