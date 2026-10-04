package project

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestGraphDelayPeriodRoundtrip(t *testing.T) {
	for _, expression := range []string{"1 / pitch", "1 / 440Hz", "2 / (pitch * 2)", "10ms / 2"} {
		source := fmt.Sprintf("instrument sound { voice mono { let time = %s out = comb(noise(), time, 0.99, 0.5) } } track t sound {} pattern p notes { a3 } scene s { t=p } song { s }", expression)
		score, ds := notation.Parse([]byte(source))
		if score == nil || len(ds) != 0 {
			t.Fatalf("parse: %+v", ds)
		}
		p, ds := FromScore(score)
		if p == nil {
			t.Fatalf("project %s: %+v", expression, ds)
		}
		data, err := CanonicalJSON(p)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeJSON(data)
		if err != nil {
			t.Fatal(err)
		}
		text, err := ToSource(decoded)
		if err != nil {
			t.Fatal(err)
		}
		score, ds = notation.Parse(text)
		if score == nil || len(ds) != 0 {
			t.Fatalf("parse roundtrip: %+v", ds)
		}
		p, ds = FromScore(score)
		if p == nil {
			t.Fatalf("roundtrip: %+v\n%s", ds, text)
		}
		again, err := CanonicalJSON(p)
		if err != nil || !bytes.Equal(data, again) {
			t.Fatalf("period changed in roundtrip %s: %v\n%s", expression, err, text)
		}
	}
}

func TestGraphDelaySemanticJSONRejectsOutOfRangeNotes(t *testing.T) {
	for _, primitive := range []string{"delay(noise(), 1 / pitch)", "comb(noise(), 1 / pitch, 0.8, 0.3)"} {
		for _, transpose := range []int8{0, -12} {
			source := fmt.Sprintf("cicada 2\ninstrument sound { voice mono { out = %s } } track t sound {} pattern p notes { a3 } scene s { t=p } song { s }", primitive)
			score, ds := notation.Parse([]byte(source))
			p, ds := FromScore(score)
			if p == nil {
				t.Fatal(ds)
			}
			p.Format, p.Version = FormatID2, 2
			p.Tracks[0].Mixer.wireV2 = true
			p.Patterns[0].Data[0].Note = uint8(-transpose)
			p.Patterns[0].Transpose = transpose
			// Marshal the untrusted /2 record directly so canonical validation
			// cannot hide a decoder bypass.
			data, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeJSON(data); err == nil || !strings.Contains(err.Error(), "delay time is out of range") {
				t.Fatalf("JSON accepted MIDI note 0 for %s (transpose %d): %v", primitive, transpose, err)
			}
			if _, err := CompileEngine(p, 48_000, 128); err == nil {
				t.Fatal("engine compilation accepted out-of-range sequenced delay")
			}
			p.Patterns[0].Data[0].Note = uint8(69 - transpose)
			if _, err := CanonicalJSON(p); err != nil {
				t.Fatalf("valid delay note rejected: %v", err)
			}
			p.Patterns[0].Data[0].Note = uint8(12 - transpose)
			if _, err := CompileEngine(p, 48_000, 128); err != nil {
				t.Fatalf("valid 48 kHz delay rejected: %v", err)
			}
			if _, err := CompileEngine(p, 96_000, 128); err == nil || !strings.Contains(err.Error(), "delay time is out of range") {
				t.Fatalf("96 kHz compilation accepted an oversized delay: %v", err)
			}
		}
	}
}
