package project

import (
	"fmt"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func reverbScoreDiagnostics(t *testing.T, source string) (*Project, []notation.Diagnostic) {
	t.Helper()
	score, ds := notation.Parse([]byte(source))
	if score == nil {
		t.Fatalf("parse: %+v", ds)
	}
	var errs []notation.Diagnostic
	for _, d := range ds {
		if d.Severity == "error" {
			errs = append(errs, d)
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	p, pds := FromScore(score)
	for _, d := range pds {
		if d.Severity == "error" {
			errs = append(errs, d)
		}
	}
	return p, errs
}

const reverbTail = "track keys piano { SENDS }\npattern p { c4 . e4 . }\nscene s { keys = p }\nsong { s }\n"

// An edition-2 score routes up to seven reverbs, each reached by name; the
// first is slot B and the others slots C to H.
func TestTwoReverbReturnsCompileByName(t *testing.T) {
	source := "cicada 2\ntempo 120\nfx hall reverb {}\nfx room reverb {}\n" + strings.Replace(reverbTail, "SENDS", "send hall = -12dB send room = -18dB pre", 1)
	p, errs := reverbScoreDiagnostics(t, source)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	cfg, err := CompileEngine(p, 48000, 512)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReverbB == nil || cfg.Routing == nil || len(cfg.Routing.Returns) != 1 || cfg.Routing.Returns[0].Name != "room" || cfg.Routing.Returns[0].Slot != 2 {
		t.Fatalf("returns: slot B %v, routing %+v", cfg.ReverbB != nil, cfg.Routing)
	}
	if cfg.Track[0].SendB <= 0 || cfg.Track[0].SendBPre || len(cfg.Routing.TrackSends) != 1 || len(cfg.Routing.TrackSends[0]) != 1 || !cfg.Routing.TrackSends[0][0].Pre {
		t.Fatalf("sends: B %v pre %v, extra %+v", cfg.Track[0].SendB, cfg.Track[0].SendBPre, cfg.Routing.TrackSends)
	}
	if p.Tracks[0].Mixer.SendB != cfg.Track[0].SendB {
		t.Fatalf("the legacy send B is %v, want the send to hall %v, not the send to room", p.Tracks[0].Mixer.SendB, cfg.Track[0].SendB)
	}
}

// Edition 1 addresses returns by kind, so it keeps one reverb; an eighth
// reverb is over the limit. Both errors point at the declaration.
func TestReverbReturnLimits(t *testing.T) {
	_, errs := reverbScoreDiagnostics(t, "cicada 1\ntempo 120\nfx hall reverb {}\nfx room reverb {}\n"+strings.Replace(reverbTail, "SENDS", "", 1))
	if len(errs) == 0 || errs[0].Code != "CICADA-VERSION" || errs[0].Position.Line != 4 {
		t.Errorf("edition 1 with two reverbs: %+v, want CICADA-VERSION at line 4", errs)
	}
	var decls strings.Builder
	for i := 1; i <= 8; i++ {
		fmt.Fprintf(&decls, "fx r%d reverb {}\n", i)
	}
	_, errs = reverbScoreDiagnostics(t, "cicada 2\ntempo 120\n"+decls.String()+strings.Replace(reverbTail, "SENDS", "", 1))
	if len(errs) == 0 || errs[0].Code != "CICADA-LIMIT" || errs[0].Position.Line != 10 || !strings.Contains(errs[0].Message, "r8") {
		t.Errorf("eight reverbs: %+v, want CICADA-LIMIT naming r8 at line 10", errs)
	}
	_, errs = reverbScoreDiagnostics(t, "cicada 2\ntempo 120\nfx hall reverb {}\nfx room reverb {}\n"+strings.Replace(reverbTail, "SENDS", "send room = -12dB send room = -6dB", 1))
	if len(errs) == 0 {
		t.Error("two sends to the same reverb were accepted")
	}
}

// A later reverb's controls are not addressed yet: its path is refused
// instead of moving the first reverb, which the kind-addressed control reaches.
func TestExtraReverbControlsAreFixed(t *testing.T) {
	p, errs := reverbScoreDiagnostics(t, "cicada 2\ntempo 120\nfx hall reverb {}\nfx room reverb {}\n"+strings.Replace(reverbTail, "SENDS", "send room = -12dB", 1))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if _, err := ResolveParameterPath(p, "hall.decay"); err != nil {
		t.Fatalf("the first reverb's control: %v", err)
	}
	_, err := ResolveParameterPath(p, "room.decay")
	if err == nil || !strings.Contains(err.Error(), "first reverb, hall") {
		t.Fatalf("room.decay: %v, want a refusal that names the first reverb", err)
	}
	for _, address := range ParamAddresses(p) {
		if strings.HasPrefix(address.Address, "room.") {
			t.Fatalf("live address %s for the second reverb", address.Address)
		}
	}
}
