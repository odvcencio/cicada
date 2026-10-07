package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func continuousAutomationApp(t *testing.T) *httptest.Server {
	t.Helper()
	source, err := os.ReadFile("../examples/continuous-automation.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(source)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if p == nil || len(ds) != 0 {
		t.Fatal(ds)
	}
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/workspace" {
			_ = json.NewEncoder(w).Encode(workspace{Source: string(source), Filename: "continuous-automation.cicada", Revision: "current", Valid: true, Project: p})
		} else {
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(service.Close)
	b, err := newBackend(service.URL)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newApp(b)
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(handler)
	t.Cleanup(app.Close)
	return app
}

func TestContinuousAutomationSessionShowsCurvesAndSceneControls(t *testing.T) {
	app := continuousAutomationApp(t)
	page := getPage(t, app.Client(), app.URL+"/?panel=session")
	for _, text := range []string{`id="continuous-automation"`, `data-continuous-path="bass.cutoff"`, `data-continuous-path="bass.pan"`, `aria-label="bass.cutoff automation"`, "@1.1.1: 400Hz, linear", "@3.1.1: 0.6, smooth", "Bar 5", "Scene automation", "Add or replace point"} {
		if !strings.Contains(page, text) {
			t.Errorf("session is missing %q", text)
		}
	}
}
