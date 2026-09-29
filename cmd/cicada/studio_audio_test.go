package main

import (
	"os"
	"strings"
	"testing"

	webhost "m31labs.dev/cicada/host/web"
)

func TestStudioKernelImageCarriesSourceRevision(t *testing.T) {
	handler, path := studioTestHandler(t)
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	response := studioCall(t, handler, "/api/kernel-image?rate=48000", nil)
	if response.Code != 200 || response.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("kernel image response: %d %s", response.Code, response.Body.String())
	}
	revision := studioRevision(source)
	if response.Header().Get("X-Cicada-Revision") != revision || response.Header().Get("ETag") != `"`+revision+`"` {
		t.Fatalf("kernel image revision headers: %v", response.Header())
	}
	if !strings.HasPrefix(response.Body.String(), "CIC1") {
		t.Fatal("kernel image does not have the CIC1 header")
	}
	badRate := studioCall(t, handler, "/api/kernel-image?rate=22050", nil)
	if badRate.Code != 400 {
		t.Fatalf("unsupported sample rate accepted: %d", badRate.Code)
	}
}

func TestStudioServesEmbeddedWorkletAssets(t *testing.T) {
	handler, _ := studioTestHandler(t)
	processor := studioCall(t, handler, "/audio/cicada-processor.js", nil)
	if processor.Code != 200 || !strings.Contains(processor.Body.String(), "registerProcessor") {
		t.Fatalf("processor asset response: %d", processor.Code)
	}
	if len(webhost.Processor()) > 5120 {
		t.Fatalf("AudioWorklet processor is %d bytes, exceeds 5120", len(webhost.Processor()))
	}
	client := studioCall(t, handler, "/audio/cicada-client.js", nil)
	if client.Code != 200 || !strings.Contains(client.Body.String(), "cicadaBrowserAudio") {
		t.Fatalf("client asset response: %d", client.Code)
	}
}
