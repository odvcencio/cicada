package notation

import (
	"fmt"
	"strings"
	"testing"
)

const liveSource = `cicada 2
track bass acid {}
track drums drums {}
track lead acid {}
live {
  land = bar
  phrase = 8bars
  macro intensity = 0.3 smooth 400ms
  layers intensity {
    drums >= 0.25
    bass >= 0.45
    lead >= 0.70
    attack 1bar
    release 3bars
  }
}
pattern p { 1 . }
pattern beat drums { bd: x... }
scene main { bass = p drums = beat lead = p }
song { main*8 }
`

func TestLiveBlockParses(t *testing.T) {
	s, ds := Parse([]byte(liveSource))
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %+v", ds)
	}
	l := s.Live
	if l == nil || l.Land != "bar" || l.PhraseBars != 8 || len(l.Macros) != 1 || len(l.Layers) != 1 {
		t.Fatalf("live: %+v", l)
	}
	m := l.Macros[0]
	if m.Name != "intensity" || m.Value != 0.3 || m.SmoothMS != 400 {
		t.Fatalf("macro: %+v", m)
	}
	layers := l.Layers[0]
	if layers.Macro != "intensity" || layers.AttackBars != 1 || layers.ReleaseBars != 3 || len(layers.Rules) != 3 {
		t.Fatalf("layers: %+v", layers)
	}
	for i, want := range []float64{0.25, 0.45, 0.70} {
		if layers.Rules[i].Value != want {
			t.Fatalf("rule %d: %+v", i, layers.Rules[i])
		}
	}
}

func TestLiveDefaultsAndBounds(t *testing.T) {
	for _, land := range []string{"now", "beat", "bar", "2bars", "4bars", "phrase"} {
		t.Run(land, func(t *testing.T) {
			source := strings.Replace(liveSource, "land = bar", "land = "+land, 1)
			source = strings.Replace(source, "phrase = 8bars", "phrase = 64bars", 1)
			source = strings.Replace(source, "macro intensity = 0.3 smooth 400ms", "macro intensity = 1 smooth 0.4s", 1)
			source = strings.Replace(source, "attack 1bar\n    release 3bars", "", 1)
			score, ds := Parse([]byte(source))
			if len(ds) != 0 {
				t.Fatalf("diagnostics: %+v", ds)
			}
			if score.Live.Land != land || score.Live.PhraseBars != 64 || score.Live.Macros[0].SmoothMS != 400 || score.Live.Layers[0].AttackBars != 1 || score.Live.Layers[0].ReleaseBars != 3 {
				t.Fatalf("defaults: %+v", score.Live)
			}
		})
	}
	source := strings.Replace(liveSource, "macro intensity = 0.3 smooth 400ms", "macro intensity = 0", 1)
	score, ds := Parse([]byte(source))
	if len(ds) != 0 || score.Live.Macros[0].SmoothMS != 0 {
		t.Fatalf("default smoothing: %+v", ds)
	}
}

