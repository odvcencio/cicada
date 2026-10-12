package sampleasset

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/kernel/stream"
	"m31labs.dev/cicada/project"
)

func streamPlanFixture(t *testing.T) *StreamPlan {
	t.Helper()
	root := t.TempDir()
	data := testwav.Bytes(48000, 1, 16, 200000, 1)
	if err := os.WriteFile(filepath.Join(root, "take.wav"), data, 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := PrepareStreamPlan(root, []project.Asset{{Name: "take", Path: "take.wav", Format: "wav", Frames: 200000, RateHz: 48000, Channels: 1, SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}}, []uint32{1})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
func TestStreamPlanRetirementClosesPrivateFilesAndJoinsWorkers(t *testing.T) {
	plan := streamPlanFixture(t)
	for range 8 {
		prepared, err := plan.New(4, 48000, 4096)
		if err != nil {
			t.Fatal(err)
		}
		runtime := prepared.(*streamRuntime)
		reader := runtime.Reader(0)
		reader.SeekFrame(90000, 1)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := runtime.WaitReady(ctx, reader); err != nil {
			cancel()
			t.Fatal(err)
		}
		cancel()
		reader.Stop()
		runtime.Retire()
		select {
		case <-runtime.worker.Done():
		case <-time.After(time.Second):
			t.Fatal("retired workers did not terminate")
		}
		var sample [1]float32
		if err := runtime.sources[0].ReadFrames(context.Background(), 0, sample[:], sample[:]); !errors.Is(err, os.ErrClosed) {
			t.Fatalf("private WAV handle retained after retire: %v", err)
		}
		if err := runtime.Close(); err != nil {
			t.Fatal(err)
		}
		if stats := runtime.Stats(); stats.ArenaBytes != 4*6*stream.PageBytes {
			t.Fatalf("arena bound changed: %+v", stats)
		}
	}
}
func TestStreamPlanErrorBeforeRuntimePublicationClosesOpenedSources(t *testing.T) {
	plan := streamPlanFixture(t)
	// An impossible reader count fails after verified file open; Close must also
	// work before the worker starts or the cache is published.
	if _, err := plan.New(stream.MaxReaders+1, 48000, 4096); err == nil {
		t.Fatal("invalid runtime admission passed")
	}
	runtime, err := plan.New(2, 48000, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
}
