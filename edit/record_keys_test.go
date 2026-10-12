package edit_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/keyboard"
	"m31labs.dev/cicada/project"
)

func recordedKeysSource(kind string) []byte {
	return []byte(fmt.Sprintf("cicada 2\ntrack part %s { voices=2 } // keep controls\npattern take notes steps=4 { . . . . }\nscene main { part=take }\nsong { main }\n", kind))
}

func TestRecordedKeysAllPatchesCompileSingleNoteTakes(t *testing.T) {
	for _, kind := range keyboard.Names {
		t.Run(kind, func(t *testing.T) {
			source := recordedKeysSource(kind)
			updated, err := recordTakeSource(t, source, "part", "take", []edits.TakeNote{
				{Tick: 0, EndTick: 60, Note: 21, Velocity: 40},
				{Tick: seq.TicksPerStep, EndTick: seq.TicksPerStep + 60, Note: 60, Velocity: 90},
				{Tick: 2 * seq.TicksPerStep, EndTick: 2*seq.TicksPerStep + 60, Note: 108, Velocity: 120},
			})
			if err != nil {
				t.Fatal(err)
			}
			p := patternProject(t, updated)
			for step, note := range []uint8{21, 60, 108} {
				value := p.Patterns[0].Data[step]
				if value == nil || value.Note != note || value.Slide || value.Tie || len(value.Notes) != 0 {
					t.Fatalf("step %d lost keyboard note %d: %+v", step, note, value)
				}
			}
			cfg, err := project.CompileEngine(p, 48000, 128)
			if err != nil || cfg.Track[0].Kind != engine.VoiceKeys || cfg.Track[0].Keys == nil || cfg.Track[0].Keys.Patch != keyboard.ID(kind) || cfg.Track[0].Keys.Controls[127] != 2 {
				t.Fatalf("recorded keyboard source did not compile with its patch and controls: %v", err)
			}
			if !bytes.Contains(updated, []byte("voices=2 } // keep controls")) {
				t.Fatalf("recording changed keyboard controls: %s", updated)
			}
		})
	}
}

func TestRecordedKeysTransposeRange(t *testing.T) {
	source := bytes.Replace(recordedKeysSource("fm_ep"), []byte("notes steps=4"), []byte("notes steps=4 transpose=12"), 1)
	if _, err := recordTakeSource(t, source, "part", "take", []edits.TakeNote{{Tick: 0, EndTick: 60, Note: 108, Velocity: 90}}); err == nil || !strings.Contains(err.Error(), "after pattern transposition") {
		t.Fatalf("accepted transposed keyboard note outside modeled range: %v", err)
	}
}

func TestRecordedKeysKeepChordTakePolicy(t *testing.T) {
	for _, kind := range []string{"tine_ep", "organ_full", "bell_keys"} {
		t.Run(kind, func(t *testing.T) {
			source := recordedKeysSource(kind)
			if _, err := recordTakeSource(t, source, "part", "take", []edits.TakeNote{{Tick: 0, EndTick: 180, Note: 60, Velocity: 90}, {Tick: 0, EndTick: 180, Note: 64, Velocity: 100}}); err == nil || !strings.Contains(err.Error(), "one pitch per step") {
				t.Fatalf("chord take was collapsed to one pitch: %v", err)
			}
			chord := bytes.Replace(source, []byte(". . . ."), []byte("[c4 e4 g4] . . ."), 1)
			if _, err := project.CompileEngine(patternProject(t, chord), 48000, 128); err != nil {
				t.Fatalf("authored keyboard chord did not compile: %v", err)
			}
			updated, err := recordTakeSource(t, chord, "part", "take", []edits.TakeNote{{Tick: 0, EndTick: 60, Note: 62, Velocity: 90}})
			if err == nil || updated != nil {
				t.Fatalf("recording edited an existing authored chord: %s %v", updated, err)
			}
		})
	}
}

func TestRecordedKeysDeclaredSourcesKeepOwnership(t *testing.T) {
	for _, mode := range []string{"mono", "poly"} {
		t.Run("graph/"+mode, func(t *testing.T) {
			source := []byte(fmt.Sprintf("cicada 2\ninstrument fm_ep { voice %s { out=sine(pitch) * env(gate, 20ms) } }\ntrack part fm_ep {}\npattern take notes steps=4 { . . . . }\nscene main { part=take }\nsong { main }\n", mode))
			updated, err := recordTakeSource(t, source, "part", "take", []edits.TakeNote{{Tick: 0, EndTick: 120, Note: 12, Velocity: 90, Expressions: []edits.TakeExpression{{Tick: 0, PitchCents: 20, Pressure: .5, Timbre: .5}}}})
			if err != nil {
				t.Fatal(err)
			}
			p := patternProject(t, updated)
			cfg, err := project.CompileEngine(p, 48000, 128)
			wantKind := engine.VoiceGraph
			if mode == "poly" {
				wantKind = engine.VoiceGraphPoly
			}
			if err != nil || p.NeedsKeysEngine() || cfg.Track[0].Kind != wantKind || p.Patterns[0].Data[0].Note != 12 || p.Patterns[0].Expression[0].PitchCents != 20 {
				t.Fatalf("authored graph override was treated as modeled keys: %v", err)
			}
		})
	}
	t.Run("sampler", func(t *testing.T) {
		source := []byte("cicada 2\nasset wave \"recorded.wav\" { sha256=\"" + strings.Repeat("a", 64) + "\" format=wav frames=480 rate=48000Hz channels=1 }\nsampler organ_jazz { asset=wave root=c3 mode=oneshot voices=2 }\ntrack part organ_jazz {}\npattern take notes steps=4 { . . . . }\nscene main { part=take }\nsong { main }\n")
		if _, err := recordTakeSource(t, source, "part", "take", []edits.TakeNote{{Tick: 0, EndTick: 60, Note: 12, Velocity: 90}}); err == nil || !strings.Contains(err.Error(), "not a pitched instrument track") {
			t.Fatalf("sampler override entered modeled recording: %v", err)
		}
	})
	t.Run("kit", func(t *testing.T) {
		source := []byte("cicada 2\nkit tine_ep { bd=model.kick; }\ntrack part tine_ep {}\npattern take drums steps=4 { bd:....; }\nscene main { part=take }\nsong { main }\n")
		updated, err := recordTakeSource(t, source, "part", "take", []edits.TakeNote{{Tick: 120, EndTick: 120, Note: 36, Velocity: 80}})
		if err != nil {
			t.Fatal(err)
		}
		p := patternProject(t, updated)
		if p.NeedsKeysEngine() || p.Patterns[0].Lanes["bd"][1] == nil {
			t.Fatal("kit override lost drum recording ownership")
		}
	})
}
