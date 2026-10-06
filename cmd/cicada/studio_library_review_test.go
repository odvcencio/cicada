package main

import (
	"os"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestStudioLibraryStopCancelsPendingNativePreview(t *testing.T) {
	_, filename := libraryStudio(t)
	transport := newStudioTransport(filename)
	transport.audioNull = true
	t.Cleanup(transport.stop)
	p, _, err := studioLibraryPreviewProject(filename, studioLibraryItem{Path: "std/synth", Name: "glassbass", Kind: "instrument"})
	if err != nil {
		t.Fatal(err)
	}
	transport.pollMu.Lock()
	locked := true
	defer func() {
		if locked {
			transport.pollMu.Unlock()
		}
	}()
	done := make(chan error, 1)
	go func() { done <- transport.startPreview(filename, p) }()
	// Wait until preparation is blocked inside startPreview, so Stop always
	// races with an earlier request rather than with goroutine scheduling.
	deadline := time.Now().Add(2 * time.Second)
	stack := make([]byte, 128<<10)
	for {
		n := runtime.Stack(stack, true)
		if strings.Contains(string(stack[:n]), "studioTransport).startPreview(") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("preview did not reach preparation")
		}
		time.Sleep(time.Millisecond)
	}
	transport.stopPreview()
	transport.pollMu.Unlock()
	locked = false
	select {
	case err := <-done:
		transport.mu.Lock()
		running := transport.preview != nil
		transport.mu.Unlock()
		if err == nil || running {
			t.Fatal("stopped pending request started playback")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled preview did not finish")
	}
}

func TestStudioLibraryBrowserGateIncludesPanelAndAllocations(t *testing.T) {
	data, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`browser-runner\.sh browser '([^']+)'`).FindSubmatch(data)
	if len(match) != 2 {
		t.Fatal("browser gate selector is missing")
	}
	selector, err := regexp.Compile(string(match[1]))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"TestBrowserStudioLibrary", "TestBrowserStudioLibraryProcessorAllocations"} {
		if !selector.MatchString(name) {
			t.Fatalf("browser gate excludes %s", name)
		}
	}
}
