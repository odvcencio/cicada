package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStudioTakeGridPreviewUsesDestinationStepDuration(t *testing.T) {
	source := strings.Replace(studioScore, "pattern pulse acid steps=4 {", "pattern pulse acid steps=4 { step = 1/8t", 1)
	path := filepath.Join(t.TempDir(), "score.cicada")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	page := studioCall(t, handler, "/", nil)
	if page.Code != 200 || !bytes.Contains(page.Body.Bytes(), []byte(`class="pattern" data-step-ticks="320"`)) || !bytes.Contains(page.Body.Bytes(), []byte(`class="pattern drum" data-step-ticks="240"`)) {
		t.Fatalf("pattern step durations absent from Studio page: status %d", page.Code)
	}
	for _, tc := range []struct {
		tick int64
		want string
	}{{140, "step = 1/8t c4 . 5 ."}, {160, "step = 1/8t 1 c4 5 ."}} {
		result, err := recordedTakeSource([]byte(source), "bass", "pulse", []studioTakeNote{{Tick: tc.tick, EndTick: 240, Note: 60, Velocity: 90}})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(result), tc.want) {
			t.Fatalf("tick %d did not commit to the expected triplet step: %s", tc.tick, result)
		}
	}
}
