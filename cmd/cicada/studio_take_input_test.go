package main

import (
	"encoding/json"
	"io"
	"testing"
)

func TestNativeTakeArmUsesNegotiatedInputLayout(t *testing.T) {
	for _, channels := range []int{1, 2} {
		t.Run(map[int]string{1: "mono", 2: "stereo"}[channels], func(t *testing.T) {
			s := newTakeStudio(t, t.TempDir())
			s.transport.sampleRate = 48000
			s.transport.audioNull = true
			s.transport.audioOptions.InputEnabled = true
			audio, device := simulatedCaptureAudio(256, zeroAudioSource{})
			audio.actual.CaptureChannels = channels
			opens := 0
			s.transport.takeInputOpener = func(io.Reader, studioAudioOptions, string, int) (studioAudioDevice, error) {
				opens++
				return audio, nil
			}
			response := studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "arm", Track: "vox", Scene: "main", Revision: studioRevision([]byte(audioTakeScore))})
			if response.Code != 200 {
				t.Fatalf("arm %d-channel device: %d %s", channels, response.Code, response.Body)
			}
			var result struct{ ActiveCapture string }
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			take, err := s.takes.Get(result.ActiveCapture)
			if err != nil || take.Channels != channels || take.Rate != 48000 || opens != 1 || device.starts != 1 {
				t.Fatalf("negotiated format not used: take=%+v opens=%d starts=%d err=%v", take, opens, device.starts, err)
			}
			if s.captureRecorder == nil || !s.captureRecorder.FormatFits(256, channels) {
				t.Fatal("ring does not match the opened device")
			}
		})
	}
}
