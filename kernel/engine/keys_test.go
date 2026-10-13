package engine

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/keyboard"
)

type fakeKeys struct {
	notes      [128]uint8
	released   [128]bool
	sustain    float32
	resetCount int
}

func (f *fakeKeys) NoteOn(note, velocity uint8) error {
	if note < keyboard.MinNote || note > keyboard.MaxNote || velocity > 127 {
		return Error("invalid fake key")
	}
	if velocity == 0 {
		f.NoteOff(note)
		return nil
	}
	f.notes[note], f.released[note] = velocity, false
	return nil
}
func (f *fakeKeys) NoteOff(note uint8) {
	f.released[note] = true
	if f.sustain < .5 {
		f.notes[note] = 0
	}
}
func (f *fakeKeys) AllNotesOff() {
	for note := range f.notes {
		f.NoteOff(uint8(note))
	}
}
func (f *fakeKeys) NextStereo() (float32, float32) {
	var value float32
	for note, velocity := range f.notes {
		value += float32(float32(note+1)*float32(velocity)) * .000001
	}
	return value, value
}
func (f *fakeKeys) SetSustain(value float32) error {
	if value < 0 || value > 1 || math.IsNaN(float64(value)) {
		return Error("invalid fake pedal")
	}
	if value < .5 {
		for note, released := range f.released {
			if released {
				f.notes[note] = 0
			}
		}
	}
	f.sustain = value
	return nil
}
func (f *fakeKeys) Reset() {
	f.notes, f.released, f.sustain = [128]uint8{}, [128]bool{}, 0
	f.resetCount++
}

func installFakeKeys(t *testing.T) {
	t.Helper()
	old := keyboard.Prepare
	t.Cleanup(func() { keyboard.Prepare = old })
	keyboard.Prepare = func(rate int, spec *keyboard.Spec) (keyboard.Voice, error) {
		if rate != 48000 || spec.Patch != 1 {
			return nil, Error("unsupported fake keyboard")
		}
		return new(fakeKeys), nil
	}
}

func keysConfig() Config {
	cfg := Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 8, BPMMilli: 120000}
	cfg.Track[0].Kind, cfg.Track[0].Keys = VoiceKeys, &keyboard.Spec{Patch: 1}
	cfg.Track[0].Keys.Controls[127] = 8
	return cfg
}

func TestKeysVoiceKindPreservesPublishedKinds(t *testing.T) {
	if VoicePiano != 4 || VoiceGuitar != 5 || VoiceModal != 6 || VoiceAudio != 8 || VoiceSample != 9 || VoicePrepared != 10 || VoiceGraphPoly != 11 || VoiceKeys != 12 {
		t.Fatal("modeled keyboards changed a published voice identifier")
	}
}

func TestKeysFactoryAndBudgetValidation(t *testing.T) {
	installFakeKeys(t)
	cfg := keysConfig()
	cfg.MaxVoices = 7
	if _, err := New(cfg); err == nil {
		t.Fatal("keys failed to reserve eight voices")
	}
	for _, limit := range []float32{0, .5, 8.5, 9} {
		cfg = keysConfig()
		cfg.Track[0].Keys.Controls[127] = limit
		if _, err := New(cfg); err == nil {
			t.Fatalf("keys accepted voice limit %g", limit)
		}
	}
	cfg = keysConfig()
	cfg.MaxVoices = 1
	cfg.Track[0].Keys.Controls[127] = 1
	if _, err := New(cfg); err != nil {
		t.Fatalf("keys failed to reserve one voice: %v", err)
	}
	cfg = keysConfig()
	cfg.Track[0].Keys = nil
	if _, err := New(cfg); err == nil {
		t.Fatal("keys accepted nil spec")
	}
	cfg = keysConfig()
	cfg.Track[0].Keys.Patch = 0
	if _, err := New(cfg); err == nil {
		t.Fatal("keys accepted invalid patch")
	}
	cfg = keysConfig()
	cfg.Track[0].Keys.Controls[100] = float32(math.NaN())
	if _, err := New(cfg); err == nil {
		t.Fatal("keys accepted nonfinite control")
	}
	cfg = keysConfig()
	cfg.Track[0].Keys.Controls[0] = 2
	if _, err := New(cfg); err == nil {
		t.Fatal("keys ignored invalid initial sustain")
	}
	keyboard.Prepare = nil
	if _, err := New(keysConfig()); err == nil {
		t.Fatal("keys silently accepted missing factory")
	}
	keyboard.Prepare = func(int, *keyboard.Spec) (keyboard.Voice, error) { return nil, nil }
	if _, err := New(keysConfig()); err == nil {
		t.Fatal("keys silently accepted nil voice")
	}
}

