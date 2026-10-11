package liveplay

import (
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	hostkeys "m31labs.dev/cicada/host/keyboard"
	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/kernel/voice/guitar"
	"m31labs.dev/cicada/kernel/voice/modeledkit"
)

type previewVoiceKind struct {
	name string
	kind engine.VoiceKind
}

// Read the kernel's published constants rather than maintaining another list.
// The gap at kind 7 is not a voice and must not silently become a test fixture.
func previewVoiceKinds(t *testing.T) []previewVoiceKind {
	t.Helper()
	source, err := parser.ParseFile(token.NewFileSet(), filepath.Join("..", "..", "kernel", "engine", "engine.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var voices []previewVoiceKind
	for _, declaration := range source.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}
		iotaValue := false
		value := 0
		for index, declaration := range group.Specs {
			spec := declaration.(*ast.ValueSpec)
			if len(spec.Names) != 1 || !strings.HasPrefix(spec.Names[0].Name, "Voice") {
				continue
			}
			if len(spec.Values) != 0 {
				switch expression := spec.Values[0].(type) {
				case *ast.Ident:
					if expression.Name != "iota" {
						t.Fatalf("unrecognized voice constant %s", spec.Names[0].Name)
					}
					iotaValue = true
				case *ast.BasicLit:
					value, err = strconv.Atoi(expression.Value)
					if err != nil {
						t.Fatal(err)
					}
					iotaValue = false
				default:
					t.Fatalf("unrecognized voice constant %s", spec.Names[0].Name)
				}
			}
			if iotaValue {
				value = index
			}
			voices = append(voices, previewVoiceKind{spec.Names[0].Name, engine.VoiceKind(value)})
		}
	}
	if len(voices) == 0 {
		t.Fatal("no kernel voice kinds found")
	}
	return voices
}

type previewPreparedFactory struct{}
type previewPreparedVoice struct{}

func (previewPreparedFactory) VoiceCount() int { return 1 }
func (previewPreparedFactory) NewStereoVoice(int) (engine.StereoVoice, error) {
	return previewPreparedVoice{}, nil
}
func (previewPreparedVoice) NextStereo() (float32, float32)      { return 0, 0 }
func (previewPreparedVoice) NoteOn(uint8, uint8) error           { return nil }
func (previewPreparedVoice) NoteOff()                            {}
func (previewPreparedVoice) Reset()                              {}
func (previewPreparedVoice) SelectSlot(uint8, int64, bool) error { return nil }
func (previewPreparedVoice) Play() error                         { return nil }

func previewParameterConfig(t *testing.T, kind engine.VoiceKind) engine.Config {
	t.Helper()
	program := graph.Program{Len: 2, Output: 1}
	program.Nodes[0] = graph.Node{Op: graph.Constant, Value: 375}
	program.Nodes[1] = graph.Node{Op: graph.Sine, A: 0}
	keys, err := hostkeys.DefaultSpec(hostkeys.Names[0])
	if err != nil {
		t.Fatal(err)
	}
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: 32, BPMMilli: 120_000}
	cfg.Track[0] = engine.TrackConfig{
		Kind: kind, Graph: program, Guitar: guitar.DefaultParams(), Experimental: true,
		Sample: &engine.SamplerConfig{Voices: 1, RootKey: 60}, Keys: &keys, Prepared: previewPreparedFactory{},
		GainDB: -6, GainSet: true, Pan: -1, BusSFX: true,
	}
	for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
		cfg.Track[0].Drums[lane] = drum.DefaultParams(lane)
	}
	cfg.Assets = []engine.AudioAsset{{SampleRate: 48_000, Left: make([]float32, 256), Right: make([]float32, 256)}}
	return cfg
}

func previewParameterScore(t *testing.T, kind engine.VoiceKind) Score {
	t.Helper()
	cfg := previewParameterConfig(t, kind)
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	score := Score{Engine: created, SampleRate: 48_000, BPMMilli: 120_000, Tracks: []TrackSlots{{ID: "bass"}}}
	for _, spec := range kernel.Params {
		if spec.Scope == "track" && spec.Live {
			score.Parameters = append(score.Parameters, ParameterValue{Track: 0, ID: spec.ID, Value: spec.Default})
		}
	}
	return score
}

