package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

var errStorage = errors.New("injected storage failure")
var errClose = errors.New("injected close failure")

type failingWAV struct {
	remaining          int
	writeErr, closeErr error
	short              bool
	closed             bool
}

func (f *failingWAV) Write(p []byte) (int, error) {
	if f.short {
		return len(p) - 1, nil
	}
	if len(p) > f.remaining {
		n := f.remaining
		f.remaining = 0
		return n, f.writeErr
	}
	f.remaining -= len(p)
	return len(p), nil
}
func (f *failingWAV) Close() error { f.closed = true; return f.closeErr }

func TestWAVStorageErrorsReachCaller(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		remaining                int
		writeErr, closeErr, want error
		short                    bool
	}{
		{name: "header", writeErr: errStorage, want: errStorage},
		{name: "partial header", remaining: 8, writeErr: errStorage, want: errStorage},
		{name: "sample", remaining: 45, writeErr: errStorage, want: errStorage},
		{name: "short write without error", short: true, want: io.ErrShortWrite},
		{name: "close", remaining: 1000, closeErr: errClose, want: errClose},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &failingWAV{remaining: tc.remaining, writeErr: tc.writeErr, closeErr: tc.closeErr, short: tc.short}
			err := writeWAV(f, []float64{.1, -.2}, []float64{-.1, .2})
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if !f.closed {
				t.Fatal("WAV must be closed even after a write failure")
			}
		})
	}
}
func TestWAVRetainsBothWriteAndCloseErrors(t *testing.T) {
	f := &failingWAV{writeErr: errStorage, closeErr: errClose}
	err := writeWAV(f, []float64{0}, []float64{0})
	if !errors.Is(err, errStorage) || !errors.Is(err, errClose) {
		t.Fatalf("lost storage or close failure: %v", err)
	}
}
func TestDemoDoesNotReportSuccessAfterFilesystemFailure(t *testing.T) {
	t.Run("create output directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "regular-file")
		if err := os.WriteFile(path, []byte("existing"), 0600); err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		if err := run(path, &output); err == nil {
			t.Fatal("directory creation failure was ignored")
		}
		if output.Len() != 0 {
			t.Fatal("reported success after output setup failure")
		}
	})
	t.Run("metrics", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "metrics.json"), 0700); err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		if err := run(dir, &output); err == nil {
			t.Fatal("metrics write failure was ignored")
		}
		if output.Len() != 0 {
			t.Fatal("reported success before metrics were saved")
		}
	})
}
