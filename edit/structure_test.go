package edit

import (
	"errors"
	"strings"
	"testing"
)

const structureScore = `cicada 2
tempo 120
// the bass line
track bass acid { cutoff = 620Hz }
track keys piano {}
pattern riff { a1 . c2 . }
// pads
pattern pad { [a3 c4 e4] - - . }
pattern fill { a1 a1 a1 a1 }
scene main { bass = riff keys = pad }
scene break {
  // just the bass
  bass = riff
}
song { main*2 break }
`

// structureCase applies one intent to structureScore with LF and with CRLF
// and compares the result with want, in which every "\n" becomes the
// source's newline.
func structureCase(t *testing.T, source string, intent Intent, want string) {
	t.Helper()
	for _, newline := range []string{"\n", "\r\n"} {
		got, err := applyEffectIntent(t, strings.ReplaceAll(source, "\n", newline), intent)
		if err != nil {
			t.Fatalf("%q: %v", newline, err)
		}
		if want := strings.ReplaceAll(want, "\n", newline); got != want {
			t.Fatalf("%q:\n%s\nwant:\n%s", newline, got, want)
		}
	}
}

// structureRefusal applies one intent and requires a refusal with a reason,
// a field, message words and, when given, the exact uses.
func structureRefusal(t *testing.T, source string, intent Intent, reason, field, words string, uses ...string) {
	t.Helper()
	_, err := applyEffectIntent(t, source, intent)
	var refused *ReasonError
	var located *IntentError
	if !errors.As(err, &refused) || refused.Reason != reason || !strings.Contains(err.Error(), words) || !errors.As(err, &located) || located.Field != field {
		t.Fatalf("got %v (%+v)", err, refused)
	}
	if len(uses) > 0 && strings.Join(refused.Uses, "|") != strings.Join(uses, "|") {
		t.Fatalf("uses %q, want %q", refused.Uses, uses)
	}
}

func TestAddTrackWritesAfterTheLastTrack(t *testing.T) {
	structureCase(t, structureScore, &AddTrack{Name: "lead", Voice: "piano"}, strings.Replace(structureScore, "track keys piano {}\n", "track keys piano {}\ntrack lead piano {}\n", 1))
	structureRefusal(t, structureScore, &AddTrack{Name: "keys", Voice: "piano"}, "name_taken", "name", "keys already names")
	structureRefusal(t, structureScore, &AddTrack{Name: "lead", Voice: "nosuch"}, "voice_invalid", "voice", `a track cannot play "nosuch"`)
	structureRefusal(t, structureScore, &AddTrack{Name: "lead", Voice: "piano", Preset: "warm-pad"}, "voice_and_preset", "preset", "not both")
	structureRefusal(t, structureScore, &AddTrack{Name: "lead", Preset: "nosuch"}, "unknown_preset", "preset", `unknown preset "nosuch"`)
	got, err := applyEffectIntent(t, structureScore, &AddTrack{Name: "pad", Preset: "warm-pad"})
	if err != nil || !strings.Contains(got, "instrument pad") || !strings.Contains(got, "track pad pad {}") {
		t.Fatalf("preset: %v\n%s", err, got)
	}
}

func TestDeleteTrackRemovesItsBindings(t *testing.T) {
	structureCase(t, structureScore, &DeleteTrack{Track: "keys"}, strings.NewReplacer("track keys piano {}\n", "", "bass = riff keys = pad", "bass = riff").Replace(structureScore))
	want := strings.NewReplacer("// the bass line\ntrack bass acid { cutoff = 620Hz }\n", "", "scene main { bass = riff keys = pad }", "scene main { keys = pad }", "  // just the bass\n  bass = riff\n", "").Replace(structureScore)
	structureCase(t, structureScore, &DeleteTrack{Track: "bass"}, want)
	used := structureScore + "automate bass.cutoff {\n  @1.1.1 700Hz\n}\n"
	structureRefusal(t, used, &DeleteTrack{Track: "bass"}, "track_in_use", "track", "track bass is still used by automation bass.cutoff (line 16)", "automation bass.cutoff (line 16)")
	structureRefusal(t, structureScore, &DeleteTrack{Track: "nosuch"}, "unknown_track", "track", `unknown track "nosuch"`)
}

