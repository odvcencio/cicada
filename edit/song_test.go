package edit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const songScore = "title \"Song lane\"\ntrack bass acid {}\npattern pulse { 1 . 5 . }\nscene main { bass=pulse }\nscene break { bass=pulse }\nsong { main*2 break*2 }\n"

func applyM4Intent(source []byte, raw string, opts Options) (*Result, error) {
	var env Envelope
	if err := json.Unmarshal([]byte(`{"version":1,"intents":[`+raw+`]}`), &env); err != nil {
		return nil, err
	}
	if opts.Compiler == nil {
		opts.Compiler = parseCompiler{}
	}
	return Apply(source, env, opts)
}

func TestSongIntents(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		for _, c := range []struct{ raw, block, label, errorText string }{
			{`{"kind":"movesongentry","entity":"song:0","target":1}`, "song { break*2 main*2 }", "Song block 1 moved to 2", ""},
			{`{"kind":"setsongbars","entity":"song:0","bars":4}`, "song { main*4 break*2 }", "Song block 1 set to 4 bars", ""},
			{`{"kind":"appendsongentry","scene":"break","bars":2}`, "song { main*2 break*2 break*2 }", "Arrangement · break added for 2 bars", ""},
			{`{"kind":"duplicatesongentry","entity":"song:1"}`, "song { main*2 break*2 break*2 }", "Arrangement · block 2 · duplicate", ""},
			{`{"kind":"deletesongentry","entity":"song:0"}`, "song {  break*2 }", "Arrangement · block 1 · delete", ""},
			{`{"kind":"setsongscene","entity":"song:0","scene":"break"}`, "song { break*2 break*2 }", "Arrangement · block 1 · scene", ""},
			{`{"kind":"setsongbars","entity":"song:5","bars":4}`, "", "", "song entry is out of range"},
			{`{"kind":"movesongentry","entity":"song:0","target":5}`, "", "", "song destination is out of range"},
			{`{"kind":"setsongbars","entity":"song:0","bars":0}`, "", "", "song entry must last 1–999 bars"},
			{`{"kind":"appendsongentry","scene":"missing","bars":2}`, "", "", "choose an existing scene"},
		} {
			t.Run(c.raw+newline, func(t *testing.T) {
				source := []byte(strings.ReplaceAll(songScore, "\n", newline))
				got, err := applyM4Intent(source, c.raw, Options{})
				if c.errorText != "" {
					if err == nil || err.Error() != c.errorText {
						t.Fatalf("error %v, want %q", err, c.errorText)
					}
					return
				}
				want := bytes.Replace(source, []byte("song { main*2 break*2 }"), []byte(c.block), 1)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got.Source, want) || got.Label != c.label {
					t.Fatalf("got %q / %q, want %q / %q", got.Source, got.Label, want, c.label)
				}
			})
		}
	}
}

func TestSongStructuralEditsRefuseCommentGaps(t *testing.T) {
	source := []byte(strings.Replace(songScore, "main*2 break*2", "main*2 // hook\n break*2", 1))
	for _, raw := range []string{
		`{"kind":"movesongentry","entity":"song:0","target":1}`,
		`{"kind":"appendsongentry","scene":"break","bars":2}`,
		`{"kind":"duplicatesongentry","entity":"song:0"}`,
		`{"kind":"deletesongentry","entity":"song:0"}`,
	} {
		_, err := applyM4Intent(source, raw, Options{})
		if err == nil || err.Error() != "song comments need a source edit to preserve their attachment" {
			t.Fatalf("%s: %v", raw, err)
		}
	}
}
