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
	"m31labs.dev/cicada/project"
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

func (b *backend) call(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	u := *b.url
	u.Path, u.RawQuery = path, ""
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if b.token != "" {
		req.Header.Set("Authorization", "Bearer "+b.token)
	}
	response, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("Tymbal service is unavailable: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 8<<20+1))
	if err != nil {
		return err
	}
	if len(data) > 8<<20 {
		return fmt.Errorf("audio service response exceeds 8 MiB")
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
	if output == nil {
		return nil
	}
	return json.Unmarshal(data, output)
}

type workspace struct {
	DiskSource   string           `json:"-"`
	DiskRevision string           `json:"-"`
	HasDraft     bool             `json:"-"`
	Edition      int              `json:"-"`
	Source       string           `json:"source"`
	Revision     string           `json:"revision"`
	Filename     string           `json:"filename"`
	Valid        bool             `json:"valid"`
	Error        string           `json:"error"`
	Project      *project.Project `json:"project"`
}

type transport struct {
	Playing bool   `json:"playing"`
	Bar     int64  `json:"bar"`
	Step    int64  `json:"step"`
	Backend string `json:"activeBackend"`
	Error   string `json:"error"`
}

type audioOptions struct {
	InputDevice  string  `json:"inputDevice"`
	OutputDevice string  `json:"outputDevice"`
	InputEnabled bool    `json:"inputEnabled"`
	MonitorMuted bool    `json:"monitorMuted"`
	MonitorGain  float64 `json:"monitorGain"`
	MonitorMode  string  `json:"monitorMode"`
}
type audioState struct {
	Inventory struct {
		BackendName string `json:"backendName"`
		Message     string `json:"message"`
		Devices     []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Inputs  int    `json:"inputs"`
			Outputs int    `json:"outputs"`
		} `json:"devices"`
	} `json:"inventory"`
	Options audioOptions `json:"options"`
	Playing bool         `json:"playing"`
	Status  string       `json:"status"`
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
