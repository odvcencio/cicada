package project

import (
	"encoding/binary"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/notation"
	"strings"
	"testing"
)

const ampAutomationScore = `cicada 2
fx clean eq { type = highpass frequency = 80Hz }
fx rig ampcab { drive = 0.7 tone = 0.4 output = -3dB cabinet = 1 }
track lead acid { insert = clean -> rig }
track double acid { insert = rig }
pattern line acid { 1 . 3 . }
scene main { lead = line double = line }
song { main*2 }
automate lead.amp_drive { @1.1.1 0.2 @2.1.1 0.8 smooth }
`

func TestAmpAutomationUniqueTrackAddressAndImage(t *testing.T) {
	p := masterProject(t, ampAutomationScore)
	for _, v := range []struct {
		path  string
		value float64
		id    kernel.ParamID
	}{{"lead.amp_drive", .7, kernel.ParamAmpDrive}, {"lead.amp_tone", .4, kernel.ParamAmpTone}, {"lead.amp_output", -3, kernel.ParamAmpOutput}, {"double.amp_drive", .7, kernel.ParamAmpDrive}} {
		a, err := ParamAddressByName(p, v.path)
		if err != nil || a.Value != v.value {
			t.Fatalf("%s = %+v %v", v.path, a, err)
		}
		resolved, err := ResolveParameterPath(p, v.path)
		if err != nil || resolved.ID != v.id {
			t.Fatalf("resolve %s: %+v %v", v.path, resolved, err)
		}
	}
	source, err := ToSource(p)
	if err != nil {
		t.Fatal(err)
	}
	q := masterProject(t, string(source))
	if !SemanticEqual(p, q) {
		t.Fatal("source lost automation")
	}
	cfg, err := CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Automation) < 2 {
		t.Fatal("automation controls missing")
	}
	image, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	bits := binary.LittleEndian.Uint16(image[30:32])
	if bits&kernelimage.AmpAutomationCapability == 0 {
		t.Fatal("amp capability missing")
	}
	decoded, err := kernelimage.Decode(image, 48000, 128)
	if err != nil || len(decoded.Automation) != len(cfg.Automation) {
		t.Fatalf("image roundtrip: %v", err)
	}
	binary.LittleEndian.PutUint16(image[30:32], bits & ^kernelimage.AmpAutomationCapability)
	if _, err := kernelimage.Decode(image, 48000, 128); err == nil {
		t.Fatal("unnegotiated controls accepted")
	}
	// Legacy static amp images have no new capability and remain readable.
	cfg.Automation = nil
	legacy, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	oldBits := binary.LittleEndian.Uint16(legacy[30:32])
	binary.LittleEndian.PutUint16(legacy[30:32], oldBits & ^kernelimage.AmpAutomationCapability)
	if _, err := kernelimage.Decode(legacy, 48000, 128); err != nil {
		t.Fatalf("legacy static amp image rejected: %v", err)
	}
	if kernel.ParamKeysEpTremolo != 151 || kernel.ParamKeysStringsOutput != 174 || kernel.ParamAmpDrive != 175 || kernel.ParamAmpOutput != 177 {
		t.Fatal("published parameter IDs changed")
	}
}
func TestAmpAutomationRejectsAmbiguousOrMissing(t *testing.T) {
	for _, src := range []string{strings.Replace(ampAutomationScore, "insert = clean -> rig", "insert = clean", 1), strings.Replace(ampAutomationScore, "fx clean eq", "fx other ampcab {}\nfx clean eq", 1)} {
		if strings.Contains(src, "fx other ampcab") {
			src = strings.Replace(src, "insert = clean -> rig", "insert = rig -> other", 1)
		}
		score, ds := notation.Parse([]byte(src))
		if hasTestErrors(ds) {
			continue
		}
		p, ds := FromScore(score)
		if p != nil && !hasTestErrors(ds) {
			t.Fatal("accepted missing/ambiguous amp")
		}
	}
}
