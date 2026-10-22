package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/project"
)

func TestVocalSceneControlsKeepBackingPCM(t *testing.T) {
	isolateState(t)
	for _, test := range []struct{ name, settings, lane string }{
		{"new lanes", "lead.level=-18dB lead.pan=-0.5 lead.cutoff=250Hz", ""},
		{"curve takes over later", "lead.level=-18dB", "automate lead.level { @5.1.1 -9dB curve 2 @6.1.1 -14dB smooth }\n"},
		{"active smooth curve", "lead.level=-18dB", "automate lead.level { @1.1.1 -9dB @6.1.1 -14dB smooth }\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, entry, _, owner := songSourcesStudio(t, t.TempDir())
			root := filepath.Dir(s.path)
			scenes := []byte("scene first { lead=one vox=off " + test.settings + " }\nscene held { lead=keep lead.level=-18dB }\nscene next { lead=two lead.level=-6dB lead.pan=0.5 lead.cutoff=1800Hz }\nscene quiet { lead=off }\n")
			if err := os.WriteFile(filepath.Join(root, "scenes.cicada"), scenes, 0600); err != nil {
				t.Fatal(err)
			}
			if test.lane != "" {
				entry = append(entry, []byte(test.lane)...)
				if err := os.WriteFile(s.path, entry, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := compileStudioSource(s.path, entry)
			if err != nil {
				t.Fatal(err)
			}
			take := fakeTimelineTake(t, s, 0, 6*seq.TicksPerBar, 576000, 0)
			result, _, err := s.vocalTakeResult(entry, take, nil)
			if err != nil {
				t.Fatal(err)
			}
			overrides := map[string][]byte{}
			for _, file := range result.Files {
				overrides[file.Path] = file.After
			}
			after, err := compileStudioSourceWithOverrides(s.path, result.Source, overrides)
			if err != nil {
				t.Fatal(err)
			}
			render := func(p *project.Project) []byte {
				cfg, err := schedule.CompileResident(p, root, 48000, 128)
				if err != nil {
					t.Fatal(err)
				}
				e, err := engine.New(cfg)
				if err != nil {
					t.Fatal(err)
				}
				e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
				left, right := make([]float32, 128), make([]float32, 128)
				out := make([]byte, 576000*8)
				for at := 0; at < 576000; at += 128 {
					if at == 96000 {
						e.Push(cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixGain), Arg0: math.Float32bits(-3)})
					}
					e.Render(left, right)
					for i := range left {
						binary.LittleEndian.PutUint32(out[(at+i)*8:], math.Float32bits(left[i]))
						binary.LittleEndian.PutUint32(out[(at+i)*8+4:], math.Float32bits(right[i]))
					}
				}
				return out
			}
			a, b := render(before), render(after)
			if !bytes.Equal(a, b) {
				for i := 0; i < len(a); i += 4 {
					if !bytes.Equal(a[i:i+4], b[i:i+4]) {
						t.Fatalf("backing PCM changed at frame %d channel %d: %.9g -> %.9g", i/8, i/4%2, math.Float32frombits(binary.LittleEndian.Uint32(a[i:])), math.Float32frombits(binary.LittleEndian.Uint32(b[i:])))
					}
				}
			}
			t.Logf("exact backing PCM %x", sha256.Sum256(a))
			if actual, _ := os.ReadFile(owner); !bytes.Contains(actual, []byte("song {")) {
				t.Fatal("preview wrote the source")
			}
		})
	}
}