func TestKeysLiveTargetedReleaseAndAllocationFreeRender(t *testing.T) {
	installFakeKeys(t)
	e, err := New(keysConfig())
	if err != nil {
		t.Fatal(err)
	}
	var left, right [128]float32
	var message cmd.Message
	allocations := testing.AllocsPerRun(50, func() {
		for _, note := range []uint32{48, 60, 64, 67} {
			e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: note | 96<<8})
		}
		e.Push(cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamPianoSustain), Arg0: math.Float32bits(.7)})
		e.Render(left[:], right[:])
		e.Push(cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: 60})
		e.Render(left[:], right[:])
		for e.Poll(&message) {
		}
	})
	if allocations != 0 || e.faulted {
		t.Fatalf("keys render allocations=%g fault=%v", allocations, e.faulted)
	}
	f := e.voices[0].keys.(*fakeKeys)
	if !f.released[60] || f.released[64] || f.notes[64] == 0 || f.notes[60] == 0 {
		t.Fatal("targeted key release or sustain failed")
	}
	e.apply(cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamPianoSustain)})
	if f.notes[60] != 0 || f.notes[64] == 0 {
		t.Fatal("pedal release affected unrelated held key")
	}
	e.apply(cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: 0xffff})
	for _, velocity := range f.notes {
		if velocity != 0 {
			t.Fatal("all-key release left held note")
		}
	}
}

func TestKeysTransposedChordMatchesLiveEvents(t *testing.T) {
	installFakeKeys(t)
	cfg := keysConfig()
	pattern := seq.Pattern{Len: 4, GatePercent: 50, Transpose: 12}
	pattern.Steps[0], _ = seq.PackStep(seq.Step{Note: 48, Gate: true, Velocity: 100, Ratchet: 1, Probability: 100})
	pattern.Chords[0] = seq.ChordStep{Count: 3, Notes: [4]uint8{48, 52, 55}}
	cfg.Patterns = []PatternBank{{}}
	cfg.Patterns[0].Slots[0] = pattern
	patternEngine, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	liveEngine, err := New(keysConfig())
	if err != nil {
		t.Fatal(err)
	}
	patternEngine.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0, Index: 0})
	patternEngine.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	liveEngine.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	for _, note := range []uint32{60, 64, 67} {
		liveEngine.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: note | 100<<8})
		liveEngine.Push(cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: uint16(note), Tick: seq.TicksPerStep / 2})
	}
	var aL, aR, bL, bR [128]float32
	for block := range 140 {
		patternEngine.Render(aL[:], aR[:])
		liveEngine.Render(bL[:], bR[:])
		if aL != bL || aR != bR {
			t.Fatalf("keys chord/live PCM differs in block %d", block)
		}
	}
	if patternEngine.faulted || liveEngine.faulted {
		t.Fatal("keys pattern or live commands faulted")
	}
}

func TestKeysPatternValidationAndUnrelatedRelease(t *testing.T) {
	installFakeKeys(t)
	cfg := keysConfig()
	pattern := seq.Pattern{Len: 4, GatePercent: 50}
	pattern.Steps[0], _ = seq.PackStep(seq.Step{Note: 60, Gate: true, Velocity: 100, Ratchet: 1, Probability: 100})
	cfg.Patterns = []PatternBank{{}}
	cfg.Patterns[0].Slots[0] = pattern
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0, Index: 0})
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	var left, right [128]float32
	e.Render(left[:], right[:])
	e.apply(cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: 64})
	if e.patterns[0].playingNote == 0 {
		t.Fatal("unrelated live release discarded pattern gate")
	}
	for range 64 {
		e.Render(left[:], right[:])
	}
	if e.voices[0].keys.(*fakeKeys).notes[60] != 0 || e.faulted {
		t.Fatal("pattern note failed to release")
	}
	e.apply(cmd.Command{Op: cmd.OpSetPatternMeta, Track: 0, Arg0: uint32(uint16(60)) << 16})
	if !e.faulted {
		t.Fatal("keys accepted out-of-range transposed pattern")
	}
	pattern.Transpose = 60
	cfg.Patterns[0].Slots[0] = pattern
	if _, err := New(cfg); err == nil {
		t.Fatal("keys accepted invalid preload")
	}
}

