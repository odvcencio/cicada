package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStudioLateExternalWriteSurvivesSavesAndRestart(t *testing.T) {
	handler, path := studioTestHandler(t)
	// This handle continues to name the old inode after the first save.
	writer, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	first := strings.Replace(studioScore, "Studio", "First save", 1)
	response := studioCall(t, handler, "/api/source", studioEdit{Revision: studioRevision([]byte(studioScore)), Source: first})
	if response.Code != http.StatusOK {
		t.Fatalf("first save: %d %s", response.Code, response.Body.String())
	}
	var result struct {
		Preserved string `json:"preserved"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Preserved == "" {
		t.Fatal("save did not name the recovery file")
	}
	second := strings.Replace(studioScore, "Studio", "Second save", 1)
	response = studioCall(t, handler, "/api/source", studioEdit{Revision: studioRevision([]byte(first)), Source: second})
	if response.Code != http.StatusOK {
		t.Fatalf("second save: %d %s", response.Code, response.Body.String())
	}
	// A new process must detect writes to a version displaced before it started.
	handler, err = studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	external := strings.Replace(studioScore, "Studio", "Late external save", 1)
	if err := writer.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteAt([]byte(external), 0); err != nil {
		t.Fatal(err)
	}
	if err := writer.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, err := os.ReadFile(result.Preserved)
	if err != nil || string(recovered) != external {
		t.Fatalf("external save lost: %q, %v", recovered, err)
	}
	for _, route := range []string{"/api/state", "/api/source", "/api/toggle"} {
		var body any
		if route != "/api/state" {
			body = studioEdit{Revision: studioRevision([]byte(second)), Source: first, Pattern: "pulse", Step: 0}
		}
		response = studioCall(t, handler, route, body)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), result.Preserved) || !strings.Contains(response.Body.String(), "compare and merge") {
			t.Fatalf("%s did not refuse with recovery instructions: %d %s", route, response.Code, response.Body.String())
		}
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != second {
		t.Fatalf("refused save changed the score: %q, %v", current, err)
	}
	// An unrelated score in the same directory remains editable.
	other := filepath.Join(filepath.Dir(path), "other.cicada")
	if err := os.WriteFile(other, []byte(studioScore), 0600); err != nil {
		t.Fatal(err)
	}
	if err := studioRecoveryConflict(other); err != nil {
		t.Fatalf("unrelated score blocked: %v", err)
	}
}

func TestStudioOpenWriterDuringCommitIsRetained(t *testing.T) {
	_, path := studioTestHandler(t)
	var writer *os.File
	updated := bytes.ReplaceAll([]byte(studioScore), []byte("Studio"), []byte("New score"))
	committed, preserved, err := studioWriteIfRevision(path, updated, 0600, studioRevision([]byte(studioScore)), func() {
		var err error
		writer, err = os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
	})
	if err != nil || !committed {
		t.Fatalf("commit: %v, %v", committed, err)
	}
	defer writer.Close()
	external := []byte(strings.Replace(studioScore, "Studio", "Other editor", 1))
	if err := writer.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteAt(external, 0); err != nil {
		t.Fatal(err)
	}
	if err := writer.Sync(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(preserved)
	if err != nil || !bytes.Equal(content, external) {
		t.Fatalf("late write lost: %q, %v", content, err)
	}
	if err := studioRecoveryConflict(path); err == nil {
		t.Fatal("late write was not detected")
	}
}

func TestStudioRecoveryWithoutReceiptRefusesSave(t *testing.T) {
	_, path := studioTestHandler(t)
	recovery, err := os.CreateTemp(filepath.Dir(path), studioRecoveryPattern(path))
	if err != nil {
		t.Fatal(err)
	}
	defer recovery.Close()
	if err := studioRecoveryConflict(path); err == nil || !strings.Contains(err.Error(), recovery.Name()) {
		t.Fatalf("incomplete save not detected: %v", err)
	}
}

func TestStudioPageSaveDrafts(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is needed for the Studio page script tests")
	}
	output, err := exec.Command(node, "--test", "studio-send.test.cjs").CombinedOutput()
	if err != nil {
		t.Fatalf("Studio page tests: %v\n%s", err, output)
	}
}
