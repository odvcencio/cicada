package main

import (
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	hostkeys "m31labs.dev/cicada/host/keyboard"
	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/kernel/voice/modal"
	"m31labs.dev/cicada/project"
)

func compiledPreviewRestoreScore(t *testing.T, source string) liveplay.Score {
	t.Helper()
	path := t.TempDir() + "/score.cicada"
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	score, err := compileLiveScoreAtRate(path, 48_000)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(source, "bass.") {
		plan, err := buildLivePlan(path, []byte(source), 48_000)
		if err != nil {
			t.Fatal(err)
		}
		plan.prepareScore(&score)
	}
	return score
}

type previewRestoreFixture struct {
	name, family, kind, declaration, pattern string
	kit                                      bool
}

func previewRestoreFixtures() []previewRestoreFixture {
	fixtures := []previewRestoreFixture{
		{"acid", "acid", "acid", "", "pattern beat notes steps=2 { c4 . }", false},
		{"drums", "drums", "drums", "", "", false},
		{"piano", "piano", "piano", "", "pattern beat notes steps=2 { c4 . }", false},
		{"guitar", "guitar", "guitar", "", "pattern beat notes steps=2 { c4 . }", false},
		{"instrument", "instrument", "tone", "instrument tone { voice mono { out=sine(pitch)*env(gate,80ms) } }", "pattern beat notes steps=2 { c4 . }", false},
		{"declared-piano", "instrument", "piano", "instrument piano { voice poly { out=sine(pitch)*env(gate,80ms) } }", "pattern beat notes steps=2 { c4 . }", false},
		{"modal-alias", "instrument", "model_" + modal.Names[0], "", "pattern beat notes steps=2 { c4 . }", false},
	}
	var lanes, bindings strings.Builder
	for _, name := range drum.Names {
		fmt.Fprintf(&lanes, "%s:x.;", name)
		fmt.Fprintf(&bindings, "%s=builtin.%s;", name, name)
	}
	pattern := "pattern beat drums steps=2 {" + lanes.String() + "}"
	fixtures[1].pattern = pattern
	fixtures = append(fixtures,
		previewRestoreFixture{"named-kit", "drums", "acoustic", "kit acoustic {" + bindings.String() + "}", pattern, true},
		previewRestoreFixture{"kit-named-piano", "drums", "piano", "kit piano {" + bindings.String() + "}", pattern, true},
	)
	for _, name := range hostkeys.Names {
		fixtures = append(fixtures, previewRestoreFixture{name, "piano", name, "", "pattern beat notes steps=2 { c4 . }", false})
	}
	return fixtures
}

func authoredPreviewRestoreValue(descriptor paramdefs.Descriptor, value float64) string {
	if descriptor.Curve == "toggle" {
		if value == 0 {
			return "off"
		}
		return "on"
	}
	suffix := ""
	if descriptor.Unit == "Hz" || descriptor.Unit == "ms" || descriptor.Unit == "dB" {
		suffix = descriptor.Unit
	}
	return fmt.Sprintf("%.17g%s", value, suffix)
}

func previewRestoreSource(fixture previewRestoreFixture, descriptor paramdefs.Descriptor, value float64) string {
	source := descriptor.Source
	switch descriptor.ID {
	case "mix.send_a":
		source = "send delay"
	case "mix.send_b":
		source = "send reverb"
	}
	body := source + "=" + authoredPreviewRestoreValue(descriptor, value)
	sceneSetting := ""
	if fixture.kit && strings.HasPrefix(descriptor.ID, "drum.") {
		body = "" // Named-kit synthesis settings are authored on the scene.
		sceneSetting = " bass." + descriptor.Path + "=" + authoredPreviewRestoreValue(descriptor, value)
	}
	if fixture.family == "guitar" {
		body += " experimental=on"
	}
	pattern := strings.Replace(fixture.pattern, "steps=2 {", "{ step=1/64 gate=25% ", 1)
	witness, scene := "", "bass=beat"
	scene += sceneSetting
	if descriptor.ID == "mix.solo" {
		// Solo requires another sounding track to have an audible effect.
		witness = "instrument witness { voice mono { out=sine(pitch)*env(gate,80ms) } }\ntrack other witness {}\npattern second notes { step=1/64 gate=25% g5 . }\n"
		scene += " other=second"
	}
	return "cicada 2\ntempo 120\nseed 17\nfx delay delay { time=5ms feedback=0 }\nfx reverb reverb { predelay=0ms size=0.5 }\n" + fixture.declaration + "\ntrack bass " + fixture.kind + " {" + body + "}\n" + pattern + "\n" + witness + "scene main { " + scene + " }\nsong { main }\n"
}

