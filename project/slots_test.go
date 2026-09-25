package project

import (
	"bytes"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

const slottedScore = `cicada 1
track bass acid {}
pattern first acid steps=1 slot=7 { 1 }
pattern second acid steps=1 slot=0 { 3 }
pattern third acid steps=1 { 5 }
scene a { bass=first }
scene b { bass=second }
scene c { bass=third }
song { a b c }
`

func TestExplicitSlotsPrecedeAutomaticAllocation(t *testing.T) {
	score, diagnostics := notation.Parse([]byte(slottedScore))
	if len(diagnostics) != 0 {
		t.Fatalf("parse: %+v", diagnostics)
	}
	p, diagnostics := FromScore(score)
	if p == nil {
		t.Fatalf("project: %+v", diagnostics)
	}
	for slot, want := range map[int]string{0: "second", 1: "third", 7: "first"} {
		if got := p.Tracks[0].Slots[slot]; got == nil || *got != want {
			t.Fatalf("slot %d = %v, want %s", slot, got, want)
		}
	}
	cfg, err := CompileEngine(p, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	for slot, want := range map[int]uint8{0: 48, 1: 52, 7: 45} {
		step, err := seq.UnpackStep(cfg.Patterns[0].Slots[slot].Steps[0])
		if err != nil || step.Note != want {
			t.Fatalf("engine slot %d note = %d, %v; want %d", slot, step.Note, err, want)
		}
	}
	source, err := ToSource(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(source, []byte("pattern first {\n  slot = 7")) {
		t.Fatalf("normalized source lost explicit slot:\n%s", source)
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Tracks[0].Slots[7] == nil || *decoded.Tracks[0].Slots[7] != "first" {
		t.Fatal("JSON round-trip lost explicit slot")
	}
}

func TestExplicitSlotCollisionIsRejected(t *testing.T) {
	src := strings.Replace(slottedScore, "slot=0", "slot=7", 1)
	_, diagnostics := notation.Parse([]byte(src))
	for _, d := range diagnostics {
		if d.Code == "CICADA-DUPLICATE" && d.Position.Line == 4 {
			return
		}
	}
	t.Fatalf("slot collision did not produce a positioned diagnostic: %+v", diagnostics)
}

func TestExplicitSlotOutOfRangeIsRejected(t *testing.T) {
	for _, value := range []string{"-1", "16", "1.5"} {
		src := strings.Replace(slottedScore, "slot=7", "slot="+value, 1)
		_, diagnostics := notation.Parse([]byte(src))
		found := false
		for _, d := range diagnostics {
			if d.Severity == "error" {
				found = true
			}
		}
		if !found {
			t.Fatalf("slot %s accepted", value)
		}
	}
}

func TestAutomaticSlotCanDifferAcrossTracks(t *testing.T) {
	src := []byte(`cicada 1
track bass acid {}
track lead acid {}
pattern first acid steps=1 { 1 }
pattern shared acid steps=1 { 3 }
scene a { bass=first lead=shared }
scene b { bass=shared }
song { a b }
`)
	score, diagnostics := notation.Parse(src)
	if len(diagnostics) != 0 {
		t.Fatalf("parse: %+v", diagnostics)
	}
	p, diagnostics := FromScore(score)
	if p == nil {
		t.Fatalf("project: %+v", diagnostics)
	}
	if p.Tracks[0].Slots[1] == nil || *p.Tracks[0].Slots[1] != "shared" ||
		p.Tracks[1].Slots[0] == nil || *p.Tracks[1].Slots[0] != "shared" {
		t.Fatalf("unexpected automatic slots: %+v %+v", p.Tracks[0].Slots, p.Tracks[1].Slots)
	}
	converted, err := ToSource(p)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(converted, []byte("pattern shared {\n  slot =")) {
		t.Fatalf("per-track automatic slot became a global explicit slot:\n%s", converted)
	}
}

func TestSourceGeneratedSlotLayoutsRoundTrip(t *testing.T) {
	random := rand.New(rand.NewSource(42))
	for trial := 0; trial < 48; trial++ {
		var source strings.Builder
		source.WriteString("cicada 1\n")
		for track := 0; track < 3; track++ {
			fmt.Fprintf(&source, "track t%d acid {}\n", track)
		}
		reserved := map[int]bool{}
		for pattern := 0; pattern < 5; pattern++ {
			fmt.Fprintf(&source, "pattern p%d acid steps=1", pattern)
			if random.Intn(3) == 0 {
				for slot := random.Intn(16); ; slot = (slot + 1) % 16 {
					if !reserved[slot] {
						reserved[slot] = true
						fmt.Fprintf(&source, " slot=%d", slot)
						break
					}
				}
			}
			fmt.Fprintf(&source, " { %d }\n", pattern%7+1)
		}
		var scenes []string
		for track := 0; track < 3; track++ {
			for pattern := 0; pattern < 5; pattern++ {
				if pattern != track && random.Intn(2) == 0 {
					continue
				}
				name := "s" + strconv.Itoa(len(scenes))
				scenes = append(scenes, name)
				fmt.Fprintf(&source, "scene %s { t%d=p%d }\n", name, track, pattern)
			}
		}
		fmt.Fprintf(&source, "song { %s }\n", strings.Join(scenes, " "))
		score, diagnostics := notation.Parse([]byte(source.String()))
		if len(diagnostics) != 0 {
			t.Fatalf("trial %d parse: %+v\n%s", trial, diagnostics, source.String())
		}
		p, diagnostics := FromScore(score)
		if p == nil {
			t.Fatalf("trial %d project: %+v\n%s", trial, diagnostics, source.String())
		}
		if _, err := ToSource(p); err != nil {
			t.Fatalf("trial %d round-trip: %v\n%s", trial, err, source.String())
		}
	}
}
