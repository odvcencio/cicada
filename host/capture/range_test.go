package capture

import "testing"

func TestRecorderRangeEndsAtMappedInputBoundary(t *testing.T) {
	for _, latency := range []int64{0, 2} {
		t.Run(string(rune('0'+latency)), func(t *testing.T) {
			var blocks []RecordedBlock
			r, err := NewRecorder(16, 4, 1, func(b RecordedBlock, _ [][]float32) error { blocks = append(blocks, b); return nil })
			if err != nil {
				t.Fatal(err)
			}
			if err = r.BeginRange(CountIn{Frames: 5}, Calibration{}, 7); err != nil {
				t.Fatal(err)
			}
			input, output := [][]float32{{1, 2, 3, 4}}, [][]float32{make([]float32, 4)}
			engine := int64(0)
			for period := 0; period < 8; period++ {
				b := Block{DeviceEpoch: 1, EngineEpoch: 1, DeviceFrame: uint64(period * 4), EngineFrame: engine, SampleRate: 48000, Frames: 4, Layout: LayoutMono, InputLatencyValid: true, OutputLatencyValid: true, InputLatencyNano: latency * 1000000000 / 48000}
				r.Capture(b, input)
				delay := r.LeadIn(output)
				engine += int64(4 - delay)
			}
			if err = r.Close(); err != nil {
				t.Fatal(err)
			}
			got := r.Snapshot()
			if !got.Completed || got.WrittenFrames != uint64(12+latency) || got.Incomplete {
				t.Fatalf("range snapshot %+v", got)
			}
			last := blocks[len(blocks)-1]
			if last.Placement.EngineFrame+int64(last.Timing.Frames) != 7 {
				t.Fatalf("punch out drift: %+v", last)
			}
			if blocks[0].Placement.EngineFrame != -5-latency {
				t.Fatalf("lost count-in or latency: %+v", blocks[0])
			}
		})
	}
}
