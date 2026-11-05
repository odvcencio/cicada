package engine

import (
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/fx/chorus"
)

// BenchmarkChorusEngine compares the same 16 sounding voices with and without
// a chorus on every track. Divide the per-block difference by 16*128 to
// estimate the integrated cost per chorus stereo frame.
func BenchmarkChorusEngine(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		name := "dry"
		if enabled {
			name = "chorus"
		}
		b.Run(name, func(b *testing.B) {
			cfg := Config{SampleRate: 48000, MaxBlock: 128, Tracks: 16, MaxVoices: 16, BPMMilli: 120000}
			params := chorus.DefaultParams()
			var commands [16]cmd.Command
			for i := 0; i < cfg.Tracks; i++ {
				cfg.Track[i].Kind = VoiceAcid
				if enabled {
					cfg.Track[i].InsertChain = []TrackEffectConfig{{Kind: TrackEffectChorus, Chorus: &params}}
				}
				commands[i] = cmd.Command{Op: cmd.OpNoteOn, Track: uint8(i), Arg0: 45 | 100<<8}
			}
			e, err := New(cfg)
			if err != nil {
				b.Fatal(err)
			}
			defer e.Close()
			if !e.PushBatch(commands[:]) {
				b.Fatal("notes rejected")
			}
			e.meterRate = 0
			var left, right [128]float32
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				e.Render(left[:], right[:])
			}
		})
	}
}
