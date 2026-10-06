package main

import (
	_ "embed"
	"net/http"

	webhost "m31labs.dev/cicada/host/web"
)

//go:embed studio-capture.js
var studioCaptureUI []byte

func serveCaptureScript(w http.ResponseWriter, bytes []byte) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(bytes)
}

func (s *studio) captureAdapterAsset(w http.ResponseWriter, _ *http.Request) {
	serveCaptureScript(w, webhost.CaptureAdapter())
}
func (s *studio) captureProcessorAsset(w http.ResponseWriter, _ *http.Request) {
	serveCaptureScript(w, webhost.CaptureProcessor())
}
func (s *studio) captureWorkerAsset(w http.ResponseWriter, _ *http.Request) {
	serveCaptureScript(w, webhost.CaptureWorker())
}
func (s *studio) captureClientAsset(w http.ResponseWriter, _ *http.Request) {
	serveCaptureScript(w, webhost.CaptureClient())
}
func (s *studio) captureUIScript(w http.ResponseWriter, _ *http.Request) {
	serveCaptureScript(w, studioCaptureUI)
}
