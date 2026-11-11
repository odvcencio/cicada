package notation

import (
	"strings"
	"testing"
)

// A wrapped error that starts with its own code prints the code once.
func TestDiagnosticPrintsItsCodeOnce(t *testing.T) {
	ds := []Diagnostic{{Code: "CICADA-PARAM", Severity: "error", Message: "CICADA-PARAM: chord too large"}}
	if got, want := ds[0].Error(), "0:0: error CICADA-PARAM: chord too large"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	other := Diagnostic{Code: "CICADA-UNIT", Message: "CICADA-PARAM: kept"}
	if trimCode(other.Code, other.Message) != "CICADA-PARAM: kept" {
		t.Fatal("a different leading code was removed")
	}
}

// A send path may name the effect, as a track's send line does; a send path
// that names an effect that is not a send return gets a hint with the paths.
func TestSendPathNamedByEffectGetsAHint(t *testing.T) {
	source := "cicada 2\ntempo 120\nfx echo tape { time = 1/8 }\nfx hall reverb { size = 0.5 }\nfx grit drive {}\ntrack keys acid { send echo = 0.2 send hall = 0.2 insert = grit }\npattern p acid { 1 . 3 . }\nscene s { keys = p keys.send.echo = 0.1 keys.send.hall = -6dB }\nsong { s }\n"
	score, ds := Parse([]byte(source))
	if score == nil {
		t.Fatalf("parse: %+v", ds)
	}
	if ds = append(ds, Validate(score)...); len(ds) != 0 {
		t.Fatalf("send paths by effect name: %+v", ds)
	}
	score, _ = Parse([]byte(strings.Replace(source, "keys.send.hall = -6dB", "keys.send.grit = 0.1", 1)))
	for _, d := range Validate(score) {
		if strings.Contains(d.Message, "only the delay and reverb returns have track send paths") {
			return
		}
	}
	t.Fatal("no diagnostic names the send paths for a send to a drive")
}