func TestSetTrackVoiceRefusesOrDropsSettings(t *testing.T) {
	structureCase(t, structureScore, &SetTrackVoice{Track: "keys", Voice: "acid"}, strings.Replace(structureScore, "track keys piano {}", "track keys acid {}", 1))
	structureRefusal(t, structureScore, &SetTrackVoice{Track: "bass", Voice: "piano"}, "voice_params_unsupported", "voice", "piano has no setting cutoff", "setting cutoff (line 4)")
	structureCase(t, structureScore, &SetTrackVoice{Track: "bass", Voice: "piano", DropParams: true}, strings.Replace(structureScore, "track bass acid { cutoff = 620Hz }", "track bass piano {}", 1))
	structureRefusal(t, structureScore, &SetTrackVoice{Track: "keys", Voice: "nosuch"}, "voice_invalid", "voice", `a track cannot play "nosuch"`)
	structureRefusal(t, structureScore, &SetTrackVoice{Track: "keys", Voice: "piano"}, "voice_unchanged", "voice", "track keys already plays piano")
	acid := strings.Replace(structureScore, "pattern riff {", "pattern riff acid {", 1)
	structureRefusal(t, acid, &SetTrackVoice{Track: "bass", Voice: "piano", DropParams: true}, "voice_change_invalid", "voice", "pattern kind differs from track instrument")
}

func TestAddAndDuplicateScene(t *testing.T) {
	structureCase(t, structureScore, &AddScene{Name: "drop"}, strings.Replace(structureScore, "  bass = riff\n}\n", "  bass = riff\n}\nscene drop {}\n", 1))
	structureCase(t, structureScore, &DuplicateScene{Scene: "break", Name: "break-2"}, strings.Replace(structureScore, "  bass = riff\n}\n", "  bass = riff\n}\nscene break-2 {\n  // just the bass\n  bass = riff\n}\n", 1))
	structureCase(t, structureScore, &DuplicateScene{Scene: "main", Name: "main-2"}, strings.Replace(structureScore, "scene main { bass = riff keys = pad }\n", "scene main { bass = riff keys = pad }\nscene main-2 { bass = riff keys = pad }\n", 1))
	structureRefusal(t, structureScore, &AddScene{Name: "main"}, "name_taken", "name", "scene main already exists")
}

func TestDeleteSceneRefusesWhilePlayed(t *testing.T) {
	structureRefusal(t, structureScore, &DeleteScene{Scene: "break"}, "scene_in_use", "scene", "scene break is still used by song entry break (line 15)", "song entry break (line 15)")
	structureCase(t, structureScore, &DeleteScene{Scene: "break", Cascade: true}, strings.NewReplacer("scene break {\n  // just the bass\n  bass = riff\n}\n", "", "song { main*2 break }", "song { main*2 }").Replace(structureScore))
	structureCase(t, structureScore, &DeleteScene{Scene: "main", Cascade: true}, strings.NewReplacer("scene main { bass = riff keys = pad }\n", "", "song { main*2 break }", "song { break }").Replace(structureScore))
	onlyMain := strings.Replace(structureScore, "song { main*2 break }", "song { main*2 main }", 1)
	structureRefusal(t, onlyMain, &DeleteScene{Scene: "main", Cascade: true}, "song_would_be_empty", "cascade", "every song entry plays scene main")
}

func TestDeletePatternRefusesWhileUsed(t *testing.T) {
	structureCase(t, structureScore, &DeletePattern{Pattern: "fill"}, strings.Replace(structureScore, "pattern fill { a1 a1 a1 a1 }\n", "", 1))
	structureRefusal(t, structureScore, &DeletePattern{Pattern: "pad"}, "pattern_in_use", "pattern", "pattern pad is still used by scene main binding keys = pad (line 10)", "scene main binding keys = pad (line 10)")
	structureCase(t, structureScore, &DeletePattern{Pattern: "pad", Cascade: true}, strings.NewReplacer("// pads\npattern pad { [a3 c4 e4] - - . }\n", "", "keys = pad", "keys = off").Replace(structureScore))
}

