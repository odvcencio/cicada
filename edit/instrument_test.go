package edit_test

import (
	"bytes"
	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
	"strings"
	"testing"
)

const presetEditScore = "cicada 1\ntitle \"Keep my title\"\n// authored track\ntrack bass acid { cutoff = 723.25Hz } // preserve this\npattern p acid { 1 . 5 . }\n// scene annotation\nscene main { bass=p }\nsong { main*2 }\n"

func addPresetViaIntent(t *testing.T, source []byte, preset, instrument, track string) ([]byte, error) {
	t.Helper()
	result, err := applyM5(t, source, &edits.AddPreset{Preset: preset, Instrument: instrument, Track: track})
	if err != nil {
		return nil, err
	}
	return result.Source, nil
}

func TestAddPresetPreservesSourceAndLineEndings(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		before := []byte(strings.ReplaceAll(presetEditScore, "\n", newline))
		updated, err := addPresetViaIntent(t, before, "warm-pad", "my-pad", "keys")
		if err != nil {
			t.Fatal(err)
		}
		score, ds := notation.Parse(updated)
		if hasRecordErrors(ds) || score == nil || len(score.Tracks) != 2 || len(score.Instruments) != 1 || score.Tracks[1].Kind != "my-pad" {
			t.Fatalf("added preset: %+v\n%s", ds, updated)
		}
		if !bytes.Contains(updated, []byte("track bass acid { cutoff = 723.25Hz } // preserve this"+newline)) || !bytes.Contains(updated, []byte("// scene annotation"+newline)) || !bytes.Contains(updated, []byte("song { main*2 }"+newline)) {
			t.Fatal("preset insertion changed authored source")
		}
		if newline == "\r\n" && bytes.Contains(bytes.ReplaceAll(updated, []byte(newline), nil), []byte("\n")) {
			t.Fatal("new declaration changed CRLF conventions")
		}
		for _, request := range [][3]string{{"ghost", "voice-b", "keys-b"}, {"warm-pad", "my-pad", "keys-b"}, {"warm-pad", "voice-b", "keys"}, {"warm-pad", "acid", "keys-b"}, {"warm-pad", "voice-b", "bad\ntrack x"}} {
			if _, err := addPresetViaIntent(t, updated, request[0], request[1], request[2]); err == nil {
				t.Fatalf("accepted invalid preset creation %+v", request)
			}
		}
	}
}
