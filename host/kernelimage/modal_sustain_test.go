package kernelimage_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/modal"
)

// modalPedalMarker is a float32 whose bytes occur nowhere else in a small image, so
// a test can find the pedal value in a track record. The profile byte comes
// just before it.
const modalPedalMarker = float32(0.15625)

// modalFlaggedVibraphone is the profile byte of a vibraphone record that carries a
// pedal value.
const modalFlaggedVibraphone = byte(modal.Vibraphone) | 0x80

// modalPedalImageConfig has a vibraphone and a marimba track.
func modalPedalImageConfig(sustain float32) engine.Config {
	cfg := engine.Config{SampleRate: 48000, MaxBlock: 128, Tracks: 2, MaxVoices: 8, BPMMilli: 120000}
	cfg.Track[0] = engine.TrackConfig{Kind: engine.VoiceModal, Modal: modal.Vibraphone, PianoSustain: sustain}
	cfg.Track[1] = engine.TrackConfig{Kind: engine.VoiceModal, Modal: modal.Marimba}
	return cfg
}

func oneModalTrack(sustain float32) engine.Config {
	cfg := modalPedalImageConfig(sustain)
	cfg.Tracks = 1
	cfg.Track[1] = engine.TrackConfig{}
	return cfg
}

func modalImageCapabilities(image []byte) uint16 { return binary.LittleEndian.Uint16(image[30:32]) }

func modalPedalControl(track uint8, tick int64) cmd.Command {
	return cmd.Command{Op: cmd.OpSetParam, Track: track, Index: uint16(kernel.ParamPianoSustain), Arg0: math.Float32bits(1), Tick: tick}
}

func modalPedalScenes(track uint8) []engine.Scene {
	return []engine.Scene{{}, {Settings: []engine.SceneSetting{{Track: track, ID: kernel.ParamPianoSustain, Value: 1}}}}
}

var modalPedalSong = []engine.SongEntry{{Scene: 0, Bars: 1}, {Scene: 1, Bars: 1}}

// modalPedalAt finds the one place the marker appears.
func modalPedalAt(t *testing.T, image []byte) int {
	t.Helper()
	var want [4]byte
	binary.LittleEndian.PutUint32(want[:], math.Float32bits(modalPedalMarker))
	if bytes.Count(image, want[:]) != 1 {
		t.Fatalf("the pedal value occurs %d times", bytes.Count(image, want[:]))
	}
	return bytes.Index(image, want[:])
}

func TestImagesWithoutAModalPedalKeepTheirBytes(t *testing.T) {
	// These hashes come from the encoder before the pedal existed.
	cfg := modalPedalImageConfig(0)
	cfg.Track[0].GainDB, cfg.Track[0].GainSet, cfg.Track[1].Pan = -6, true, .25
	cfg.Scenes = []engine.Scene{{Settings: []engine.SceneSetting{{Track: 0, ID: kernel.ParamMixGain, Value: -3}}}}
	cfg.Song = []engine.SongEntry{{Scene: 0, Bars: 2}}
	one := engine.Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 4, BPMMilli: 120000, Track: [engine.MaxTracks]engine.TrackConfig{{Kind: engine.VoiceModal, Modal: modal.Vibraphone}}}
	for _, tc := range []struct {
		name   string
		cfg    engine.Config
		length int
		sha    string
	}{
		{"two modal tracks and a scene", cfg, 439, "c71067f38f744288b30f1d023386a6790afb7da6c3bf9f3cfe7914c3d0d974da"},
		{"one vibraphone", one, 223, "e2d0eb8c5f06a1c805b00bbb326278df1fcabb4e56f3a4cb8b892b03a51e6d02"},
	} {
		image, err := kernelimage.Encode(tc.cfg)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(image)
		if len(image) != tc.length || hex.EncodeToString(sum[:]) != tc.sha || modalImageCapabilities(image) != kernelimage.ModalCapability {
			t.Fatalf("%s: %d bytes, capabilities %#x, sha256 %x", tc.name, len(image), modalImageCapabilities(image), sum)
		}
		decoded, err := kernelimage.Decode(image, 48000, 128)
		if err != nil || decoded.Track[0].PianoSustain != 0 {
			t.Fatalf("%s: old image no longer loads: %v", tc.name, err)
		}
	}
}

func TestModalPedalRoundTrip(t *testing.T) {
	plain, err := kernelimage.Encode(modalPedalImageConfig(0))
	if err != nil {
		t.Fatal(err)
	}
	image, err := kernelimage.Encode(modalPedalImageConfig(modalPedalMarker))
	if err != nil {
		t.Fatal(err)
	}
	// The pedal costs four bytes on the track that has one, and no capability
	// bit or version: the header is the plain image's.
	if len(image) != len(plain)+4 || !bytes.Equal(image[:32], plain[:32]) || modalImageCapabilities(image) != kernelimage.ModalCapability {
		t.Fatalf("pedal image is %d bytes against %d, capabilities %#x", len(image), len(plain), modalImageCapabilities(image))
	}
	if at := modalPedalAt(t, image); image[at-1] != modalFlaggedVibraphone || plain[at-1] != byte(modal.Vibraphone) {
		t.Fatalf("profile byte %#x, want %#x (plain image %#x)", image[at-1], modalFlaggedVibraphone, plain[at-1])
	}
	decoded, err := kernelimage.Decode(image, 48000, 128)
	if err != nil || decoded.Track[0].PianoSustain != modalPedalMarker || decoded.Track[0].Modal != modal.Vibraphone || decoded.Track[1].PianoSustain != 0 || decoded.Track[1].Modal != modal.Marimba {
		t.Fatalf("round trip: %v, %+v", err, decoded.Track[:2])
	}
	again, err := kernelimage.Encode(decoded)
	if err != nil || !bytes.Equal(image, again) {
		t.Fatal("image bytes changed")
	}
}