func TestLiveDiagnostics(t *testing.T) {
	tests := []struct {
		name, body, code string
		line, col        int
	}{
		{"macro range", "macro intensity = 1.1", "CICADA-LIVE-MACRO", 3, 21},
		{"macro units", "macro intensity = 30%", "CICADA-LIVE-MACRO", 3, 21},
		{"smooth units", "macro intensity = 0.3 smooth 4Hz", "CICADA-LIVE-MACRO", 3, 32},
		{"smooth negative", "macro intensity = 0.3 smooth -1s", "CICADA-LIVE-MACRO", 3, 32},
		{"unknown macro", "layers missing {}", "CICADA-LIVE-MACRO", 3, 10},
		{"unknown track", "macro intensity = 0.3\n  layers intensity { missing >= 0.5 }", "CICADA-LIVE-TRACK", 4, 22},
		{"threshold range", "macro intensity = 0.3\n  layers intensity { bass >= -0.1 }", "CICADA-LIVE-MACRO", 4, 30},
		{"land", "land = pattern", "CICADA-LIVE-LAND", 3, 10},
		{"phrase zero", "phrase = 0bars", "CICADA-LIVE-PHRASE", 3, 12},
		{"phrase high", "phrase = 65bars", "CICADA-LIVE-PHRASE", 3, 12},
		{"phrase fractional", "phrase = 1.5bars", "CICADA-LIVE-PHRASE", 3, 12},
		{"attack", "macro intensity = 0.3\n  layers intensity { attack 2bars }", "CICADA-LIVE-ATTACK", 4, 29},
		{"release", "macro intensity = 0.3\n  layers intensity { release 17bars }", "CICADA-LIVE-RELEASE", 4, 30},
		{"duplicate macro", "macro intensity = 0.3\n  macro intensity = 0.4", "CICADA-LIVE-MACRO", 4, 3},
		{"duplicate setting", "land = bar\n  land = beat", "CICADA-LIVE-BLOCK", 4, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := "cicada 2\ntrack bass acid {}\nlive {\n  " + tt.body + "\n}\npattern p { 1 }\nscene main { bass = p }\nsong { main }"
			_, ds := Parse([]byte(source))
			for _, d := range ds {
				if d.Code == tt.code && d.Position == (Position{Line: tt.line + 1, Column: tt.col}) {
					return
				}
			}
			t.Fatalf("want %s at %d:%d, got %+v", tt.code, tt.line+1, tt.col, ds)
		})
	}
	t.Run("macro limit", func(t *testing.T) {
		var body strings.Builder
		for i := 0; i < 17; i++ {
			fmt.Fprintf(&body, "macro m%d = 0\n", i)
		}
		_, ds := Parse([]byte("cicada 2\ntrack bass acid {}\nlive {\n" + body.String() + "}\npattern p { 1 }\nscene main { bass = p }\nsong { main }"))
		for _, d := range ds {
			if d.Code == "CICADA-LIVE-LIMIT" && d.Position == (Position{Line: 20, Column: 1}) {
				return
			}
		}
		t.Fatalf("macro limit: %+v", ds)
	})
	t.Run("threshold limit", func(t *testing.T) {
		_, ds := Parse([]byte("cicada 2\ntrack a acid {} track b acid {} track c acid {} track d acid {}\nlive { macro intensity = 0 layers intensity {\na >= 0.1\nb >= 0.2\nc >= 0.3\nd >= 0.4\n} }\npattern p { 1 } scene main { a = p } song { main }"))
		for _, d := range ds {
			if d.Code == "CICADA-LIVE-LIMIT" && d.Position == (Position{Line: 7, Column: 6}) {
				return
			}
		}
		t.Fatalf("threshold limit: %+v", ds)
	})
	t.Run("duplicate block", func(t *testing.T) {
		_, ds := Parse([]byte(strings.Replace(liveSource, "pattern p", "live {}\npattern p", 1)))
		for _, d := range ds {
			if d.Code == "CICADA-LIVE-BLOCK" && d.Position == (Position{Line: 17, Column: 1}) {
				return
			}
		}
		t.Fatalf("duplicate block: %+v", ds)
	})
	t.Run("block before track", func(t *testing.T) {
		_, ds := Parse([]byte(strings.Replace(liveSource, "track bass", "live {}\ntrack bass", 1)))
		for _, d := range ds {
			if d.Code == "CICADA-LIVE-BLOCK" && d.Position == (Position{Line: 3, Column: 1}) {
				return
			}
		}
		t.Fatalf("placement: %+v", ds)
	})
}

func TestLiveBlockRoundTripsThroughFmt(t *testing.T) {
	source := strings.Replace(liveSource, "land = bar", "// landing\n  land=bar // keep landing", 1)
	source = strings.Replace(source, "macro intensity = 0.3 smooth 400ms", "macro intensity=0.3 smooth 0.4s // macro comment", 1)
	source = strings.Replace(source, "drums >= 0.25", "// rule comment\n    drums>=0.25", 1)
	source = strings.Replace(source, "attack 1bar\n    release 3bars", "attack 1bar release 3bars // timing", 1)
	document, err := ParseDocument([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	first, err := Format(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, comment := range []string{"// landing", "// keep landing", "// macro comment", "// rule comment", "// timing"} {
		if !strings.Contains(string(first), comment) {
			t.Fatalf("lost %s", comment)
		}
	}
	reparsed, ds := Parse(first)
	if len(ds) != 0 {
		t.Fatalf("formatted source: %s\n%+v", first, ds)
	}
	if reparsed.Live.Macros[0].SmoothMS != 400 {
		t.Fatal("smoothing changed")
	}
	document, err = ParseDocument(first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Format(document)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("non-idempotent:\n%s\n%s", first, second)
	}
	if !strings.Contains(string(first), "    drums >= 0.25\n") || !strings.Contains(string(first), "    attack 1bar\n    release 3bars\n") {
		t.Fatalf("statement layout: %s", first)
	}
}
