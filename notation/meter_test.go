package notation

import (
	"strings"
	"testing"
)

const waltzHeader = "cicada 2\ntempo 132\nmeter 3/4\n"

func TestMeterDeclarationSetsTheScoreMap(t *testing.T) {
	for _, c := range []struct {
		source         string
		beats, unit    int64
		groups         string
		barTicks, beat int64
	}{
		{waltzHeader + "track bass acid {}\npattern oom { step = 1/4 c2 g2 g2 }\nscene a { bass = oom }\nsong { a*16 }\n", 3, 4, "", 2880, 960},
		{"cicada 2\nmeter 6/8\ntrack lead acid {}\npattern jig { step = 1/8 c3 eb3 g3 c4 g3 eb3 }\nscene a { lead = jig }\nsong { a*8 }\n", 6, 8, "", 2880, 480},
		{"cicada 2\nmeter 7/8 2+2+3\ntrack bass acid {}\n", 7, 8, "2+2+3", 3360, 480},
		{"meter 5/4\ntrack bass acid {}\n", 5, 4, "", 4800, 960}, // edition 1 too
	} {
		s, ds := Parse([]byte(c.source))
		if s == nil || parseHasErrors(ds) {
			t.Fatalf("%q: %v", c.source, ds)
		}
		m := s.Meter
		if m == nil || m.Beats != c.beats || m.Unit != c.unit {
			t.Fatalf("%q: meter %+v", c.source, m)
		}
		if got := m.String(); got != strings.TrimSpace(strings.Split(strings.SplitN(c.source, "meter ", 2)[1], "\n")[0]) {
			t.Fatalf("%q: meter writes as %q", c.source, got)
		}
		if mm := s.MeterMap(); mm.BarTicks(0) != c.barTicks || mm.BeatTicks(0) != c.beat || mm.BarStart(16) != 16*c.barTicks {
			t.Fatalf("%q: bar %d beat %d", c.source, mm.BarTicks(0), mm.BeatTicks(0))
		}
	}
	s, ds := Parse([]byte("cicada 2\ntrack bass acid {}\n"))
	if s == nil || parseHasErrors(ds) || s.Meter != nil || s.MeterMap().BarTicks(0) != 3840 {
		t.Fatalf("a score without a meter must be 4/4: %v %+v", ds, s)
	}
}

func TestMeterDiagnostics(t *testing.T) {
	for _, c := range []struct{ source, code, message string }{
		{"cicada 2\nmeter 3/5\n", "CICADA-METER", "meter must be N/D with N from 1 to 32 and D of 2, 4, 8 or 16"},
		{"cicada 2\nmeter 33/4\n", "CICADA-METER", "meter must be N/D with N from 1 to 32 and D of 2, 4, 8 or 16"},
		{"cicada 2\nmeter 3/4T\n", "CICADA-METER", "meter must be N/D with N from 1 to 32 and D of 2, 4, 8 or 16"},
		{"cicada 2\nmeter 7/8 2+2+2\n", "CICADA-METER", "beat groups 2+2+2 add up to 6; 7/8 has 7 beats"},
		{"cicada 2\nmeter 32/2\n", "CICADA-LIMIT", "a bar of 32/2 is 61440 ticks; the limit is 30720"},
		{"cicada 2\nmeter 3/4\nmeter 6/8\n", "CICADA-DUPLICATE", "duplicate meter declaration"},
		{waltzHeader + "track bass acid {}\npattern hits { c3 . c4 . }\narrange {\n  place p bass hits { at = @3.4.1 length = 1bar }\n}\n", "", "@3.4.1 is outside bar 3, which is in 3/4 (beats 1 to 3, steps 1 to 4)"},
	} {
		_, ds := Parse([]byte(c.source))
		found := false
		for _, d := range ds {
			if (c.code == "" || d.Code == c.code) && strings.Contains(d.Message, c.message) {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: want %s %q, got %v", c.source, c.code, c.message, ds)
		}
	}
}

// A track or pattern may still be named meter, as tempo may.
func TestMeterIsStillAName(t *testing.T) {
	s, ds := Parse([]byte("cicada 2\nmeter 3/4\ntrack meter acid {}\npattern meter { c2 g2 g2 }\nscene a { meter = meter }\nsong { a }\n"))
	if s == nil || parseHasErrors(ds) || len(s.Tracks) != 1 || s.Tracks[0].Name != "meter" {
		t.Fatalf("%v", ds)
	}
}

// A meter declared in a later file places the arrangement of an earlier one.
func TestMeterInALaterFilePlacesTheArrangement(t *testing.T) {
	files := []SourceFile{
		{Path: "main.cicada", Source: []byte(arrangeFixture)},
		{Path: "later.cicada", Source: []byte("meter 3/4\n")},
	}
	s, ds := ParseFiles(files, 2)
	if s == nil || parseHasErrors(ds) {
		t.Fatal(ds)
	}
	// @2.3.4 on 3/4: 2880 + 2*960 + 3*240; @5.1.1: 4*2880.
	if p, m := s.Arrange.Placements[0], s.Arrange.Markers[0]; p.AtTick != 5520 || m.AtTick != 11520 {
		t.Fatalf("placement at %d, marker at %d", p.AtTick, m.AtTick)
	}
}

func TestLibrariesCannotDeclareAMeter(t *testing.T) {
	ds := CheckLibrary(SourceFile{Path: "lib/a.cicada", Source: []byte("meter 3/4\n")})
	if len(ds) != 1 || ds[0].Code != "CICADA-LIB-DECL" || ds[0].Message != "meter cannot be declared in a library" {
		t.Fatalf("%v", ds)
	}
}