func TestModalPedalFlagIsOutsideTheOldProfileRange(t *testing.T) {
	// This is a proxy check: it runs no older decoder. A decoder from before
	// the pedal accepts a modal profile byte below modal.ProfileCount and
	// nothing else, and the flagged byte is above it, so that decoder refuses
	// the image. The header is the plain image's, so the track record is what
	// refuses.
	image, err := kernelimage.Encode(modalPedalImageConfig(modalPedalMarker))
	if err != nil {
		t.Fatal(err)
	}
	if flagged := image[modalPedalAt(t, image)-1]; flagged < byte(modal.ProfileCount) {
		t.Fatalf("an older decoder would read %d as a profile", flagged)
	}
	if modal.ProfileCount >= 0x80 {
		t.Fatal("profile numbers reach the pedal flag")
	}
}

func TestModalPedalInSceneSettingsAndAutomation(t *testing.T) {
	// A control that moves a modal pedal makes its track carry one, so an older
	// decoder refuses the image at the track and no control reaches a kernel
	// that would fault on it.
	scene := modalPedalImageConfig(0)
	scene.Scenes, scene.Song = modalPedalScenes(1), modalPedalSong
	automation := modalPedalImageConfig(0)
	automation.Automation = []cmd.Command{modalPedalControl(0, 480)}
	both := modalPedalImageConfig(0)
	both.Automation = []cmd.Command{modalPedalControl(0, 480), modalPedalControl(1, 960)}
	other := modalPedalImageConfig(0)
	other.Automation = []cmd.Command{{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixPan), Arg0: math.Float32bits(.5), Tick: 0}}
	for _, tc := range []struct {
		name  string
		cfg   engine.Config
		flags int // the tracks whose record carries a pedal
	}{
		{"scene setting on the marimba", scene, 1},
		{"automation on the vibraphone", automation, 1},
		{"automation on both", both, 2},
	} {
		image, err := kernelimage.Encode(tc.cfg)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		flagged := 0
		for _, profile := range []byte{byte(modal.Vibraphone), byte(modal.Marimba)} {
			flagged += bytes.Count(image, []byte{profile | 0x80})
		}
		if flagged != tc.flags {
			t.Fatalf("%s: %d flagged profile bytes, want %d", tc.name, flagged, tc.flags)
		}
		decoded, err := kernelimage.Decode(image, 48000, 128)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		again, err := kernelimage.Encode(decoded)
		if err != nil || !bytes.Equal(image, again) {
			t.Fatalf("%s: image bytes changed", tc.name)
		}
	}
	// Moving another control of a modal track does not give it a pedal.
	if image, err := kernelimage.Encode(other); err != nil || bytes.Contains(image, []byte{modalFlaggedVibraphone}) {
		t.Fatalf("another control flagged the track: %v", err)
	}
	// A pedal on a track that is not modal is nobody's pedal for this record.
	graph := modalPedalImageConfig(0)
	graph.Track[1] = engine.TrackConfig{Kind: engine.VoicePiano}
	graph.Automation = []cmd.Command{modalPedalControl(1, 0)}
	if image, err := kernelimage.Encode(graph); err != nil || bytes.Contains(image, []byte{byte(modal.Marimba) | 0x80}) {
		t.Fatalf("a piano pedal flagged a modal record: %v", err)
	}
}

