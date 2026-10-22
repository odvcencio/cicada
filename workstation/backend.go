package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/control"
)

type backend struct {
	url    *url.URL
	client *http.Client
	token  string
}

func newBackend(address string) (*backend, error) {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("audio service must be a loopback HTTP origin")
	}
	host := u.Hostname()
	if host != "localhost" && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback()) {
		return nil, fmt.Errorf("audio service must be a loopback HTTP origin")
	}
	return &backend{url: u, client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, token: os.Getenv("CICADA_BACKEND_TOKEN")}, nil
}

type backendError struct {
	Status  int
	Message string
}

func (e *backendError) Error() string { return e.Message }

func (b *backend) request(ctx context.Context, method, path string, input any) (*http.Response, error) {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	u := *b.url
	resource, err := url.ParseRequestURI(path)
	if err != nil || resource.IsAbs() || resource.Host != "" {
		return nil, fmt.Errorf("audio service resource must be a local path")
	}
	u.Path, u.RawPath, u.RawQuery = resource.Path, resource.RawPath, resource.RawQuery
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if b.token != "" {
		req.Header.Set("Authorization", "Bearer "+b.token)
	}
	client := b.client
	if method == http.MethodPost && (path == "/api/record" || path == "/api/intents") {
		copy := *client
		copy.Timeout = 30 * time.Second // large retained gesture takes compile before acknowledgment
		client = &copy
	}
	if method == http.MethodPost && path == "/api/arrange" {
		copy := *client
		copy.Timeout = 60 * time.Second // a placement variation checks its rendered sound
		client = &copy
	}
	if payload, ok := input.(map[string]any); ok && method == http.MethodPost && path == "/api/pattern" && payload["action"] == "duplicate" {
		copy := *client
		copy.Timeout = 60 * time.Second // material copies check the original song's sound
		client = &copy
	}
	if method == http.MethodPost && resource.Path == "/api/midi/input" {
		copy := *client
		copy.Timeout = 0 // The mounted Live panel owns this input stream.
		client = &copy
	}
	if strings.HasPrefix(resource.Path, "/api/sampler") || method == http.MethodPost && resource.Path == "/api/audio/config" {
		copy := *client
		copy.Timeout = 60 * time.Second
		if resource.Path == "/api/audio/config" {
			// Wait for the user's native permission response. The browser
			// request context still cancels if its caller leaves.
			copy.Timeout = 0
		}
		client = &copy
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Tymbal service is unavailable: %w", err)
	}
	return response, nil
}

func (b *backend) call(ctx context.Context, method, path string, input, output any) error {
	response, err := b.request(ctx, method, path, input)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	limit := int64(8 << 20)
	if path == "/api/workspace" || path == "/api/intents" || path == "/api/record" {
		limit = 32 << 20
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return fmt.Errorf("audio service response exceeds %d MiB", limit>>20)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &failure)
		if failure.Error == "" {
			failure.Error = strings.TrimSpace(string(data))
		}
		return &backendError{response.StatusCode, failure.Error}
	}
	if method == http.MethodPost {
		if receipt, ok := ctx.Value(workspaceWriteReceiptKey{}).(*workspaceWriteReceipt); ok {
			var acknowledgment workspaceWriteReceipt
			if json.Unmarshal(data, &acknowledgment) == nil && acknowledgment.Revision != "" {
				receipt.Revision = acknowledgment.Revision
			}
		}
	}
	if output == nil {
		return nil
	}
	return json.Unmarshal(data, output)
}

type projectFile struct {
	Name        string                `json:"name"`
	Source      string                `json:"source"`
	Revision    string                `json:"revision"`
	Diagnostics []notation.Diagnostic `json:"diagnostics"`
}