func sceneControlsStudio(t *testing.T, dir string) (*studio, []byte, []byte, string) {
	t.Helper()
	s, entry, song, owner := songSourcesStudio(t, dir)
	scenes := []byte("scene first { lead=one vox=off lead.level=-18dB lead.pan=-0.5 }\nscene held { lead=keep lead.level=-18dB }\nscene next { lead=two lead.level=-6dB lead.pan=0.5 }\nscene quiet { lead=off }\n")
	if err := os.WriteFile(filepath.Join(dir, "scenes.cicada"), scenes, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "curves.cicada"), []byte("// keep curve notes\r\nautomate lead.level { @5.1.1 -9dB curve 2 @6.1.1 -14dB smooth recall @6.1.1 }\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "cicada.mod")
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, append(data, []byte("source \"curves.cicada\"\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	return s, entry, song, owner
}

func TestVocalSceneControlsCommitAllOwningFiles(t *testing.T) {
	isolateState(t)
	s, entry, song, owner := sceneControlsStudio(t, t.TempDir())
	root := filepath.Dir(s.path)
	curvesPath := filepath.Join(root, "curves.cicada")
	curves, _ := os.ReadFile(curvesPath)
	take := fakeTimelineTake(t, s, 0, 2*seq.TicksPerBar, 192000, .2)
	seen, err := project.ReadScoreRevision(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if r := studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "select", TakeID: take.ID, Revision: studioRevision(entry), SourceRevision: seen}); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	after, _ := os.ReadFile(s.path)
	afterSong, _ := os.ReadFile(owner)
	afterCurves, _ := os.ReadFile(curvesPath)
	if bytes.Equal(curves, afterCurves) || !bytes.Contains(afterCurves, []byte("@1.1.1 -18dB step")) || !bytes.Contains(afterSong, []byte("automate lead.pan")) {
		t.Fatal("conversion did not carry both new and existing curves")
	}
	selected, _ := s.takes.Get(take.ID)
	if len(selected.Files) != 2 {
		t.Fatalf("journal lost a source: %v", selected.Files)
	}
	for _, step := range []struct {
		route              string
		main, part, curves []byte
	}{{"/api/undo", entry, song, curves}, {"/api/redo", after, afterSong, afterCurves}} {
		current, _ := os.ReadFile(s.path)
		if r := studioCall(t, s.routes(), step.route, studioEdit{Revision: studioRevision(current)}); r.Code != 200 {
			t.Fatal(r.Code, r.Body.String())
		}
		main, _ := os.ReadFile(s.path)
		part, _ := os.ReadFile(owner)
		actualCurves, _ := os.ReadFile(curvesPath)
		if !bytes.Equal(main, step.main) || !bytes.Equal(part, step.part) || !bytes.Equal(actualCurves, step.curves) {
			t.Fatal("history did not restore all three edited sources", step.route)
		}
	}
	if err := s.shutdown(); err != nil {
		t.Fatal(err)
	}
	reopened, err := newStudio(s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.shutdown()
	current, _ := os.ReadFile(s.path)
	if _, err := compileStudioSource(s.path, current); err != nil {
		t.Fatal(err)
	}
}

func TestVocalSceneControlsCrashWithOutsideSongSave(t *testing.T) {
	isolateState(t)
	for _, stage := range []takejournal.Stage{takejournal.AuxiliaryFileSwapped, takejournal.AuxiliarySwapped} {
		t.Run(string(stage), func(t *testing.T) {
			dir := t.TempDir()
			child := exec.Command(os.Args[0], "-test.run=^TestSecondaryVocalCrashChild$")
			child.Env = append(os.Environ(), "CICADA_PRINT_CRASH_DIR="+dir, "CICADA_PRINT_CRASH_STAGE="+string(stage), "CICADA_PRINT_CRASH_CONTROLS=1")
			output, err := child.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 73 {
				t.Fatalf("crash boundary not reached: %v %s", err, output)
			}
			owner := filepath.Join(dir, "song.cicada")
			song, _ := os.ReadFile(owner)
			external := append(bytes.Clone(song), []byte("// outside song save\r\n")...)
			if err := os.WriteFile(owner, external, 0600); err != nil {
				t.Fatal(err)
			}
			s, err := newStudio(filepath.Join(dir, "main.cicada"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.shutdown()
			id, _ := os.ReadFile(filepath.Join(dir, "target"))
			take, err := s.takes.Get(string(id))
			if err != nil {
				t.Fatal(err)
			}
			if take.Stage != takejournal.Conflict || len(take.Files) != 2 {
				t.Fatal("conflicted prepared conversion was lost", take.Stage, len(take.Files))
			}
			for _, file := range take.Files {
				actual, _ := os.ReadFile(filepath.Join(dir, file.Path))
				expected := file.Before
				if stage == takejournal.AuxiliarySwapped {
					expected = file.After
				}
				if file.Path == "song.cicada" {
					expected = external
				}
				if !bytes.Equal(actual, expected) {
					t.Fatal("conflicted conversion left a partially applied source", file.Path)
				}
			}

		})
	}
}

func TestVocalSceneControlsKeepTrailingSilence(t *testing.T) {
	isolateState(t)
	s, entry, _, owner := songSourcesStudio(t, t.TempDir())
	if err := os.WriteFile(owner, []byte("song { first*2 held next quiet*2 }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	take := fakeTimelineTake(t, s, 0, 2*seq.TicksPerBar, 192000, 0)
	current := entry
	for attempt := 0; attempt < 2; attempt++ {
		result, _, err := s.vocalTakeResult(current, take, nil)
		if err != nil {
			t.Fatal(err)
		}
		overrides := map[string][]byte{}
		for _, file := range result.Files {
			overrides[file.Path] = file.After
		}
		p, err := compileStudioSourceWithOverrides(s.path, result.Source, overrides)
		if err != nil {
			t.Fatal(err)
		}
		var end int64
		for _, place := range p.Arrange.Placements {
			end = max(end, place.AtTick+place.LengthTicks)
		}
		if end != 6*seq.TicksPerBar {
			t.Fatalf("short phrase changed the six-bar song duration to %d ticks", end)
		}
		if err := os.WriteFile(s.path, result.Source, 0600); err != nil {
			t.Fatal(err)
		}
		for _, file := range result.Files {
			if err := os.WriteFile(file.Path, file.After, 0600); err != nil {
				t.Fatal(err)
			}
		}
		current = result.Source
	}
}

func TestCarriedAutomationEditingKeepsOwnerAndRejectsOutsideSaves(t *testing.T) {
	isolateState(t)
	for _, conflict := range []string{"none", "unseen", "mid-commit"} {
		t.Run(conflict, func(t *testing.T) {
			s, entry, _, _ := sceneControlsStudio(t, t.TempDir())
			take := fakeTimelineTake(t, s, 0, 2*seq.TicksPerBar, 192000, .2)
			if err := s.commitTake(take.ID, studioRevision(entry), nil); err != nil {
				t.Fatal(err)
			}
			current, _ := os.ReadFile(s.path)
			owner := filepath.Join(filepath.Dir(s.path), "curves.cicada")
			before, _ := os.ReadFile(owner)
			seen, err := project.ReadScoreRevision(s.path)
			if err != nil {
				t.Fatal(err)
			}
			external := append(bytes.Clone(before), []byte("// outside curve save\r\n")...)
			write := func() {
				if err := os.WriteFile(owner, external, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if conflict == "unseen" {
				write()
			} else if conflict == "mid-commit" {
				s.afterStage = write
			}
			r := studioCall(t, s.routes(), "/api/automation", studioEdit{Action: "automation-point-set", Path: "lead.level", Revision: studioRevision(current), SourceRevision: seen, Value: json.RawMessage(`{"oldAt":"@1.1.1","at":"@1.1.1","value":"-16dB","shape":"step"}`)})
			actual, _ := os.ReadFile(owner)
			main, _ := os.ReadFile(s.path)
			if !bytes.Equal(main, current) {
				t.Fatal("editing a curve changed entry bytes")
			}
			if conflict != "none" {
				if r.Code != 409 || !bytes.Equal(actual, external) {
					t.Fatal(r.Code, r.Body.String())
				}
				return
			}
			if r.Code != 200 || !bytes.Contains(actual, []byte("@1.1.1 -16dB step")) || !bytes.Contains(actual, []byte("recall @6.1.1")) {
				t.Fatal(r.Code, r.Body.String())
			}
			if r := studioCall(t, s.routes(), "/api/undo", studioEdit{Revision: studioRevision(main)}); r.Code != 200 {
				t.Fatal(r.Code, r.Body.String())
			}
			restored, _ := os.ReadFile(owner)
			if !bytes.Equal(restored, before) {
				t.Fatal("curve Undo lost notes or recall cues")
			}
		})
	}
}