func TestModalPedalImageRules(t *testing.T) {
	valid, err := kernelimage.Encode(oneModalTrack(modalPedalMarker))
	if err != nil {
		t.Fatal(err)
	}
	at := modalPedalAt(t, valid)
	// The decoder reads the pedal value and leaves its range to the engine,
	// which builds the voice and refuses a value outside 0 to 1. The kernel
	// carries no second copy of that check.
	for name, value := range map[string]uint32{"above one": math.Float32bits(1.5), "negative": math.Float32bits(-.5), "not a number": 0x7fc00000, "infinite": 0x7f800000} {
		image := bytes.Clone(valid)
		binary.LittleEndian.PutUint32(image[at:], value)
		decoded, err := kernelimage.Decode(image, 48000, 128)
		if err != nil {
			t.Fatalf("pedal %s: %v", name, err)
		}
		if _, err := engine.New(decoded); err == nil || !strings.Contains(err.Error(), "modal sustain") {
			t.Fatalf("pedal %s: the engine built the voice: %v", name, err)
		}
	}
	// The writer marks a record that has a pedal, a scene setting or an
	// automation control for one, so that a decoder from before the pedal
	// refuses the image. The reader does not insist on the mark: the engine
	// takes the control either way. Take the flag and the value off a record
	// whose scene setting moves the pedal, and the setting still reaches it.
	scene := oneModalTrack(0)
	scene.Scenes, scene.Song = modalPedalScenes(0), modalPedalSong
	image, err := kernelimage.Encode(scene)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(image, []byte{modalFlaggedVibraphone}) {
		t.Fatal("a scene setting for the pedal did not mark the track record")
	}
	unmarked := bytes.Replace(image, []byte{modalFlaggedVibraphone, 0, 0, 0, 0}, []byte{byte(modal.Vibraphone)}, 1)
	if len(unmarked) != len(image)-4 {
		t.Fatalf("expected the marked record to hold a zero pedal value: %d bytes, %d marked", len(unmarked), len(image))
	}
	decoded, err := kernelimage.Decode(unmarked, 48000, 128)
	if err != nil || len(decoded.Scenes) != 2 || len(decoded.Scenes[1].Settings) != 1 {
		t.Fatalf("an unmarked record with a pedal setting: %v", err)
	}
	if _, err := engine.New(decoded); err != nil {
		t.Fatal(err)
	}
	// A marked record whose value is zero loads as a track with no pedal.
	empty := bytes.Clone(valid)
	binary.LittleEndian.PutUint32(empty[at:], 0)
	if decoded, err := kernelimage.Decode(empty, 48000, 128); err != nil || decoded.Track[0].PianoSustain != 0 {
		t.Fatalf("a marked record with a zero pedal: %v", err)
	}
	bad := oneModalTrack(1.5)
	if _, err := kernelimage.Encode(bad); err == nil {
		t.Fatal("encoded a pedal above one")
	}
	bad.Track[0].PianoSustain = float32(math.NaN())
	if _, err := kernelimage.Encode(bad); err == nil {
		t.Fatal("encoded a pedal that is not a number")
	}
}

func TestModalPedalImagePlaysLikeTheConfig(t *testing.T) {
	// The image path must carry the pedal all the way to the voice: the same
	// pattern rings after its gate when the pedal is in the image, and not when
	// it is not.
	build := func(sustain float32) *engine.Engine {
		cfg := engine.Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 4, BPMMilli: 120000}
		cfg.Track[0] = engine.TrackConfig{Kind: engine.VoiceModal, Modal: modal.Vibraphone, PianoSustain: sustain}
		pattern := seq.Pattern{Len: 16, GatePercent: 50}
		pattern.Steps[0], _ = seq.PackStep(seq.Step{Note: 60, Gate: true, Velocity: 100, Ratchet: 1, Probability: 100})
		cfg.Patterns = []engine.PatternBank{{}}
		cfg.Patterns[0].Slots[0] = pattern
		image, err := kernelimage.Encode(cfg)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := kernelimage.Decode(image, 48000, 128)
		if err != nil {
			t.Fatal(err)
		}
		e, err := engine.New(decoded)
		if err != nil {
			t.Fatal(err)
		}
		e.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0, Index: 0})
		e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
		return e
	}
	// The gate ends at frame 3000 and the damper needs 3360 more, so count the
	// energy from frame 8192 to 16384.
	tail := func(e *engine.Engine) float64 {
		var left, right [128]float32
		var sum float64
		for block := 0; block < 16384/128; block++ {
			e.Render(left[:], right[:])
			if block >= 8192/128 {
				for _, v := range left {
					sum += float64(v) * float64(v)
				}
			}
		}
		return sum
	}
	if pedal, plain := tail(build(1)), tail(build(0)); pedal < 1e-2 || plain > 1e-9 {
		t.Fatalf("energy after the gate: %.3e with the pedal in the image, %.3e without", pedal, plain)
	}
}

func TestDamagedModalPedalRecordNeverReachesAnEngineOutOfRange(t *testing.T) {
	// The decoder leaves the pedal's range to the engine. Try every value of
	// each byte of a marked record, the profile byte and the four of the value:
	// the image either fails to load, or loads with a pedal from 0 to 1.
	valid, err := kernelimage.Encode(oneModalTrack(modalPedalMarker))
	if err != nil {
		t.Fatal(err)
	}
	at := modalPedalAt(t, valid)
	loaded := 0
	for position := at - 1; position < at+4; position++ {
		for value := 0; value < 256; value++ {
			image := bytes.Clone(valid)
			image[position] = byte(value)
			decoded, err := kernelimage.Decode(image, 48000, 128)
			if err != nil {
				continue
			}
			if _, err := engine.New(decoded); err != nil {
				continue
			}
			loaded++
			if sustain := decoded.Track[0].PianoSustain; !(sustain >= 0 && sustain <= 1) {
				t.Fatalf("byte %d set to %#x loaded with a pedal of %g", position-at, value, sustain)
			}
		}
	}
	if loaded == 0 {
		t.Fatal("no damaged record loaded, so the check proves nothing")
	}
}
