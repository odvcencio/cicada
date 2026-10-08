package keyboard

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"

	"m31labs.dev/cicada/instrument/keyboardpresets"
)

func TestAllPatchesPrepareAndRenderWithoutAllocating(t *testing.T) {
	for _, name := range Names {
		t.Run(name, func(t *testing.T) {
			s, err := DefaultSpec(name)
			if err != nil || Name(s.Patch) != name {
				t.Fatalf("patch lookup: %v", err)
			}
			for _, rate := range []int{44100, 48000, 96000, 192000} {
				v, err := New(rate, &s)
				if err != nil {
					t.Fatalf("prepare at %d: %v", rate, err)
				}
				var energy float64
				var noteErr error
				allocs := testing.AllocsPerRun(5, func() {
					v.Reset()
					for _, note := range [4]uint8{48, 60, 64, 67} {
						noteErr = v.NoteOn(note, 102)
					}
					for frame := 0; frame < 4096; frame++ {
						if frame == 2048 {
							v.AllNotesOff()
						}
						l, r := v.NextStereo()
						energy += float64(l)*float64(l) + float64(r)*float64(r)
					}
				})
				if noteErr != nil || allocs != 0 || energy <= 0 || math.IsNaN(energy) || math.IsInf(energy, 0) {
					t.Fatalf("rate=%d err=%v allocations=%g energy=%g", rate, noteErr, allocs, energy)
				}
			}
		})
	}
}

func TestCompoundValidationAndFloat32Boundaries(t *testing.T) {
	for _, name := range []string{"tine_ep", "clav", "soft_pad", "string_machine", "fm_ep"} {
		s, _ := DefaultSpec(name)
		for _, p := range Parameters(name) {
			if p.Name == "release" || p.Name == "pickup_distance" || p.Name == "tangent" || p.Name == "gain" {
				s.Controls[p.Index] = p.Min
			}
		}
		if _, err := New(48000, &s); err != nil {
			t.Fatalf("valid float32 boundary %s: %v", name, err)
		}
	}
	for _, name := range []string{"poly_keys", "string_machine", "fm_ep"} {
		s, _ := DefaultSpec(name)
		switch name {
		case "poly_keys":
			s.Controls[1], s.Controls[2] = 0, 0
		case "string_machine":
			s.Controls[1], s.Controls[2], s.Controls[3] = 0, 0, 0
		default:
			for op := 0; op < 6; op++ {
				s.Controls[14+op*12] = 0
			}
		}
		if err := Validate(&s); err == nil {
			t.Fatalf("accepted silent compound patch %s", name)
		}
	}
	s, _ := DefaultSpec("tine_ep")
	s.Controls[127] = 1
	if _, err := New(48000, &s); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []float32{0, 8.5, float32(math.NaN())} {
		s.Controls[127] = bad
		if _, err := New(48000, &s); err == nil {
			t.Fatalf("accepted voice limit %g", bad)
		}
	}
}

func TestDefaultSpecDelegatesUnchanged(t *testing.T) {
	for _, name := range Names {
		got, err := DefaultSpec(name)
		want, wantErr := keyboardpresets.DefaultSpec(name)
		if err != nil || wantErr != nil || got != want {
			t.Fatalf("%s: delegate differs (%v, %v)", name, err, wantErr)
		}
	}
	hash := sha256.New()
	for _, name := range Names {
		spec, err := DefaultSpec(name)
		if err != nil {
			t.Fatal(err)
		}
		_ = binary.Write(hash, binary.LittleEndian, spec.Patch)
		_ = binary.Write(hash, binary.LittleEndian, spec.Controls[:])
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != "f00b7923e2695cb8e55930ce22dec2e9f8b095c7f61a19f45887cb81e79a8605" {
		t.Fatalf("preset table changed: %s", got)
	}
}