func TestPreviewCompiledParameterLifecycleMatrix(t *testing.T) {
	pairs, cases := 0, 0
	families := make(map[string]bool)
	for _, fixture := range previewRestoreFixtures() {
		families[fixture.family] = true
		for _, descriptor := range paramdefs.Registry {
			if descriptor.Scope != "track" || !descriptor.Live || !paramdefs.HasVoice(descriptor, fixture.family) {
				continue
			}
			pairs++
			values := []float64{descriptor.Default, descriptor.Min, descriptor.Max}
			for boundary, value := range values {
				for _, exit := range []string{"cancel", "socket-teardown"} {
					t.Run(fmt.Sprintf("%s/%s/%d/%s", fixture.name, descriptor.ID, boundary, exit), func(t *testing.T) {
						source := previewRestoreSource(fixture, descriptor, value)
						score := compiledPreviewRestoreScore(t, source)
						baseline := compiledPreviewRestoreScore(t, source)
						p, err := liveplay.New(score, 48_000)
						if err != nil {
							t.Fatal(err)
						}
						defer p.Close()
						if len(score.SceneParameters) > 0 {
							renderStudioPreview(t, p, 256)
						}
						id, _ := kernel.FindParam(descriptor.ID)
						committed, ok := p.CommittedValue(0, id)
						if !ok {
							t.Fatalf("compiled %s has no committed value", descriptor.ID)
						}
						if fixture.kit && strings.HasPrefix(descriptor.ID, "drum.") {
							expected, err := project.ParameterFloat32Value(descriptor.Min, descriptor.Max, value)
							if err != nil || committed != expected {
								t.Fatalf("scene-authored %s committed %g, want %g: %v", descriptor.ID, committed, expected, err)
							}
						}
						s := &studio{transport: &studioTransport{stream: p}}
						session := make(livePreviewSession)
						spec := kernel.Params[id]
						gesture := spec.Default
						if gesture == committed {
							gesture, err = project.ParameterFloat32Value(descriptor.Min, descriptor.Max, descriptor.Max)
							if err != nil {
								t.Fatal(err)
							}
						}
						message := fmt.Sprintf(`{"type":"preview-set","entity":"track:bass","param":%q,"value":%g}`, descriptor.ID, gesture)
						if err := s.handleLiveMessage([]byte(message), session); err != nil {
							t.Fatal(err)
						}
						renderStudioPreview(t, p, 256)
						if !score.Engine.Playing() {
							t.Fatal("preview stopped playback")
						}
						if exit == "cancel" {
							message = fmt.Sprintf(`{"type":"preview-end","entity":"track:bass","param":%q,"commit":false}`, descriptor.ID)
							if err := s.handleLiveMessage([]byte(message), session); err != nil {
								t.Fatal(err)
							}
						} else {
							session.close()
						}
						renderStudioPreview(t, p, 256)
						if _, active := p.OverrideValue(0, id); active || !score.Engine.Playing() {
							t.Fatal("restore left an override active or stopped playback")
						}
						for len(p.Events()) > 0 {
							if event := <-p.Events(); event.Kind == "preview-error" {
								t.Fatalf("restoration unavailable: %+v", event)
							}
						}
						p.Close()
						assertPreviewRestoreAudio(t, score.Engine, baseline.Engine, id, committed)
						cases++
					})
				}
			}
		}
	}
	for _, descriptor := range paramdefs.Registry {
		if descriptor.Live && descriptor.Scope == "track" {
			for _, family := range descriptor.Voices {
				if !families[family] {
					t.Fatalf("unverified voice family %s", family)
				}
			}
		}
	}
	t.Logf("%d compiled fixture/parameter pairs; %d complete preview/restore audio cases across %d fixtures", pairs, cases, len(previewRestoreFixtures()))
}

