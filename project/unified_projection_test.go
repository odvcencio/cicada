package project

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
)

func TestUnifiedChordsAudioArrangementProjection(t *testing.T) {
	_, base, _ := assetFixture(t)
	source := "cicada 2\n" + string(base)
	source = strings.ReplaceAll(source, "scene verse { chops = hits vox = vocal-a }\nsong { verse*4 }\n", "")
	source += `instrument piano { voice poly { out = sine(pitch) * env(gate, 100ms) * 0.1 } }
track keys piano {}
pattern harmony notes { [c4 e4 g4] - . [d4 f4 a4] }
arrange {
 place chord-part keys harmony { at = 0ticks length = 960ticks }
 place sample-part chops hits { at = 0ticks length = 960ticks }
 place clip-part vox vocal-a { at = 0ticks length = 960ticks }
 marker end { at = 960ticks }
}
`
	score, ds := notation.Parse([]byte(source))
	p, ds := FromScore(score)
	if p == nil || hasErrors(ds) {
		t.Fatal(ds)
	}
	canonical, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	fromJSON, err := DecodeJSON(canonical)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, err := ToSource(fromJSON)
	if err != nil {
		t.Fatal(err)
	}
	again, ds := notation.Parse(rebuilt)
	fromSource, ds := FromScore(again)
	if fromSource == nil || hasErrors(ds) {
		t.Fatalf("%v\n%s", ds, rebuilt)
	}
	restored, err := CanonicalJSON(fromSource)
	if err != nil || !bytes.Equal(canonical, restored) {
		t.Fatalf("combined schema/source projection changed: %v\n%s\n%s", err, canonical, restored)
	}
	pcm := make([]float32, 4800)
	for i := range pcm {
		pcm[i] = float32(i%31) / 1000
	}
	assets := []engine.AudioAsset{{SampleRate: 48000, Left: pcm}}
	var want []byte
	for _, current := range []*Project{p, fromJSON, fromSource} {
		cfg, err := CompileEngineWithAssets(current, 48000, 128, assets)
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Schedule) != 6 || len(cfg.Clips) != 1 || len(cfg.Assets) != 1 || cfg.Track[0].Kind != engine.VoiceSample || cfg.Track[1].Kind != engine.VoiceAudio || cfg.Track[2].Polyphony != 4 || cfg.Patterns[2].Slots[0].Chords[0].Count != 3 {
			t.Fatal("combined projection lost a feature")
		}
		data, err := kernelimage.Encode(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if binary.LittleEndian.Uint16(data[4:6]) != kernelimage.UnifiedImageVersion {
			t.Fatal("combined image not v15")
		}
		decoded, err := kernelimage.Decode(data, 48000, 128)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = engine.New(decoded); err != nil {
			t.Fatal(err)
		}
		if want == nil {
			want = data
		} else if !bytes.Equal(want, data) {
			t.Fatal("combined projection changed image bytes")
		}
	}
	p.Tracks[2].Kind = "hit"
	if err := ValidateProject(p); err == nil {
		t.Fatal("sampler silently accepted graph chord ownership")
	}
}
