package project

import (
	"errors"
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

// A path to a later reverb's send is refused with what is addressable. The old
// hint pointed at send.reverb, which is the send to the first reverb, so a
// scene or automate lane that followed it moved the wrong send.
func TestLaterReverbSendPathIsRefusedNotRedirected(t *testing.T) {
	p, errs := reverbScoreDiagnostics(t, "cicada 2\ntempo 120\nfx hall reverb {}\nfx room reverb {}\n"+strings.Replace(reverbTail, "SENDS", "send room = -12dB", 1))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	_, err := ResolveParameterPath(p, "keys.send.room")
	var pathErr *PathError
	if !errors.As(err, &pathErr) || pathErr.Code != "CICADA-UNSUPPORTED" || !strings.Contains(err.Error(), "send.reverb is the send to hall") || strings.Contains(err.Error(), "the send to room is send.reverb") {
		t.Fatalf("keys.send.room: %v", err)
	}
	if _, err := ResolveParameterPath(p, "keys.send.reverb"); err != nil {
		t.Fatalf("keys.send.reverb, the send to hall: %v", err)
	}
}

// Two scores that differ by a send to a later reverb are different mixes. The
// legacy send B was rebuilt as a send to the last reverb, so the extra send to
// room made the two compare equal.
func TestSemanticEqualSeesASendToALaterReverb(t *testing.T) {
	const head = "cicada 2\ntempo 120\nfx hall reverb {}\nfx room reverb {}\n"
	a, errs := reverbScoreDiagnostics(t, head+strings.Replace(reverbTail, "SENDS", "send hall = 0.5", 1))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	b, errs := reverbScoreDiagnostics(t, head+strings.Replace(reverbTail, "SENDS", "send hall = 0.5 send room = 0.5", 1))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if SemanticEqual(a, b) {
		t.Fatal("a score with an extra send to room compares equal to the score without it")
	}
	if !SemanticEqual(a, a) {
		t.Fatal("a score differs from itself")
	}
}

// A library reverb that only a scene addresses gets no return slot (the
// compiler keeps a library effect only when a track or bus sends to it or
// inserts it), so ReverbReturns, which picks the render path, does not count
// it. Preset resolution alone would keep it.
func TestLibraryReverbOnlyASceneAddressesGetsNoSlot(t *testing.T) {
	source := []byte("cicada 2\nimport \"demo/space\"\nfx room reverb {}\ntrack keys acid { send room = -12dB }\npattern p acid steps=4 { c4 . . . }\nscene main { keys=p space.hall.decay = 2s }\nsong { main }\n")
	files := []notation.SourceFile{
		{Path: "main.cicada", Source: source, Bindings: map[string]string{"space": "demo.space"}},
		{Path: "space.cicada", Library: "demo/space", Edition: 2, Source: []byte("fx hall reverb { decay = 4s }\n")},
	}
	score, ds := notation.ParseFiles(files, 2)
	if score == nil {
		t.Fatal(ds)
	}
	resolved, _ := notation.ResolvePresets(score)
	kept := false
	for _, effect := range resolved.Effects {
		kept = kept || effect.Name == "demo.space.hall"
	}
	if !kept {
		t.Fatal("preset resolution no longer keeps the scene-addressed library reverb; this test no longer covers the case")
	}
	if got := ReverbReturns(score); fmt.Sprint(got) != "[room]" {
		t.Fatalf("reverb returns %v, want [room]", got)
	}
}
