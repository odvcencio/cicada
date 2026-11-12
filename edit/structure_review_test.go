package edit

import (
	"strings"
	"testing"
)

// TestStopPatternIsAPatternUse: a scene binding "stop" plays the pattern
// named stop when the score declares one, so rename and delete treat it as a
// use (docs/spec/edition-1.md).
func TestStopPatternIsAPatternUse(t *testing.T) {
	source := "cicada 2\ntempo 120\ntrack bass acid {}\npattern riff { a1 . c2 . }\npattern stop { c2 c2 c2 c2 }\nscene main { bass = riff }\nscene other { bass = stop }\nsong { main other }\n"
	structureCase(t, source, &Rename{Entity: "pattern:stop", Name: "hold"}, strings.NewReplacer("pattern stop {", "pattern hold {", "bass = stop", "bass = hold").Replace(source))
	structureRefusal(t, source, &DeletePattern{Pattern: "stop"}, "pattern_in_use", "pattern", "scene other binding bass = stop (line 7)", "scene other binding bass = stop (line 7)")
	structureCase(t, source, &DeletePattern{Pattern: "stop", Cascade: true}, strings.NewReplacer("pattern stop { c2 c2 c2 c2 }\n", "", "bass = stop", "bass = off").Replace(source))
}

// TestKeywordNamesAreRenamedAndDeleted uses, as entity names, the words that
// are also controls or keywords somewhere: a scene binding's off, keep and
// stop, a scene's tempo setting, an insert chain's none, the voices acid and
// drums, an effect kind, a MIDI action. Each rename touches the entity's own
// uses only, and the controls keep their meaning.
func TestKeywordNamesAreRenamedAndDeleted(t *testing.T) {
	base := "cicada 2\ntempo 120\ntrack bass acid {}\npattern riff { a1 . c2 . }\n"
	for _, tc := range []struct {
		name, source string
		intent       Intent
		want         string
	}{
		{"track tempo", "cicada 2\ntempo 120\ntrack tempo acid {}\npattern riff { a1 . c2 . }\nscene main { tempo = riff }\nscene fast { tempo = 140 }\nsong { main fast }\n",
			&Rename{Entity: "track:tempo", Name: "lead"}, "cicada 2\ntempo 120\ntrack lead acid {}\npattern riff { a1 . c2 . }\nscene main { lead = riff }\nscene fast { tempo = 140 }\nsong { main fast }\n"},
		{"track stop", base + "track stop acid {}\nscene main { bass = riff stop = riff }\nscene quiet { stop = off }\nsong { main quiet }\n",
			&Rename{Entity: "track:stop", Name: "lead"}, base + "track lead acid {}\nscene main { bass = riff lead = riff }\nscene quiet { lead = off }\nsong { main quiet }\n"},
		{"track acid", base + "track acid acid {}\nscene main { bass = riff acid = riff }\nsong { main }\n",
			&Rename{Entity: "track:acid", Name: "lead"}, base + "track lead acid {}\nscene main { bass = riff lead = riff }\nsong { main }\n"},
		{"track none", base + "fx none drive {}\ntrack keys piano { insert = none }\nscene main { bass = riff }\nsong { main }\n",
			&DeleteTrack{Track: "keys"}, base + "fx none drive {}\nscene main { bass = riff }\nsong { main }\n"},
		{"pattern off is never bound", base + "pattern off { c2 . . . }\nscene main { bass = riff }\nscene quiet { bass = off }\nsong { main quiet }\n",
			&Rename{Entity: "pattern:off", Name: "fill"}, base + "pattern fill { c2 . . . }\nscene main { bass = riff }\nscene quiet { bass = off }\nsong { main quiet }\n"},
		{"pattern keep is never bound", base + "pattern keep { c2 . . . }\nscene main { bass = riff }\nscene same { bass = keep }\nsong { main same }\n",
			&DeletePattern{Pattern: "keep"}, base + "scene main { bass = riff }\nscene same { bass = keep }\nsong { main same }\n"},
		{"pattern none", base + "pattern none { c2 . . . }\nscene main { bass = none }\nsong { main }\n",
			&Rename{Entity: "pattern:none", Name: "fill"}, base + "pattern fill { c2 . . . }\nscene main { bass = fill }\nsong { main }\n"},
		{"pattern drums", base + "pattern drums { c2 . . . }\nscene main { bass = drums }\nsong { main }\n",
			&Rename{Entity: "pattern:drums", Name: "fill"}, base + "pattern fill { c2 . . . }\nscene main { bass = fill }\nsong { main }\n"},
		{"scene launch", base + "scene launch { bass = riff }\nsong { launch }\nmidi {\n  port pads\n  pads note c1 -> launch launch\n}\n",
			&Rename{Entity: "scene:launch", Name: "verse"}, base + "scene verse { bass = riff }\nsong { verse }\nmidi {\n  port pads\n  pads note c1 -> launch verse\n}\n"},
		{"scene off", base + "scene off { bass = riff }\nscene main { bass = off }\nsong { off main }\n",
			&Rename{Entity: "scene:off", Name: "intro"}, base + "scene intro { bass = riff }\nscene main { bass = off }\nsong { intro main }\n"},
		{"scene stop", base + "scene stop { bass = riff }\nscene main { bass = riff }\nsong { main stop }\n",
			&DeleteScene{Scene: "stop", Cascade: true}, base + "scene main { bass = riff }\nsong { main }\n"},
		{"scene drive", base + "scene drive { bass = riff }\nsong { drive }\n",
			&Rename{Entity: "scene:drive", Name: "verse"}, base + "scene verse { bass = riff }\nsong { verse }\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { structureCase(t, tc.source, tc.intent, tc.want) })
	}
	master := "cicada 2\ntempo 120\ntrack master acid {}\npattern riff { a1 . c2 . }\nscene main { master = riff }\nsong { main }\nautomate master.cutoff {\n  @1.1.1 700Hz\n}\n"
	structureRefusal(t, master, &Rename{Entity: "track:master", Name: "lead"}, "name_ambiguous", "entity", "may mean either", "automation master.cutoff (line 7)")
}

// TestDeletesLeaveNoStrayText removes declarations that share a line, carry
// a trailing comment, sit under a comment, or hold placements with comments.
// The rule: a comment after a declaration on its line goes with it, and so
// do the comment lines directly above it, unless they open the file or the
// declaration is directly followed by another of its kind (the comment heads
// the group); a comment set apart by a blank line stays.
func TestDeletesLeaveNoStrayText(t *testing.T) {
	shared := "cicada 2\ntempo 120\ntrack a acid {} track b acid {}\ntrack c acid {} // the c track\n\n// patterns\npattern riff { a1 . c2 . } pattern fill { a1 a1 a1 a1 } // fills\n// end of patterns\n\nscene main { a = riff b = riff c = riff }\nsong { main }\n"
	structureCase(t, shared, &DeleteTrack{Track: "a"}, strings.NewReplacer("track a acid {} track b acid {}", "track b acid {}", "{ a = riff b = riff", "{ b = riff").Replace(shared))
	structureCase(t, shared, &DeleteTrack{Track: "c"}, strings.NewReplacer("track c acid {} // the c track\n", "", " c = riff }", " }").Replace(shared))
	structureCase(t, shared, &DeletePattern{Pattern: "fill"}, strings.Replace(shared, " pattern fill { a1 a1 a1 a1 } // fills", "", 1))
	structureCase(t, shared, &DeletePattern{Pattern: "riff", Cascade: true}, strings.NewReplacer("pattern riff { a1 . c2 . } pattern fill", "pattern fill", "a = riff b = riff c = riff", "a = off b = off c = off").Replace(shared))
	section := "cicada 2\ntempo 120\n\n// ---- tracks ----\ntrack bass acid {\n  cutoff = 620Hz\n}\ntrack keys piano {}  // keys\n\npattern riff { a1 . c2 . }\nscene main { keys = riff }\nsong { main }\n"
	// The heading above the first of two tracks stays with the track left.
	structureCase(t, section, &DeleteTrack{Track: "bass"}, strings.Replace(section, "track bass acid {\n  cutoff = 620Hz\n}\n", "", 1))
	// A comment that opens the file stays; one directly above a lone
	// declaration goes with it.
	leading := "// rhythm lives here\ntrack bass acid {}\n\n// the keys\ntrack keys piano {}\n\npattern riff { a1 . c2 . }\nscene main { bass = riff }\nsong { main }\n"
	structureCase(t, leading, &DeleteTrack{Track: "keys"}, strings.Replace(leading, "// the keys\ntrack keys piano {}\n\n", "", 1))
	leadingOnly := "// rhythm lives here\ntrack keys piano {}\n\ntrack bass acid {}\npattern riff { a1 . c2 . }\nscene main { bass = riff }\nsong { main }\n"
	structureCase(t, leadingOnly, &DeleteTrack{Track: "keys"}, strings.Replace(leadingOnly, "track keys piano {}\n", "", 1))
	structureCase(t, section, &DeleteTrack{Track: "keys"}, strings.NewReplacer("track keys piano {}  // keys\n", "", "scene main { keys = riff }", "scene main {}").Replace(section))
	arranged := "cicada 2\ntempo 120\ntrack bass acid {}\npattern riff { a1 . c2 . }\npattern alt { c2 . . . }\narrange {\n  // first bar\n  place p1 bass riff { at = @1.1.1 length = 1bars }\n  place p2 bass alt { at = @2.1.1 length = 1bars } // second\n  marker end { at = @3.1.1 }\n}\n"
	structureCase(t, arranged, &DeletePattern{Pattern: "alt", Cascade: true}, strings.NewReplacer("pattern alt { c2 . . . }\n", "", "  place p2 bass alt { at = @2.1.1 length = 1bars } // second\n", "").Replace(arranged))
	structureCase(t, arranged, &Rename{Entity: "pattern:riff", Name: "groove"}, strings.NewReplacer("pattern riff {", "pattern groove {", "place p1 bass riff", "place p1 bass groove").Replace(arranged))
}

// TestRenameLeavesCommentsStringsAndLongerNames is the reviewer's probe: the
// expected text is written out, so a comment that says bass, the track bass2
// and the title keep their text.
func TestRenameLeavesCommentsStringsAndLongerNames(t *testing.T) {
	before := `cicada 2
title "bass song"
tempo 120
// bass is the low line; bass2 doubles it
fx glue comp { sidechain = "bass" }
bus music { insert = glue }
track bass acid { cutoff = 620Hz chain = riff riff2 }
track bass2 acid {}
track cue acid {}
pattern riff { a1 . c2 . }
pattern riff2 { a1 . c2 . }
pattern stinger-p { a1 . . . }
scene main { bass = riff bass2 = riff2 }
scene other { bass = riff }
song { main*2 other }
midi {
  port pads
  pads note c2 -> stop-track bass
  pads note d2 -> launch-slot bass riff
  pads note e2 -> launch main
}
live {
  macro tone -> bass.cutoff 200Hz..2kHz
  stinger hit = cue.stinger-p
}
`
	structureCase(t, before, &Rename{Entity: "track:bass", Name: "sub"}, `cicada 2
title "bass song"
tempo 120
// bass is the low line; bass2 doubles it
fx glue comp { sidechain = "sub" }
bus music { insert = glue }
track sub acid { cutoff = 620Hz chain = riff riff2 }
track bass2 acid {}
track cue acid {}
pattern riff { a1 . c2 . }
pattern riff2 { a1 . c2 . }
pattern stinger-p { a1 . . . }
scene main { sub = riff bass2 = riff2 }
scene other { sub = riff }
song { main*2 other }
midi {
  port pads
  pads note c2 -> stop-track sub
  pads note d2 -> launch-slot sub riff
  pads note e2 -> launch main
}
live {
  macro tone -> sub.cutoff 200Hz..2kHz
  stinger hit = cue.stinger-p
}
`)
	structureCase(t, before, &Rename{Entity: "pattern:riff", Name: "groove"}, strings.NewReplacer(
		"chain = riff riff2", "chain = groove riff2",
		"pattern riff {", "pattern groove {",
		"bass = riff bass2", "bass = groove bass2",
		"scene other { bass = riff }", "scene other { bass = groove }",
		"launch-slot bass riff", "launch-slot bass groove").Replace(before))
}

// TestStructuralRefusalsByName gives each refusal its own reason and field.
func TestStructuralRefusalsByName(t *testing.T) {
	var many strings.Builder
	many.WriteString("cicada 2\ntempo 120\n")
	for i := 1; i <= 64; i++ {
		many.WriteString("track t" + strings.Repeat("x", 0) + itoa(i) + " acid {}\n")
	}
	many.WriteString("pattern riff { a1 . }\nscene main { t1 = riff }\nsong { main }\n")
	structureRefusal(t, many.String(), &AddTrack{Name: "extra", Voice: "piano"}, "track_limit", "name", "at most 64 tracks")
	structureRefusal(t, structureScore, &AddTrack{Name: "lead", Voice: "piano", Instrument: "warm"}, "instrument_without_preset", "instrument", "give preset with it")
	structureRefusal(t, structureScore, &AddTrack{Name: "lead", Preset: "warm-pad", Instrument: "acid"}, "invalid_name", "instrument", "acid is a built-in voice")
	one := "cicada 2\ntempo 120\ntrack bass acid {}\ntrack keys piano {}\npattern riff { a1 . c2 . }\nscene main { bass = riff }\nsong { main }\n"
	structureRefusal(t, one, &DeletePattern{Pattern: "riff", Cascade: true}, "last_pattern", "pattern", "only pattern")
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

// TestRemovalKeepsHeadingsPastTrailingComments: the third review's comment
// inputs. A heading above a declaration whose next sibling is of its kind
// stays even when the declaration carries a trailing comment, and removing
// the last declaration of a file with no final newline leaves one newline.
func TestRemovalKeepsHeadingsPastTrailingComments(t *testing.T) {
	trailing := "cicada 2\ntempo 120\n\n// Tracks\ntrack a acid {} // the lead\ntrack b acid {}\n\npattern p { a1 . }\nscene main { b = p }\nsong { main }\n"
	structureCase(t, trailing, &DeleteTrack{Track: "a"}, strings.Replace(trailing, "track a acid {} // the lead\n", "", 1))
	last := "cicada 2\ntempo 120\n\ntrack b acid {}\npattern p { a1 . }\nscene main { b = p }\nsong { main }\n\n// spare\ntrack a acid {}"
	got, err := applyEffectIntent(t, last, &DeleteTrack{Track: "a"})
	if err != nil || got != "cicada 2\ntempo 120\n\ntrack b acid {}\npattern p { a1 . }\nscene main { b = p }\nsong { main }\n" {
		t.Fatalf("%v\n%q", err, got)
	}
}
