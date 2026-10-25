package main

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// The page can identify a completed export, but cannot choose an origin or a
// filesystem target. The host supplies its active sidecar and native picker.
type nativeExportRequest struct {
	Path string `json:"path"`
}

func nativeExportFormat(path string) (extension, contentType string, err error) {
	for _, format := range []struct{ prefix, extension, contentType string }{
		{"/media/exports/", ".wav", "audio/wav"},
		{"/media/midi-exports/", ".mid", "audio/midi"},
		{"/media/stem-exports/", ".zip", "application/zip"},
	} {
		if !strings.HasPrefix(path, format.prefix) || !strings.HasSuffix(path, format.extension) {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(path, format.prefix), format.extension)
		if len(id) < 1 || len(id) > 64 {
			break
		}
		for _, c := range id {
			if c != '_' && c != '-' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
				return "", "", fmt.Errorf("choose a completed WAV, MIDI, or stems export")
			}
		}
		return format.extension, format.contentType, nil
	}
	return "", "", fmt.Errorf("choose a completed WAV, MIDI, or stems export")
}

func nativeExportOrigin(readyURL string) (*url.URL, error) {
	origin, err := url.Parse(readyURL)
	if err != nil || origin.Scheme != "http" || origin.User != nil || origin.Opaque != "" || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" {
		return nil, fmt.Errorf("Studio's active export service is unavailable")
	}
	host, port, err := net.SplitHostPort(origin.Host)
	ip := net.ParseIP(host)
	number, portErr := strconv.Atoi(port)
	if err != nil || ip == nil || !ip.IsLoopback() || portErr != nil || number < 1 || number > 65535 {
		return nil, fmt.Errorf("Studio exports require an active loopback service")
	}
	// A Ready URL may carry the native session bootstrap capability. It is not
	// an export prefix and must never be appended or forwarded as a query.
	origin.Path, origin.RawPath = "", ""
	return origin, nil
}

// openNativeExport returns a validated response whose body belongs to the
// caller, plus a safe suggested filename. Close the body if the picker is
// cancelled, or pass it to saveNativeExport. Large audio stays in the stream.
func openNativeExport(ctx context.Context, readyURL, exportPath string) (*http.Response, string, error) {
	extension, contentType, err := nativeExportFormat(exportPath)
	if err != nil {
		return nil, "", err
	}
	origin, err := nativeExportOrigin(readyURL)
	if err != nil {
		return nil, "", err
	}
	origin.Path = exportPath
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin.String(), nil)
	if err != nil {
		return nil, "", err
	}
	transport := &http.Transport{
		DialContext:            (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		ResponseHeaderTimeout:  15 * time.Second,
		MaxResponseHeaderBytes: 16 << 10,
		DisableCompression:     true,
		DisableKeepAlives:      true,
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, "", err
	}
	reject := func(err error) (*http.Response, string, error) {
		_ = response.Body.Close()
		return nil, "", err
	}
	if response.StatusCode != http.StatusOK {
		return reject(fmt.Errorf("export download is unavailable (HTTP %d); refresh Export and try again", response.StatusCode))
	}
	actualType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || actualType != contentType {
		return reject(fmt.Errorf("the export service returned an unexpected file format"))
	}
	if encoding := response.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return reject(fmt.Errorf("the export service returned an unexpected encoding"))
	}
	disposition, params, err := mime.ParseMediaType(response.Header.Get("Content-Disposition"))
	name := params["filename"]
	if err != nil || disposition != "attachment" || !nativeExportFilename(name, extension) {
		return reject(fmt.Errorf("the export service returned an invalid download filename"))
	}
	if response.ContentLength == 0 {
		return reject(fmt.Errorf("the completed export is empty"))
	}
	return response, name, nil
}

func nativeExportFilename(name, extension string) bool {
	if len(name) <= len(extension) || len(name) > 255 || filepath.Base(name) != name || strings.TrimSpace(name) != name || filepath.Ext(name) != extension {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) || strings.ContainsRune(`/\:*?"<>|`, c) {
			return false
		}
	}
	// These names are devices on Windows even when an extension follows them.
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return false
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && (base[3] >= '0' && base[3] <= '9') {
		return false
	}
	return true
}

// saveNativeExport consumes and closes a response opened above. The target
// comes only from the native Save dialog. A failed, interrupted or truncated
// download never opens the target for writing: it publishes only a fully
// streamed, synced and closed temporary file beside the selected destination.
func saveNativeExport(response *http.Response, target string) (err error) {
	if response == nil || response.Body == nil {
		return fmt.Errorf("there is no completed export to save")
	}
	bodyClosed := false
	defer func() {
		if !bodyClosed {
			_ = response.Body.Close()
		}
	}()
	if response.Request == nil || response.Request.URL == nil {
		return fmt.Errorf("the export request is unavailable")
	}
	extension, _, err := nativeExportFormat(response.Request.URL.EscapedPath())
	if err != nil {
		return err
	}
	if !filepath.IsAbs(target) || !strings.EqualFold(filepath.Ext(target), extension) {
		return fmt.Errorf("choose an absolute destination ending in %s", extension)
	}
	ctx := response.Request.Context()
	if err := ctx.Err(); err != nil {
		return err
	}
	if info, err := os.Lstat(target); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("choose a regular file as the export destination")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(target), ".cicada-export-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()
	written, copyErr := io.CopyBuffer(file, response.Body, make([]byte, 64<<10))
	closeErr := response.Body.Close()
	bodyClosed = true
	if copyErr != nil {
		return fmt.Errorf("download the complete export: %w", copyErr)
	}
	if closeErr != nil {
		return closeErr
	}
	if written == 0 || response.ContentLength >= 0 && written != response.ContentLength {
		return fmt.Errorf("the export download was incomplete")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	err = file.Close()
	closed = true
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(temporary, target)
}
