package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestOpenLogRestrictsStartupCapability(t *testing.T) {
	path := filepath.Join(t.TempDir(), "studio.log")
	if err := os.WriteFile(path, []byte("previous launch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	log, err := openLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	info, err := log.Stat()
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("startup log is not private: %v %v", info, err)
	}
}

func TestSidecarReceivesChosenAudioSettingsFolder(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "cicada")
	script := "#!/bin/sh\nprintf '%s' \"$CICADA_AUDIO_SETTINGS_DIR\" > \"${0%/*}/settings-path\"\necho 'Cicada Studio: http://127.0.0.1:12345/'\nexec sleep 60\n"
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(t.TempDir(), "Studio profile")
	h := &host{cicada: helper, statePath: filepath.Join(profile, "state.json"), logOut: io.Discard}
	t.Cleanup(h.shutdown)
	if _, err := h.startSidecar(filepath.Join(dir, "main.cicada")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "settings-path"))
	if err != nil || string(got) != filepath.Join(profile, "audio") {
		t.Fatalf("sidecar used a different settings folder: %q %v", got, err)
	}
}

func TestShutdownStopsSidecarDuringAndAfterStartup(t *testing.T) {
	for _, ready := range []bool{false, true} {
		name := "waiting-for-readiness"
		if ready {
			name = "ready"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "sidecar.pid")
			helper := filepath.Join(dir, "cicada")
			script := "#!/bin/sh\necho $$ > '" + marker + "'\n"
			if ready {
				script += "echo 'Cicada Studio: http://127.0.0.1:12345/'\n"
			}
			script += "exec sleep 60\n"
			if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			h := &host{cicada: helper, logOut: io.Discard}
			t.Cleanup(h.shutdown)
			started := make(chan error, 1)
			go func() {
				_, err := h.startSidecar(filepath.Join(dir, "main.cicada"))
				started <- err
			}()
			var pid int
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				contents, _ := os.ReadFile(marker)
				pid, _ = strconv.Atoi(strings.TrimSpace(string(contents)))
				if pid > 0 {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if pid == 0 {
				t.Fatal("sidecar did not start")
			}
			if ready {
				select {
				case err := <-started:
					if err != nil {
						t.Fatalf("start ready sidecar: %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("sidecar did not become ready")
				}
			}
			stopped := make(chan struct{})
			go func() { h.shutdown(); close(stopped) }()
			select {
			case <-stopped:
			case <-time.After(5 * time.Second):
				t.Fatal("shutdown did not cancel startup promptly")
			}
			if !ready {
				if err := <-started; !errors.Is(err, context.Canceled) {
					t.Fatalf("startup error = %v, want cancellation", err)
				}
			}
			if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatalf("sidecar %d survived shutdown: %v", pid, err)
			}
			if _, err := h.startSidecar(filepath.Join(dir, "main.cicada")); !errors.Is(err, context.Canceled) {
				t.Fatalf("host allowed a new launch after shutdown: %v", err)
			}
		})
	}
}