func TestKeysSceneSeekAndResetRestoreSustain(t *testing.T) {
	installFakeKeys(t)
	cfg := keysConfig()
	cfg.Track[0].Keys.Controls[0] = .25
	cfg.Scenes = []Scene{{}, {Settings: []SceneSetting{{Track: 0, ID: kernel.ParamPianoSustain, Value: 1}}}}
	cfg.Song = []SongEntry{{Scene: 0, Bars: 1}, {Scene: 1, Bars: 1}}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.apply(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	e.apply(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 1})
	f := e.voices[0].keys.(*fakeKeys)
	if e.faulted || f.sustain != 1 {
		t.Fatal("forward seek did not apply keys sustain")
	}
	e.apply(cmd.Command{Op: cmd.OpSeek, Track: 0xff})
	if e.faulted || f.sustain != .25 || f.resetCount == 0 {
		t.Fatal("backward seek/reset did not restore initial keys sustain")
	}
}

func TestKeysRejectPerNoteExpressionBeforePlayback(t *testing.T) {
	installFakeKeys(t)
	for _, params := range []seq.Expression{{}, {Set: true, PitchCents: 25, Pressure: .5, Timbre: .75}} {
		cfg := keysConfig()
		pattern := seq.Pattern{Len: 1, GatePercent: 50, Expression: new([64]seq.Expression)}
		pattern.Steps[0], _ = seq.PackStep(seq.Step{Note: 60, Gate: true, Ratchet: 1, Probability: 100, Velocity: 100})
		pattern.Expression[0] = params
		cfg.Patterns = []PatternBank{{Slots: [16]seq.Pattern{pattern}}}
		if e, err := New(cfg); err == nil || e != nil {
			t.Fatal("keys accepted unsupported expression storage")
		}
	}
	for _, identity := range []uint16{0, 7} {
		e, err := New(keysConfig())
		if err != nil {
			t.Fatal(err)
		}
		if !e.Push(expressionCommand(identity, .5)) {
			t.Fatal("valid expression command rejected before voice support validation")
		}
		var left, right [128]float32
		e.Render(left[:], right[:])
		var message cmd.Message
		found := false
		for e.Poll(&message) {
			found = found || message.Kind == cmd.Fault && message.A == FaultVoiceUnsupported
		}
		if !e.faulted || !found || left != [128]float32{} || right != [128]float32{} {
			t.Fatal("keys failed to reject live expression explicitly and silence playback")
		}
	}
}

func TestKeysReleaseTargetsMIDIPitchInsteadOfLiveIdentity(t *testing.T) {
	installFakeKeys(t)
	e, err := New(keysConfig())
	if err != nil {
		t.Fatal(err)
	}
	for i, note := range []uint32{60, 64, 67} {
		e.apply(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Index: uint16(500 + i), Arg0: note | 100<<8})
	}
	f := e.voices[0].keys.(*fakeKeys)
	for _, note := range []uint16{60, 64} {
		e.apply(cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: note})
		if e.faulted || !f.released[note] || f.notes[note] != 0 || f.notes[67] != 100 {
			t.Fatalf("keys release %d used live identity or affected another pitch", note)
		}
	}
	e.apply(cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: 502})
	if !e.faulted {
		t.Fatal("keys accepted a note identity as a MIDI release pitch")
	}
}

// controlKeys records live-control calls the way a real family voice receives them.
type controlKeys struct {
	fakeKeys
	values [128]float32
	glides [128]float32
	calls  int
	fail   bool
}

func (c *controlKeys) SetControl(index int, value, glide float32) error {
	if c.fail || index < 0 || index >= len(c.values) {
		return Error("fake control rejected")
	}
	c.values[index], c.glides[index] = value, glide
	c.calls++
	return nil
}

func installControlKeys(t *testing.T, patch uint8) {
	t.Helper()
	old := keyboard.Prepare
	t.Cleanup(func() { keyboard.Prepare = old })
	keyboard.Prepare = func(rate int, spec *keyboard.Spec) (keyboard.Voice, error) {
		c := new(controlKeys)
		copy(c.values[:], spec.Controls[:])
		return c, nil
	}
}

