// Package irfetch downloads the impulse responses that an irasset catalog
// lists. It is apart from irasset so that the score compiler, which decodes
// impulses with irasset, does not link net/http.
package irfetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"m31labs.dev/cicada/audioasset"
	"m31labs.dev/cicada/host/irasset"
)

// Fetch bounds the transfer and verifies its checksum before returning any
// audio. The caller controls cancellation and HTTP timeouts through context
// and client. Call it on a worker or host thread, never an audio owner.
func Fetch(ctx context.Context, client *http.Client, e irasset.Entry) ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("impulse fetch requires an HTTP client")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.URL, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("impulse fetch returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > e.Bytes {
		return nil, errors.New("impulse transfer exceeds declared byte count")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, e.Bytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != e.Bytes {
		return nil, errors.New("impulse transfer differs from declared byte count")
	}
	checksum, err := audioasset.SHA256(bytes.NewReader(data))
	if err != nil || checksum != e.SHA256 {
		return nil, errors.New("impulse transfer checksum mismatch")
	}
	return data, nil
}