func TestPreviewParameterVoicePairsAndBoundaries(t *testing.T) {
	voices := previewVoiceKinds(t)
	accepted, rejected := 0, 0
	boundaryAccepted, boundaryRejected := 0, 0
	for _, voice := range voices {
		t.Run(voice.name, func(t *testing.T) {
			p, err := New(previewParameterScore(t, voice.kind), 48_000)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			family := strings.ToLower(strings.TrimPrefix(voice.name, "Voice"))
			if voice.kind == engine.VoiceKeys {
				family = "piano" // The compiler resolves modeled keyboards to piano controls.
			}
			if family != "acid" && family != "drums" && family != "guitar" && family != "piano" {
				family = "instrument"
			}
			var pcm [blockFrames * 8]byte
			for _, spec := range kernel.Params {
				descriptor, ok := paramdefs.Lookup(spec.Name)
				if !ok {
					t.Fatalf("kernel parameter %s is absent from the registry", spec.Name)
				}
				want := spec.Live && spec.Scope == "track" && slices.Contains(descriptor.Voices, family)
				before := p.overrides.Load()
				version, err := p.SetPreview(0, spec.ID, spec.Default)
				if (err == nil) != want {
					_, renderErr := p.Read(pcm[:])
					t.Fatalf("%s/%s accepted=%v, want %v; render error: %v", voice.name, spec.Name, err == nil, want, renderErr)
				}
				if !want {
					rejected++
					if p.overrides.Load() != before {
						t.Fatalf("rejected %s changed the control snapshot", spec.Name)
					}
					if _, err := p.Read(pcm[:]); err != nil {
						t.Fatalf("rejected %s faulted playback: %v", spec.Name, err)
					}
					continue
				}
				accepted++
				for _, value := range []float32{spec.Default, spec.Min, spec.Max} {
					wantValue := previewKernelAcceptsValue(t, voice.kind, spec.ID, value)
					before := p.overrides.Load()
					version, err = p.SetPreview(0, spec.ID, value)
					if (err == nil) != wantValue {
						t.Fatalf("%s boundary %.17g accepted=%v, engine accepts=%v", spec.Name, value, err == nil, wantValue)
					}
					if !wantValue {
						boundaryRejected++
						t.Logf("Known kernel follow-up: %s/%s rejects documented boundary %.17g", voice.name, spec.Name, value)
						if p.overrides.Load() != before {
							t.Fatalf("rejected %s boundary changed the control snapshot", spec.Name)
						}
						if _, err := p.Read(pcm[:]); err != nil || !p.current.Engine.Playing() {
							t.Fatalf("rejected %s boundary stopped playback: %v", spec.Name, err)
						}
						// The nearest representable value inside the advertised range
						// must still work; do not reject the entire parameter.
						inward := math.Nextafter32(value, spec.Default)
						if !previewKernelAcceptsValue(t, voice.kind, spec.ID, inward) {
							t.Fatalf("%s has more than a one-ULP boundary mismatch", spec.Name)
						}
						version, err = p.SetPreview(0, spec.ID, inward)
						if err != nil {
							t.Fatalf("%s inward boundary %g: %v", spec.Name, inward, err)
						}
					} else {
						boundaryAccepted++
					}
					if _, err := p.Read(pcm[:]); err != nil {
						t.Fatalf("accepted %s boundary %g faulted playback: %v", spec.Name, value, err)
					}
					if p.overrides.Load().tracks["bass"].state.appliedVersions[spec.ID] != version || !p.current.Engine.Playing() {
						t.Fatalf("accepted %s boundary %g was not applied to a running engine", spec.Name, value)
					}
				}
				for _, value := range []float32{math.Nextafter32(spec.Min, float32(math.Inf(-1))), math.Nextafter32(spec.Max, float32(math.Inf(1)))} {
					before := p.overrides.Load()
					if _, err := p.SetPreview(0, spec.ID, value); err == nil || p.overrides.Load() != before {
						t.Fatalf("%s accepted or published out-of-range value %g", spec.Name, value)
					}
				}
				if err := p.CancelPreview(version); err != nil {
					t.Fatal(err)
				}
				if _, err := p.Read(pcm[:]); err != nil {
					t.Fatalf("canceling %s faulted playback: %v", spec.Name, err)
				}
			}
		})
	}
	total := len(voices) * len(kernel.Params)
	if accepted+rejected != total {
		t.Fatalf("%d pairs remain unverified", total-accepted-rejected)
	}
	t.Logf("%d voice kinds x %d kernel parameters = %d pairs: accepted %d, rejected %d, unverified 0; default/min/max cases accepted %d, rejected %d; just-outside and inward bounds checked", len(voices), len(kernel.Params), total, accepted, rejected, boundaryAccepted, boundaryRejected)
}

// The test owns both publication and rendering of this independent engine.
// Its real command path is the oracle for float32 values widened by the DSP.
func previewKernelAcceptsValue(t *testing.T, kind engine.VoiceKind, id kernel.ParamID, value float32) bool {
	t.Helper()
	e := previewParameterScore(t, kind).Engine
	if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) || !e.Push(cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(id), Arg0: math.Float32bits(value)}) {
		t.Fatal("oracle engine rejected valid command framing")
	}
	var left, right [blockFrames]float32
	e.Render(left[:], right[:])
	var message cmd.Message
	for e.Poll(&message) {
		if message.Kind == cmd.Fault {
			if message.A != engine.FaultParam {
				t.Fatalf("oracle fault %d is unrelated to parameter admission", message.A)
			}
			return false
		}
	}
	if !e.Playing() {
		t.Fatal("oracle engine stopped without a parameter fault")
	}
	return true
}