func controlKeysConfig(patch uint8) Config {
	cfg := keysConfig()
	cfg.Track[0].Keys.Patch = patch
	return cfg
}

func TestKeysControlsReachTheirFamilySlot(t *testing.T) {
	cases := []struct {
		patch uint8
		id    kernel.ParamID
		slot  int
		value float32
	}{
		{1, kernel.ParamKeysEpTremolo, 7, .6},
		{1, kernel.ParamKeysEpTremoloRate, 8, 6},
		{1, kernel.ParamKeysEpAutoPan, 9, .4},
		{1, kernel.ParamKeysEpGain, 10, 1.2},
		{1, kernel.ParamKeysEpDrive, 6, .5},
		{7, kernel.ParamKeysClavGain, 6, 1.1},
		{7, kernel.ParamKeysClavDrive, 5, .3},
		{10, kernel.ParamKeysOrganGain, 21, 1.3},
		{10, kernel.ParamKeysOrganDrive, 17, .4},
		{10, kernel.ParamKeysOrganRotaryMix, 18, .5},
		{10, kernel.ParamKeysOrganRotaryFast, 20, 1},
		{10, kernel.ParamKeysOrganDrawbar1, 1, 3},
		{10, kernel.ParamKeysOrganDrawbar9, 9, 8},
		{14, kernel.ParamKeysFmGain, 1, .9},
		{17, kernel.ParamKeysAnalogDrive, 14, .6},
		{17, kernel.ParamKeysAnalogOutput, 21, -6},
		{21, kernel.ParamKeysStringsOutput, 10, -3},
	}
	for _, c := range cases {
		installControlKeys(t, c.patch)
		e, err := New(controlKeysConfig(c.patch))
		if err != nil {
			t.Fatal(err)
		}
		f := e.voices[0].keys.(*controlKeys)
		e.apply(cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(c.id), Arg0: math.Float32bits(c.value)})
		if e.faulted || f.values[c.slot] != c.value || f.calls != 1 {
			t.Fatalf("%s did not reach slot %d: faulted=%v value=%g calls=%d", kernel.Params[c.id].Name, c.slot, e.faulted, f.values[c.slot], f.calls)
		}
		if got, want := f.glides[c.slot], e.paramAlpha[c.id]; got != want {
			t.Fatalf("%s glide %g, want the registry smoothing %g", kernel.Params[c.id].Name, got, want)
		}
	}
}

func TestKeysControlOfAnotherFamilyOrVoiceFaults(t *testing.T) {
	installControlKeys(t, 1)
	e, err := New(controlKeysConfig(1)) // electric piano
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []kernel.ParamID{kernel.ParamKeysClavGain, kernel.ParamKeysOrganDrawbar1, kernel.ParamKeysStringsOutput} {
		e.faulted = false
		e.apply(cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(id), Arg0: math.Float32bits(1)})
		if !e.faulted {
			t.Fatalf("%s reached an electric piano", kernel.Params[id].Name)
		}
	}
	installFakeKeys(t) // A voice without live controls.
	e, err = New(keysConfig())
	if err != nil {
		t.Fatal(err)
	}
	e.apply(cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamKeysEpGain), Arg0: math.Float32bits(1)})
	if !e.faulted {
		t.Fatal("a control reached a voice that has no live controls")
	}
	installControlKeys(t, 1)
	e, _ = New(controlKeysConfig(1))
	e.voices[0].keys.(*controlKeys).fail = true
	e.apply(cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamKeysEpGain), Arg0: math.Float32bits(1)})
	if !e.faulted {
		t.Fatal("a voice rejection did not fault")
	}
}

