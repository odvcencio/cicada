package gosxhost

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/sdk/director"
)

func testSurface() director.Surface {
	return director.Surface{
		Version: 1, SampleRate: 48000, Tracks: 2, Land: "bar", PhraseBars: 8,
		Macros:      []director.Macro{{Name: "intensity", ID: 0, SmoothFrames: 19200}},
		States:      []director.State{{Name: "explore", ID: 0, Scene: 0}, {Name: "combat", ID: 1, Scene: 1}},
		Stingers:    []director.Stinger{{Name: "hit", Track: 1, Slot: 2, Quantize: "beat", CrossfadeFrames: 480}},
		Transitions: []director.Transition{{From: "explore", To: "combat", Quantize: "bar", CrossfadeFrames: 9600}},
		Setup:       []cmd.Command{{Op: cmd.OpDefineMacro, Track: 255, Index: 0, Arg0: 0x3e99999a}},
	}
}

func testImage() []byte {
	image := make([]byte, 64)
	copy(image, "\x43\x49\x43\x31")
	return image
}

func testPack() *Pack {
	return &Pack{Kernel: KernelCore, SampleRate: 48000, TempoMilli: 120000, RequiredCapabilities: 1 << 16, Image: testImage(), Surface: testSurface()}
}

func TestPackRoundTrip(t *testing.T) {
	data, err := testPack().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParsePack(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.SampleRate != 48000 || got.TempoMilli != 120000 || got.RequiredCapabilities != 1<<16 || got.Kernel != KernelCore {
		t.Fatalf("header fields changed: %+v", got)
	}
	if len(got.Image) != 64 || got.Surface.States[1].Name != "combat" || got.Surface.Transitions[0].CrossfadeFrames != 9600 {
		t.Fatalf("payload changed: %+v", got.Surface)
	}
	tempo, caps := got.Info()
	if tempo != 120000 || caps != 1<<16 {
		t.Fatalf("Info = %d, %d", tempo, caps)
	}
}

func TestParsePackRejectsDamage(t *testing.T) {
	good, _ := testPack().Marshal()
	flip := func(i int) []byte { d := append([]byte(nil), good...); d[i] ^= 0xff; return d }
	cases := map[string][]byte{
		"empty":         nil,
		"wrong magic":   append([]byte("NOTASCOR"), good[8:]...),
		"bit flip":      flip(40),
		"short":         good[:20],
		"truncated crc": good[:len(good)-1],
	}
	for name, data := range cases {
		if _, err := ParsePack(data); err == nil {
			t.Errorf("%s: ParsePack accepted damaged data", name)
		}
	}
	// A pack whose manifest and header disagree about the rate is refused even
	// with a valid checksum.
	p := testPack()
	p.Surface.SampleRate = 44100
	data, _ := p.Marshal()
	if _, err := ParsePack(data); err == nil || !strings.Contains(err.Error(), "sample rates") {
		t.Errorf("rate mismatch: err = %v", err)
	}
}

func TestMarshalRejectsMissingImage(t *testing.T) {
	p := testPack()
	p.Image = nil
	if _, err := p.Marshal(); err == nil {
		t.Fatal("Marshal accepted a pack without an image")
	}
}

func TestBackendJSONMatchesWorkletBackendShape(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal(BackendJSON(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"maxFrames", "channels", "commandBytes", "messageBytes", "messageCapacity", "supportedCapabilities", "faultOpcode", "faultArgumentOffset", "transport", "opcodeCapabilities"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("backend JSON lacks %q", key)
		}
	}
	b := DefaultBackend()
	if b.CommandBytes != cmd.CommandSize || b.MessageBytes != cmd.MessageSize {
		t.Fatal("backend record sizes drifted from kernel/cmd")
	}
	if int(cmd.Fault) != int(b.FaultOpcode) {
		t.Fatalf("fault opcode %d, kernel Fault kind %d", b.FaultOpcode, cmd.Fault)
	}
	tr := b.Transport
	if tr.Play != uint8(cmd.OpPlay) || tr.Stop != uint8(cmd.OpStop) || tr.Seek != uint8(cmd.OpSeek) {
		t.Fatal("transport opcodes drifted from kernel/cmd")
	}
	want := []uint8{uint8(cmd.OpSelectPattern), uint8(cmd.OpLaunchScene)}
	if len(tr.QuantizedOpcodes) != 2 || tr.QuantizedOpcodes[0] != want[0] || tr.QuantizedOpcodes[1] != want[1] {
		t.Fatalf("quantized opcodes %v, want %v", tr.QuantizedOpcodes, want)
	}
}

func TestBackendOffsetsMatchCommandLayout(t *testing.T) {
	// The host reads the seek bar at byte 4, the seek tick at byte 8 and writes a
	// boundary tick at byte 16, all little-endian, in the 24-byte record. Encode
	// real commands and check the fields land where the host looks.
	tr := DefaultBackend().Transport
	seek, err := cmd.EncodeCommand(cmd.Command{Op: cmd.OpSeek, Track: 255, Arg0: 7, Arg1: 123}, 4)
	if err != nil {
		t.Fatal(err)
	}
	if seek[tr.SeekBarOffset] != 7 || seek[tr.SeekTickOffset] != 123 {
		t.Fatalf("seek fields are not at the declared offsets: %v", seek)
	}
	launch, err := cmd.EncodeCommand(cmd.Command{Op: cmd.OpLaunchScene, Track: 255, Tick: 0x0102}, 4)
	if err == nil && (launch[tr.BoundaryTickOffset] != 0x02 || launch[tr.BoundaryTickOffset+1] != 0x01) {
		t.Fatalf("boundary tick is not at the declared offset: %v", launch)
	}
}

func TestPackageKeepsASmallDependencySurface(t *testing.T) {
	// A game's js/wasm build imports this package, so it must not pull in the
	// compiler, the notation parser, the host packages or GoSX.
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	for _, pkg := range strings.Fields(string(out)) {
		if !strings.HasPrefix(pkg, "m31labs.dev/") {
			continue
		}
		switch pkg {
		case "m31labs.dev/cicada/kernel", "m31labs.dev/cicada/kernel/limits", "m31labs.dev/cicada/kernel/cmd",
			"m31labs.dev/cicada/sdk/director", "m31labs.dev/cicada/sdk/gosxhost":
		default:
			t.Errorf("sdk/gosxhost depends on %s", pkg)
		}
	}
}
