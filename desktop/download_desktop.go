//go:build windows || darwin

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"m31labs.dev/gosx/desktop"
	"m31labs.dev/gosx/desktop/bridge"
	"m31labs.dev/gosx/desktop/sidecar"
)

type nativeExportSaveRequest struct {
	Path      string `json:"path"`
	RequestID string `json:"requestId"`
}

// WKWebView does not save attachment links by itself. Keep the bytes in Go;
// the bridge carries only a validated media path and bounded completion data.
func (h *host) attachNativeDownloads() (func(), error) {
	if runtime.GOOS != "darwin" {
		return func() {}, nil
	}
	if err := h.app.Bridge().Register("cicada.export.save", h.beginNativeExport); err != nil {
		return nil, err
	}
	if err := h.app.PrependBootstrapScript(nativeDownloadBootstrap); err != nil {
		h.app.Bridge().Unregister("cicada.export.save")
		return nil, err
	}
	return func() {
		h.app.Bridge().Unregister("cicada.export.save")
		h.cancelNativeExport()
	}, nil
}

func (h *host) beginNativeExport(message *bridge.Context) error {
	var request nativeExportSaveRequest
	data := message.RawPayload()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if len(data) > 1024 || decoder.Decode(&request) != nil || len(request.RequestID) == 0 || len(request.RequestID) > 80 {
		return message.Error("cicada.export.invalid", "Choose a completed export to save.")
	}
	for _, char := range request.RequestID {
		if char != '-' && (char < '0' || char > '9') {
			return message.Error("cicada.export.invalid", "Invalid export request.")
		}
	}
	if _, _, err := nativeExportFormat(request.Path); err != nil {
		return message.Error("cicada.export.invalid", "Choose a completed WAV, MIDI, or stems export to save.")
	}
	h.mu.Lock()
	if h.closing || h.proc == nil {
		h.mu.Unlock()
		return message.Error("cicada.export.closed", "Open a song before saving an export.")
	}
	if h.exportCancel != nil {
		h.mu.Unlock()
		return message.Error("cicada.export.busy", "Finish the current save first.")
	}
	proc := h.proc
	ctx, cancel := context.WithCancel(context.Background())
	h.exportCancel = cancel
	h.mu.Unlock()
	if err := message.Respond(map[string]bool{"accepted": true}); err != nil {
		cancel()
		h.mu.Lock()
		h.exportCancel = nil
		h.mu.Unlock()
		return err
	}
	go h.saveNativeExport(ctx, cancel, proc, request)
	return nil
}

func (h *host) saveNativeExport(ctx context.Context, cancel context.CancelFunc, proc *sidecar.Process, request nativeExportSaveRequest) {
	result := map[string]any{"requestId": request.RequestID, "state": "failed"}
	defer func() {
		cancel()
		h.mu.Lock()
		h.exportCancel = nil
		current := !h.closing && h.proc == proc
		h.mu.Unlock()
		if current {
			_ = h.app.Bridge().Emit("cicada.export.saved", result)
		}
	}()
	response, name, err := openNativeExport(ctx, proc.Ready(), request.Path)
	if err != nil {
		result["error"] = err.Error()
		return
	}
	defer response.Body.Close()
	if !h.nativeExportCurrent(ctx, proc) {
		return
	}
	var target string
	if h.smokeDownloadDir != "" {
		err = os.MkdirAll(h.smokeDownloadDir, 0700)
		target = filepath.Join(h.smokeDownloadDir, name)
	} else {
		extension := strings.TrimPrefix(filepath.Ext(name), ".")
		target, err = h.app.SaveFileDialog(desktop.SaveFileOptions{
			Title: "Save Cicada export", InitialFilename: name, DefaultExt: extension,
			Filters: []desktop.FileFilter{{Name: strings.ToUpper(extension) + " export", Pattern: "*." + extension}}, OverwritePrompt: true,
		})
	}
	if err != nil {
		result["error"] = err.Error()
		return
	}
	if target == "" {
		result["state"] = "cancelled"
		return
	}
	if !h.nativeExportCurrent(ctx, proc) {
		return
	}
	if err := saveNativeExport(response, target); err != nil {
		result["error"] = err.Error()
		return
	}
	result["state"], result["name"] = "saved", filepath.Base(target)
	if h.smokeOut != "" {
		file, err := os.Open(target)
		if err == nil {
			hash := sha256.New()
			size, hashErr := io.Copy(hash, file)
			file.Close()
			if hashErr == nil {
				h.recordSmoke("nativeExportSave", map[string]any{"path": target, "bytes": size, "sha256": hex.EncodeToString(hash.Sum(nil)), "mediaPath": request.Path, "pickerBypassed": h.smokeDownloadDir != "", "savedAt": time.Now().UTC().Format(time.RFC3339)})
			}
		}
	}
}

func (h *host) nativeExportCurrent(ctx context.Context, proc *sidecar.Process) bool {
	if ctx.Err() != nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return !h.closing && h.proc == proc
}

func (h *host) cancelNativeExport() {
	h.mu.Lock()
	cancel := h.exportCancel
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
