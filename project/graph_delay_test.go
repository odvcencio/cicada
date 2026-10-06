package project

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
)

func TestGraphDelayChainedDivisionRoundtrip(t *testing.T) {
	for _, expression := range []string{"1 / pitch / 2", "1 / 2 / pitch"} {
		t.Run(expression, func(t *testing.T) {
			source := fmt.Sprintf("instrument sound { voice mono { out = comb(noise(), %s, 0.8, 0.3) } } track t sound {} pattern p notes { a3 } scene s { t=p } song { s }", expression)
			score, ds := notation.Parse([]byte(source))
			if score == nil || hasErrors(ds) {
				t.Fatalf("parse: %+v", ds)
			}
			programs, ds := Check(score)
			if hasErrors(ds) {
				t.Fatalf("check: %+v", ds)
			}
			want, err := instrument.Lower(programs["sound"], nil)
			if err != nil {
				t.Fatal(err)
			}
			p, ds := FromScore(score)
			if p == nil {
				t.Fatalf("conversion: %+v", ds)
			}
			time := p.Instruments[0].Out.Args[1]
			outer, inner := "/", "period"
			if expression == "1 / 2 / pitch" {
				outer, inner = inner, outer
			}
			if time.Op != outer || time.Args[0].Op != inner {
				t.Fatalf("division operators: %+v", time)
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
			roundtrip, ds := notation.Parse(text)
			p, ds = FromScore(roundtrip)
			if p == nil {
				t.Fatalf("source roundtrip: %+v", ds)
			}
			cfg, err := CompileEngine(p, 48_000, 128)
			if err != nil || cfg.Track[0].Graph != want {
				t.Fatalf("roundtrip changed the compiled graph: %v", err)
			}
		})
	}
}

func TestGraphDelayKitLaneValidation(t *testing.T) {
	for _, primitive := range []string{"delay(noise(), %g / pitch)", "comb(noise(), %g / pitch, 0.8, 0.3)"} {
		for _, test := range []struct {
			lane    string
			periods float64
			rate    int
			invalid bool
		}{
			{"bd", 8, 48_000, true},
			{"bd", 4, 48_000, false},
			{"bd", 4, 96_000, true},
			{"cy", 8, 48_000, false},
		} {
			t.Run(fmt.Sprintf("%s/%s/%g/%d", primitive, test.lane, test.periods, test.rate), func(t *testing.T) {
				source := func(periods float64) []byte {
					return []byte(fmt.Sprintf("instrument sound { voice mono { out = %s } } kit kit { %s = sound } track t kit {} pattern p drums steps=1 { %s: x } scene s { t=p } song { s }", fmt.Sprintf(primitive, periods), test.lane, test.lane))
				}
				score, ds := notation.Parse(source(test.periods))
				if score == nil || hasErrors(ds) {
					t.Fatalf("parse: %+v", ds)
				}
				_, ds = Check(score)
				invalidAtCheckRate := test.lane == "bd" && test.periods == 8
				if hasErrors(ds) != invalidAtCheckRate {
					t.Fatalf("source check: %+v", ds)
				}
				if invalidAtCheckRate && (ds[0].Code != "CICADA-PARAM" || !strings.Contains(ds[0].Message, "lane "+test.lane)) {
					t.Fatalf("missing lane diagnostic: %+v", ds)
				}
				// Start with a valid score, then exercise untrusted semantic input.
				score, _ = notation.Parse(source(1))
				p, ds := FromScore(score)
				if p == nil {
					t.Fatalf("valid kit: %+v", ds)
				}
				p.Instruments[0].Out.Args[1].Args[0].Literal = &test.periods
				if err := ValidateProject(p); (err != nil) != invalidAtCheckRate {
					t.Fatalf("semantic validation: %v", err)
				}
				data, err := json.Marshal(p)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := DecodeJSON(data); (err != nil) != invalidAtCheckRate {
					t.Fatalf("JSON validation: %v", err)
				}
				cfg, err := CompileEngine(p, test.rate, 128)
				if (err != nil) != test.invalid {
					t.Fatalf("engine compilation: %v", err)
				}
				if test.invalid {
					if !strings.Contains(err.Error(), "delay time is out of range") || !strings.Contains(err.Error(), "lane "+test.lane) {
						t.Fatalf("missing delay/lane error: %v", err)
					}
				} else if _, err := engine.New(cfg); err != nil {
					t.Fatalf("valid kit engine: %v", err)
				}
			})
		}
	}
}

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