type workspace struct {
	Files           []projectFile    `json:"files"`
	File            string           `json:"-"`
	FileRevision    string           `json:"-"`
	ProjectRevision string           `json:"projectRevision"`
	DiskSource      string           `json:"-"`
	DiskRevision    string           `json:"-"`
	HasDraft        bool             `json:"-"`
	SourceDraft     draft            `json:"-"`
	SourceCopies    []savedSource    `json:"-"`
	RecoveryError   string           `json:"-"`
	Edition         int              `json:"-"`
	Source          string           `json:"source"`
	Revision        string           `json:"revision"`
	SourceRevision  string           `json:"source_revision"`
	Filename        string           `json:"filename"`
	Valid           bool             `json:"valid"`
	Error           string           `json:"error"`
	Project         *project.Project `json:"project"`
}

type transport struct {
	Playing   bool   `json:"playing"`
	Bar       int64  `json:"bar"`
	Step      int64  `json:"step"`
	Backend   string `json:"activeBackend"`
	Error     string `json:"error"`
	Listening string `json:"listeningStatus"`
}

type audioOptions struct {
	InputDevice       string  `json:"inputDevice"`
	OutputDevice      string  `json:"outputDevice"`
	InputEnabled      bool    `json:"inputEnabled"`
	MonitorMuted      bool    `json:"monitorMuted"`
	MonitorGain       float64 `json:"monitorGain"`
	MonitorMode       string  `json:"monitorMode"`
	RecordingOffsetMS float64 `json:"recordingOffsetMS"`
	PeriodFrames      int     `json:"periodFrames"`
}
type audioDevice struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Inputs            int    `json:"inputs"`
	Outputs           int    `json:"outputs"`
	DefaultInput      bool   `json:"defaultInput"`
	DefaultOutput     bool   `json:"defaultOutput"`
	MinPeriod         int    `json:"minPeriod"`
	MaxPeriod         int    `json:"maxPeriod"`
	DefaultPeriod     int    `json:"defaultPeriod"`
	FundamentalPeriod int    `json:"fundamentalPeriod"`
}
type audioState struct {
	SettingsWarning string `json:"settingsWarning"`
	Inventory       struct {
		Supported        bool          `json:"supported"`
		CaptureSupported *bool         `json:"captureSupported"`
		InputAccess      string        `json:"inputAccess"`
		InputMessage     string        `json:"inputMessage"`
		Backend          string        `json:"backend"`
		BackendName      string        `json:"backendName"`
		Host             string        `json:"host"`
		Message          string        `json:"message"`
		Devices          []audioDevice `json:"devices"`
		BufferChoices    []int         `json:"bufferChoices"`
	} `json:"inventory"`
	Options audioOptions         `json:"options"`
	Runtime control.AudioRuntime `json:"runtime"`
	Playing bool                 `json:"playing"`
	Status  string               `json:"status"`
}

type mixerField struct {
	Path        string               `json:"path"`
	Value       any                  `json:"value"`
	SourceValue string               `json:"sourceValue"`
	Choices     []string             `json:"choices"`
	Supported   bool                 `json:"supported"`
	Reason      string               `json:"reason"`
	Descriptor  paramdefs.Descriptor `json:"descriptor"`
}
type mixerSend struct {
	mixerField
	To string `json:"to"`
}
type mixerStrip struct {
	Name    string                `json:"name"`
	ID      string                `json:"id"`
	Fields  map[string]mixerField `json:"fields"`
	Sends   []mixerSend           `json:"sends"`
	Inserts []string              `json:"inserts"`
}
type mixerReturn struct {
	Name   string       `json:"name"`
	Fields []mixerField `json:"fields"`
}
type mixerState struct {
	Edition  int           `json:"edition"`
	Revision string        `json:"revision"`
	Tracks   []mixerStrip  `json:"tracks"`
	Buses    []mixerStrip  `json:"buses"`
	Master   mixerStrip    `json:"master"`
	Returns  []mixerReturn `json:"returns"`
	Effects  []mixerReturn `json:"effects"`
}
