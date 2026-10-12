package main

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/project"
)

func TestNativeCaptureRetainsSampleAndPreviousTakeAccompaniment(t *testing.T) {
	for _, arrangement := range []bool{false, true} {
		name := "scenes"
		if arrangement {
			name = "arrangement"
		}
		t.Run(name, func(t *testing.T) {
			s := newTakeStudio(t, t.TempDir())
			id := captureTestTake(t, s)
			if err := s.commitTake(id, studioRevision([]byte(audioTakeScore)), nil); err != nil {
				t.Fatal(err)
			}
			source, err := os.ReadFile(s.path)
			if err != nil {
				t.Fatal(err)
			}
			text := string(source) + fmt.Sprintf("\nsampler remembered { asset = %s root = c4 mode = oneshot voices = 2 }\ntrack grains remembered { level = -12dB }\npattern grain notes { c4 . . . }\n", id)
			if arrangement {
				start, end := strings.Index(text, "scene main {"), strings.Index(text, "song { main }")+len("song { main }")
				text = text[:start] + fmt.Sprintf("arrange {\n place previous vox %s-clip { at = @1.1.1 length = 1bars }\n place sample grains grain { at = @1.1.1 length = 1bars }\n place synth bass pulse { at = @1.1.1 length = 1bars }\n}\n", id) + text[end:]
			} else {
				text = strings.Replace(text, "scene main {", "scene main { grains = grain ", 1)
			}
			source = []byte(text)
			if err := os.WriteFile(s.path, source, 0600); err != nil {
				t.Fatal(err)
			}
			p, err := compileStudioSource(s.path, source)
			if err != nil {
				t.Fatal(err)
			}
			before, err := project.CanonicalJSON(p)
			if err != nil {
				t.Fatal(err)
			}
			backing, err := project.CanonicalJSON(captureBacking(p))
			if err != nil || !bytes.Equal(before, backing) {
				t.Fatal("capture changed project content, track kinds or mixer settings")
			}
			// Ordinary playback is the reference for every backing sample.
			initial, err := compileLiveProjectAtRate(s.path, p, 48000)
			if err != nil {
				t.Fatal(err)
			}
			reference, err := liveplay.New(initial, 48000)
			if err != nil {
				t.Fatal(err)
			}
			defer reference.Close()
			audio, _ := simulatedCaptureAudio(256, zeroAudioSource{})
			s.transport.audio, s.transport.sampleRate = audio, 48000
			s.transport.audioOptions = studioAudioOptions{InputEnabled: true, MonitorGain: 0.5, MonitorMode: "stereo"}
			var rawFrames uint64
			var rawChanged bool
			recorder, err := capture.NewRecorder(512, 256, 2, func(_ capture.RecordedBlock, input [][]float32) error {
				rawFrames += uint64(len(input[0]))
				for i := range input[0] {
					rawChanged = rawChanged || input[0][i] != 0.03125 || input[1][i] != -0.0625
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer recorder.Close()
			if err := s.transport.armCapture(recorder); err != nil {
				t.Fatal(err)
			}
			if err := s.transport.startCapture(capture.Calibration{}); err != nil {
				t.Fatal(err)
			}
			for index, track := range p.Tracks {
				if got, ok := s.transport.stream.TrackIndex(track.ID); !ok || int(got) != index {
					t.Fatalf("capture changed track index for %s", track.ID)
				}
			}
			countIn := recorder.Snapshot().CountInFrames
			input := [][]float32{make([]float32, 256), make([]float32, 256)}
			output := [][]float32{make([]float32, 256), make([]float32, 256)}
			want := [][]float32{make([]float32, 256), make([]float32, 256)}
			for i := range input[0] {
				input[0][i], input[1][i] = 0.03125, -0.0625
			}
			pcm := make([]byte, 256*8)
			var total int64
			var backingEnergy float64
			for total < countIn+1024 {
				simulatedCapturePeriod(t, audio, uint64(total), input, output)
				delay := int(min(int64(256), max(int64(0), countIn-total)))
				if delay < 256 {
					inTail := [][]float32{input[0][delay:], input[1][delay:]}
					outTail := [][]float32{want[0][delay:], want[1][delay:]}
					if err := renderStudioAudioPeriod(reference, pcm, inTail, outTail, studioMonitorOptions(s.transport.audioOptions)); err != nil {
						t.Fatal(err)
					}
					for ch := range output {
						for i := delay; i < 256; i++ {
							if output[ch][i] != want[ch][i] {
								t.Fatalf("capture changed accompaniment at frame %d channel %d: %g != playback %g", total+int64(i)-countIn, ch, output[ch][i], want[ch][i])
							}
							backing := float64(want[ch][i] - input[ch][i]*0.5)
							backingEnergy += backing * backing
						}
					}
				}
				total += 256
			}
			s.transport.stop()
			if err := s.transport.disarmCapture(); err != nil {
				t.Fatal(err)
			}
			if rawChanged || rawFrames != uint64(total) || recorder.Snapshot().Incomplete {
				t.Fatalf("raw recording contains backing/monitoring or lost frames: changed=%v frames=%d/%d snapshot=%+v", rawChanged, rawFrames, total, recorder.Snapshot())
			}
			if math.IsNaN(backingEnergy) || backingEnergy < 0.01 {
				t.Fatalf("backing was silent: energy=%g", backingEnergy)
			}
			after, _ := project.CanonicalJSON(p)
			current, err := os.ReadFile(s.path)
			if err != nil || !bytes.Equal(source, current) || !bytes.Equal(before, after) {
				t.Fatal("capture changed score or source project ownership")
			}
		})
	}
}