func assertPreviewRestoreAudio(t *testing.T, restored, baseline *engine.Engine, id kernel.ParamID, committed float32) {
	t.Helper()
	// The player has closed and this test exclusively owns the renderers.
	// Check the engine's drum target, independently of the host snapshot.
	validator := restored.PreviewParamValidator(0)
	if actual, ok := validator.CommittedValue(id); ok && float32(actual) != committed {
		t.Fatalf("engine target %g differs from committed value %g", actual, committed)
	}
	// The test owns both renderers. Lifecycle assertions above run before any
	// reset, so a latched fault cannot be hidden. Reset removes note/FX history
	// while retaining parameter targets; compare fresh committed-note playback.
	var left, right, wantLeft, wantRight [256]float32
	restored.Reset()
	baseline.Reset()
	for range 32 {
		restored.Render(left[:], right[:])
		baseline.Render(wantLeft[:], wantRight[:])
	}
	restored.Reset()
	baseline.Reset()
	if !restored.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) || !baseline.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
		t.Fatal("audio probe could not resume its owned engines")
	}
	var difference, reference float64
	for range 8 {
		restored.Render(left[:], right[:])
		baseline.Render(wantLeft[:], wantRight[:])
		for i := range left {
			l, r := float64(left[i]-wantLeft[i]), float64(right[i]-wantRight[i])
			difference += l*l + r*r
			reference += float64(wantLeft[i])*float64(wantLeft[i]) + float64(wantRight[i])*float64(wantRight[i])
		}
	}
	tolerance := .003
	if name := kernel.Params[id].Name; strings.HasPrefix(name, "drum.") && strings.HasSuffix(name, ".tune") {
		// Smoothed tuning stops a few ulps from its target. Square waves
		// can flip one sample at exact phase boundaries. The exact target
		// assertion above prevents this PCM tolerance hiding a bad restore.
		tolerance = .1
	}
	if math.Sqrt(difference/4096) > tolerance*math.Sqrt(reference/4096)+.00001 {
		t.Fatalf("restored audio differs from compiled committed playback: difference RMS %g, reference RMS %g", math.Sqrt(difference/4096), math.Sqrt(reference/4096))
	}
	var message cmd.Message
	for _, renderer := range []*engine.Engine{restored, baseline} {
		if !renderer.Playing() {
			t.Fatal("audio probe stopped playback")
		}
		for renderer.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatalf("audio probe fault %d", message.A)
			}
		}
	}
}

func TestPreviewCompiledDrumRestore(t *testing.T) {
	for _, input := range []struct {
		name, source, param string
		value               float32
	}{
		{"authored-minimum", "track bass drums { sd_tune=0.7 }\n", "drum.sd.tune", 1},
		{"named-kit", "kit acoustic { bd=builtin.bd; sd=builtin.sd; }\ntrack bass acoustic {}\n", "drum.sd.level", -3},
		{"remapped-kit", "kit acoustic { sd=builtin.cp; }\ntrack bass acoustic {}\n", "drum.sd.decay", 200},
	} {
		for _, exit := range []string{"cancel", "socket-teardown"} {
			t.Run(input.name+"/"+exit, func(t *testing.T) {
				source := input.source + "pattern beat drums steps=1 { sd:x; }\nscene main { bass=beat }\nsong { main }\n"
				score := compiledPreviewRestoreScore(t, source)
				baseline := compiledPreviewRestoreScore(t, source)
				p, err := liveplay.New(score, 48_000)
				if err != nil {
					t.Fatal(err)
				}
				defer p.Close()
				s := &studio{transport: &studioTransport{stream: p}}
				session := make(livePreviewSession)
				message := fmt.Sprintf(`{"type":"preview-set","entity":"track:bass","param":%q,"value":%g}`, input.param, input.value)
				if err := s.handleLiveMessage([]byte(message), session); err != nil {
					t.Fatal(err)
				}
				renderStudioPreview(t, p, 256)
				if exit == "cancel" {
					message = fmt.Sprintf(`{"type":"preview-end","entity":"track:bass","param":%q,"commit":false}`, input.param)
					if err := s.handleLiveMessage([]byte(message), session); err != nil {
						t.Fatal(err)
					}
				} else {
					session.close() // Same cleanup called by the socket reader's defer.
				}
				renderStudioPreview(t, p, 256)
				id, _ := kernel.FindParam(input.param)
				committed, ok := p.CommittedValue(0, id)
				if !ok {
					t.Fatal("committed value is unavailable")
				}
				if _, active := p.OverrideValue(0, id); active || !score.Engine.Playing() {
					t.Fatal("restore left an override active or stopped the playing engine")
				}
				p.Close()
				assertPreviewRestoreAudio(t, score.Engine, baseline.Engine, id, committed)
			})
		}
	}
}