func TestPreviewPreparedDrumValidationMatchesPlayingRecipe(t *testing.T) {
	for _, modeled := range []bool{false, true} {
		name := "remapped-recipe"
		id, value := kernel.ParamDrumBdTune, float32(55)
		if modeled {
			name, id, value = "modeled-controls", kernel.ParamDrumSdTune, 1.1
		}
		t.Run(name, func(t *testing.T) {
			cfg := previewParameterConfig(t, engine.VoiceDrums)
			kit := new([drum.LaneCount]engine.KitLaneBinding)
			for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
				kit[lane] = engine.KitLaneBinding{Kind: engine.KitLaneBuiltin, Recipe: lane}
			}
			kit[drum.BD].Recipe = drum.SD
			if modeled {
				kit[drum.SD] = engine.KitLaneBinding{Kind: engine.KitLaneModeled, Model: modeledkit.Snare, ModelParams: modeledkit.DefaultParams(), ModelLevelDB: -6}
			}
			cfg.Track[0].Kit = kit
			created, err := engine.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			p, err := New(Score{Engine: created, SampleRate: 48_000, BPMMilli: 120_000, Tracks: []TrackSlots{{ID: "bass"}}}, 48_000)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			before := p.overrides.Load()
			if _, err := p.SetPreview(0, id, value); err == nil || p.overrides.Load() != before {
				t.Fatal("preview accepted a value the prepared kit rejects")
			}
			if _, err := p.SetPreview(0, kernel.ParamDrumSdLevel, -3); err != nil {
				t.Fatalf("prepared kit rejected a valid level change: %v", err)
			}
			renderPreview(t, p, blockFrames)
			if !created.Playing() {
				t.Fatal("prepared kit validation stopped playback")
			}
		})
	}
}

func TestPreviewVoiceChangeRetiresIncompatibleOverride(t *testing.T) {
	p, err := New(previewParameterScore(t, engine.VoicePiano), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err := p.SetPreview(0, kernel.ParamPianoSustain, .5); err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, blockFrames)
	if err := p.Offer(previewParameterScore(t, engine.VoiceGraph)); err != nil {
		t.Fatal(err)
	}
	// The offered graph must not replace the playing piano's capabilities yet.
	if _, err := p.SetPreview(0, kernel.ParamPianoSustain, .75); err != nil {
		t.Fatalf("offered score changed playing capabilities: %v", err)
	}
	renderPreview(t, p, 120_064)
	if _, active := p.OverrideValue(0, kernel.ParamPianoSustain); active {
		t.Fatal("incompatible piano preview survived graph activation")
	}
	if _, err := p.SetPreview(0, kernel.ParamPianoSustain, .5); err == nil {
		t.Fatal("activated graph still accepts piano sustain")
	}
}

func TestPreviewDrumRecipeChangeRetiresIncompatibleOverride(t *testing.T) {
	p, err := New(previewParameterScore(t, engine.VoiceDrums), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err := p.SetPreview(0, kernel.ParamDrumBdTune, 60); err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, blockFrames)
	cfg := previewParameterConfig(t, engine.VoiceDrums)
	kit := new([drum.LaneCount]engine.KitLaneBinding)
	for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
		kit[lane] = engine.KitLaneBinding{Kind: engine.KitLaneBuiltin, Recipe: lane}
	}
	kit[drum.BD].Recipe = drum.SD
	cfg.Track[0].Kit = kit
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Offer(Score{Engine: created, SampleRate: 48_000, BPMMilli: 120_000, Tracks: []TrackSlots{{ID: "bass"}}}); err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, 120_064)
	if _, active := p.OverrideValue(0, kernel.ParamDrumBdTune); active || !created.Playing() {
		t.Fatal("incompatible drum preview survived recipe activation")
	}
}

func TestPreviewDrumValidationDoesNotAllocateOnTheRenderThread(t *testing.T) {
	for _, reject := range []bool{false, true} {
		name := "apply"
		if reject {
			name = "retire-invalid"
		}
		t.Run(name, func(t *testing.T) {
			p, err := New(previewParameterScore(t, engine.VoiceDrums), 48_000)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			var snapshots [101]*liveOverrides
			for i := range snapshots {
				if _, err := p.SetPreview(0, kernel.ParamDrumSdTune, 1); err != nil {
					t.Fatal(err)
				}
				snapshots[i] = p.overrides.Load()
				if reject {
					// Simulate an incompatible snapshot arriving at activation.
					// Public preview admission rejects this kernel boundary.
					snapshots[i].tracks["bass"].values[kernel.ParamDrumSdTune].value = .7
				}
			}
			var output [blockFrames * 8]byte
			var readErr error
			iteration := 0
			allocations := testing.AllocsPerRun(100, func() {
				p.overrides.Store(snapshots[iteration])
				_, readErr = p.Read(output[:])
				iteration++
			})
			if allocations != 0 || readErr != nil {
				t.Fatalf("render allocations=%g, error=%v", allocations, readErr)
			}
			_, active := p.OverrideValue(0, kernel.ParamDrumSdTune)
			if active == reject || !p.current.Engine.Playing() {
				t.Fatal("render did not apply or retire the preview while playing")
			}
		})
	}
}
