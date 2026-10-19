//go:build darwin

package main

import (
	"os"
	"testing"
	"time"

	"m31labs.dev/cicada/internal/audiobackend"
)

// A real output-only check. Never grant, revoke, or open microphone access.
func TestCoreAudioMissingPermissionStillPlays(t *testing.T) {
	if os.Getenv("CICADA_COREAUDIO_TEST") != "1" {
		t.Skip("set CICADA_COREAUDIO_TEST=1 for native output qualification")
	}
	backend, err := audiobackend.New(audiobackend.Tymbal)
	if err != nil {
		t.Fatal(err)
	}
	access, err := audiobackend.QueryInputAccess(backend)
	if err != nil {
		t.Fatal(err)
	}
	if access == audiobackend.InputAccessGranted {
		t.Skip("permission is already granted; never revoke it for a test")
	}
	options := defaultStudioAudioOptionsFor(audiobackend.Tymbal)
	options.InputEnabled = true
	rate, err := studioAudioSampleRate("tymbal", options)
	if err != nil {
		t.Fatal(err)
	}
	audio, err := openStudioAudio(zeroAudioSource{}, options, "tymbal", rate)
	if err != nil {
		t.Fatal(err)
	}
	defer audio.Close()
	if snapshot := audio.Snapshot(); snapshot.InputChannels != 0 || snapshot.Warning == "" || audio.Armed() {
		t.Fatalf("input permission was ignored: %+v", snapshot)
	}
	if err := audio.Play(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	audio.Pause()
	snapshot := audio.Snapshot()
	if err := audio.Err(); err != nil {
		t.Fatal(err)
	}
	if snapshot.Callbacks < 20 || snapshot.Dropouts != 0 || snapshot.InputChannels != 0 {
		t.Fatalf("permission blocked native output: %+v", snapshot)
	}
	t.Logf("access=%s rate=%d callbacks=%d dropouts=%d input_channels=%d", access, rate, snapshot.Callbacks, snapshot.Dropouts, snapshot.InputChannels)
}