func TestKeysSceneControlsSnapshotAndRestore(t *testing.T) {
	installControlKeys(t, 1)
	cfg := controlKeysConfig(1)
	cfg.Track[0].Keys.Controls[7] = .25 // Authored tremolo.
	cfg.Scenes = []Scene{{}, {Settings: []SceneSetting{{Track: 0, ID: kernel.ParamKeysEpTremolo, Value: .9}}}, {}}
	cfg.Song = []SongEntry{{Scene: 0, Bars: 1}, {Scene: 1, Bars: 1}, {Scene: 2, Bars: 1}}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	f := e.voices[0].keys.(*controlKeys)
	e.apply(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	e.apply(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 1})
	if e.faulted || f.values[7] != .9 {
		t.Fatalf("scene did not set tremolo: faulted=%v value=%g", e.faulted, f.values[7])
	}
	// Like every scene parameter, a later scene that omits the setting keeps it.
	e.apply(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 2})
	if e.faulted || f.values[7] != .9 {
		t.Fatalf("a later scene lost the earlier tremolo setting: %g", f.values[7])
	}
	// Restarting the song restores the authored value, at once.
	e.apply(cmd.Command{Op: cmd.OpSeek, Track: 0xff})
	if e.faulted || f.values[7] != .25 || f.glides[7] != 1 {
		t.Fatalf("restart kept tremolo %g (glide %g); want the authored .25", f.values[7], f.glides[7])
	}
	calls := f.calls
	e.apply(cmd.Command{Op: cmd.OpSeek, Track: 0xff})
	if f.calls != calls {
		t.Fatal("restoring an unchanged control called the voice")
	}
}

func TestKeysAutomationNeedsItsFamily(t *testing.T) {
	installControlKeys(t, 1)
	lane := func(id kernel.ParamID, track uint8) []cmd.Command {
		return []cmd.Command{{Op: cmd.OpSetParam, Track: track, Index: uint16(id), Arg0: math.Float32bits(.5)}}
	}
	cfg := controlKeysConfig(1)
	cfg.Automation = lane(kernel.ParamKeysEpTremolo, 0)
	if _, err := New(cfg); err != nil {
		t.Fatalf("automation of an electric piano control was rejected: %v", err)
	}
	for _, c := range []struct {
		id    kernel.ParamID
		track uint8
	}{{kernel.ParamKeysClavGain, 0}, {kernel.ParamKeysEpTremolo, 0xff}, {kernel.ParamKeysOrganDrawbar1, 0}} {
		cfg.Automation = lane(c.id, c.track)
		if _, err := New(cfg); err == nil {
			t.Fatalf("automation of %s on track %d was accepted", kernel.Params[c.id].Name, c.track)
		}
	}
}

func TestKeysControlEngineAllocatesNothing(t *testing.T) {
	installControlKeys(t, 1)
	cfg := controlKeysConfig(1)
	cfg.Scenes = []Scene{{}, {Settings: []SceneSetting{{Track: 0, ID: kernel.ParamKeysEpTremolo, Value: .9}}}}
	cfg.Song = []SongEntry{{Scene: 0, Bars: 1}, {Scene: 1, Bars: 1}}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var left, right [128]float32
	var message cmd.Message
	value := float32(0)
	allocations := testing.AllocsPerRun(100, func() {
		value = 1 - value
		e.Push(cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamKeysEpGain), Arg0: math.Float32bits(value)})
		e.Push(cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamKeysEpTremolo), Arg0: math.Float32bits(value)})
		e.Render(left[:], right[:])
		for e.Poll(&message) {
		}
	})
	if allocations != 0 || e.faulted {
		t.Fatalf("allocations=%g fault=%v", allocations, e.faulted)
	}
	e.apply(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	allocations = testing.AllocsPerRun(50, func() {
		e.apply(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 1})
		e.apply(cmd.Command{Op: cmd.OpSeek, Track: 0xff})
	})
	if allocations != 0 || e.faulted {
		t.Fatalf("scene restore allocations=%g fault=%v", allocations, e.faulted)
	}
}

func TestKeysControlTableMatchesRegistry(t *testing.T) {
	for id := keysControlFirst; id <= keysControlLast; id++ {
		spec, ok := kernel.Param(id)
		if !ok || len(spec.Name) < 6 || spec.Name[:5] != "keys." {
			t.Fatalf("ID %d in the keys range is %q", id, spec.Name)
		}
		if keysControls[id-keysControlFirst].family == keyboard.NoFamily {
			t.Fatalf("%s has no family in the engine control table", spec.Name)
		}
		if got := keyboard.Family(keysControls[id-keysControlFirst].family).String(); spec.Name[5:5+len(got)] != got {
			t.Fatalf("%s is mapped to family %q", spec.Name, got)
		}
	}
	if next, ok := kernel.Param(keysControlLast + 1); ok && len(next.Name) >= 5 && next.Name[:5] == "keys." {
		t.Fatalf("%s follows the engine control table", next.Name)
	}
}