const referenceScore = `cicada 2
tempo 120
fx glue comp { sidechain = bass }
bus music { insert = glue }
track bass acid { cutoff = 620Hz }
track keys piano {}
track cue acid {}
envelope follow { source = bass attack = 10ms release = 200ms }
mod follow -> keys.level depth = 3dB
lfo sweep { rate = 1bars }
mod sweep -> bass.reso depth = 0.2
pattern riff { a1 . c2 . }
pattern pad { [a3 c4 e4] - - . }
automate bass.cutoff {
  @1.1.1 620Hz
}
scene main { bass = riff keys = pad bass.reso = 0.5 }
scene other { bass = riff }
song { main*2 other }
midi {
  port pads
  pads ch 1 -> bass
  pads cc 74 -> bass.cutoff
  pads note c1 -> launch main
}
live {
  macro intensity = 0.3
  layers intensity { bass >= 0.25 }
  state calm = main
  stinger hit = cue.riff
}
`

// TestRenameUpdatesEveryReference renames each kind of entity in a score
// that names it in every place the language allows. The names occur nowhere
// else, so the expected text replaces every occurrence.
func TestRenameUpdatesEveryReference(t *testing.T) {
	structureCase(t, referenceScore, &Rename{Entity: "track:bass", Name: "sub"}, strings.ReplaceAll(referenceScore, "bass", "sub"))
	structureCase(t, referenceScore, &Rename{Entity: "track:cue", Name: "sting"}, strings.ReplaceAll(referenceScore, "cue", "sting"))
	structureCase(t, referenceScore, &Rename{Entity: "pattern:riff", Name: "groove"}, strings.ReplaceAll(referenceScore, "riff", "groove"))
	structureCase(t, referenceScore, &Rename{Entity: "scene:main", Name: "verse"}, strings.ReplaceAll(referenceScore, "main", "verse"))
	structureRefusal(t, referenceScore, &Rename{Entity: "track:bass", Name: "keys"}, "name_taken", "name", "keys already names")
	structureRefusal(t, referenceScore, &Rename{Entity: "pattern:riff", Name: "pad"}, "name_taken", "name", "pattern pad already exists")
	structureRefusal(t, referenceScore, &Rename{Entity: "scene:main", Name: "other"}, "name_taken", "name", "scene other already exists")
	structureRefusal(t, referenceScore, &Rename{Entity: "track:nosuch", Name: "x"}, "unknown_track", "entity", `unknown track "nosuch"`)
	structureRefusal(t, referenceScore, &Rename{Entity: "fx:glue", Name: "x"}, "unknown_entity", "entity", "rename takes a track, a pattern or a scene")
}

// TestDeleteRefusalsListEveryUse checks the structured uses of a track and a
// scene that the score names in every place the language allows.
func TestDeleteRefusalsListEveryUse(t *testing.T) {
	structureRefusal(t, referenceScore, &DeleteTrack{Track: "bass"}, "track_in_use", "track", "track bass is still used by effect glue sidechain (line 3)",
		"effect glue sidechain (line 3)",
		"envelope follow source (line 8)",
		"modulation route sweep -> bass.reso (line 11)",
		"automation bass.cutoff (line 14)",
		"scene main setting bass.reso (line 17)",
		"MIDI mapping -> bass (line 22)",
		"MIDI mapping bass.cutoff (line 23)",
		"live layers intensity (line 28)")
	structureRefusal(t, referenceScore, &DeleteScene{Scene: "main", Cascade: true}, "scene_in_use", "scene", "scene main is still used by",
		"MIDI mapping pads note c1 -> launch main (line 24)",
		"live state calm (line 29)")
	structureRefusal(t, referenceScore, &DeletePattern{Pattern: "riff", Cascade: true}, "pattern_in_use", "pattern", "pattern riff is still used by live stinger hit (line 30)",
		"live stinger hit (line 30)")
	lastTrack := "cicada 2\ntempo 120\ntrack bass acid {}\npattern riff { a1 . c2 . }\nscene main { bass = riff }\nsong { main }\n"
	structureRefusal(t, lastTrack, &DeleteTrack{Track: "bass"}, "last_track", "track", "track bass is the only track")
}
