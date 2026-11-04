package notation

import (
	"strings"
	"testing"
)

func TestMappedMacroValidatesRangesAndTargets(t *testing.T) {
	source := `cicada 2
track bass acid {}
live { macro filter -> bass.cutoff 200Hz..4kHz curve exp smooth 150ms }
pattern p { 1 }
scene main { bass = p }
song { main }
`
	s, diagnostics := Parse([]byte(source))
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	if len(s.Live.Macros) != 1 || s.Live.Macros[0].Target != "bass.cutoff" || s.Live.Macros[0].Curve != "exp" {
		t.Fatalf("mapping lost: %+v", s.Live.Macros)
	}
	for _, bad := range []string{
		strings.Replace(source, "200Hz..4kHz", "4kHz..200Hz", 1),
		strings.Replace(source, "200Hz..4kHz", "0Hz..4kHz", 1),
		strings.Replace(source, "curve exp", "curve spline", 1),
		strings.Replace(source, "bass.cutoff", "bass.missing", 1),
		strings.Replace(source, "bass.cutoff 200Hz..4kHz curve exp", "bass.mute 0..1 curve linear", 1),
	} {
		if _, ds := Parse([]byte(bad)); len(ds) == 0 {
			t.Fatalf("invalid mapped macro accepted: %s", bad)
		}
	}
	integer := strings.Replace(source, "track bass acid", "track bass tonewheel_organ", 1)
	integer = strings.Replace(integer, "bass.cutoff 200Hz..4kHz curve exp", "bass.drawbar1 0..8 curve linear", 1)
	_, ds := Parse([]byte(integer))
	if len(ds) != 1 || !strings.Contains(ds[0].Message, "continuous live parameter") {
		t.Fatalf("integer target should be rejected as non-continuous: %+v", ds)
	}
}
