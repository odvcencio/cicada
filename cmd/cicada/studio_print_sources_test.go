package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/padsampler"
	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/project"
)

func printSourcesStudio(t *testing.T, dir string) (*studio, []byte, []byte, string) {
	t.Helper()
	cut := strings.Index(studioListeningScore, "arrange {")
	entry := []byte(strings.Replace(studioListeningScore[:cut], "cicada 2\n", "cicada 2\ntempo 120\ntrack vox audio {}\n", 1))
	part := []byte(strings.ReplaceAll(studioListeningScore[cut:], "\n", "\r\n"))
	partPath := filepath.Join(dir, "arrangement.cicada")
	for name, data := range map[string][]byte{"main.cicada": entry, "arrangement.cicada": part, "cicada.mod": []byte("project print-sources\ncicada 2\nentry \"main.cicada\"\nsource \"arrangement.cicada\"\n")} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := newStudio(filepath.Join(dir, "main.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	s.transport.audioNull = true
	if err := s.openTakes(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.shutdown() })
	return s, entry, part, partPath
}

func songSourcesStudio(t *testing.T, dir string) (*studio, []byte, []byte, string) {
	t.Helper()
	entry := []byte("cicada 2\ntempo 120\nseed 42\ntrack lead acid { level=-12dB }\ntrack vox audio {}\npattern one { c3 . g3 . }\npattern two { d3 . a3 . }\n")
	song := []byte("// keep song notes\r\nsong { first*2 held next quiet first } // keep ending\r\n")
	for name, data := range map[string][]byte{"main.cicada": entry, "song.cicada": song, "scenes.cicada": []byte("scene first { lead=one vox=off }\nscene held { lead=keep }\nscene next { lead=two }\nscene quiet { lead=off }\n"), "cicada.mod": []byte("project song-sources\ncicada 2\nentry \"main.cicada\"\nsource \"scenes.cicada\"\nsource \"song.cicada\"\n")} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := newStudio(filepath.Join(dir, "main.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	s.transport.audioNull = true
	if err := s.openTakes(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.shutdown() })
	return s, entry, song, filepath.Join(dir, "song.cicada")
}

func printSamplerCommand(t *testing.T, s *studio) samplerCommand {
	t.Helper()
	h := s.routes()
	if r := samplerCommandCall(t, h, samplerCommand{Action: "sample-placement", Placement: "pickup", Count: 4}); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	revision, bank, _, ids := samplerSnapshot(t, h)
	if r := studioCall(t, h, "/api/sampler/sound/"+ids[0], nil); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	seen, err := project.ReadScoreRevision(s.path)
	if err != nil {
		t.Fatal(err)
	}
	return samplerCommand{Action: "resample", Revision: revision, SourceRevision: seen, BankRevision: bank, Performance: padsampler.Performance{Frames: 96000, Hits: []padsampler.Hit{{Sound: ids[0], Pad: 1, Velocity: 1, Choke: 1}}}, NewName: "pad-print", Track: "sampler", At: "@3.1.1", Destination: 16, Place: true}
}

func TestSecondarySamplerPrintUndoRedoAndReopen(t *testing.T) {
	isolateState(t)
	s, entry, part, partPath := printSourcesStudio(t, t.TempDir())
	request := printSamplerCommand(t, s)
	store, err := s.padStore()
	if err != nil {
		t.Fatal(err)
	}
	_, beforeBank, _ := store.Read()
	if r := studioCall(t, s.routes(), "/api/sampler", request); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	after, _ := os.ReadFile(s.path)
	afterPart, _ := os.ReadFile(partPath)
	_, afterBank, _ := store.Read()
	if bytes.Contains(after, []byte("arrange {")) || !bytes.Contains(after, []byte("asset pad-print-audio")) || !bytes.Contains(afterPart, []byte("place pad-print sampler pad-print-clip")) {
		t.Fatal("print definitions and placement were not split correctly")
	}
	if bytes.Contains(bytes.ReplaceAll(afterPart, []byte("\r\n"), nil), []byte("\n")) {
		t.Fatal("print changed arrangement line endings")
	}
	for _, step := range []struct {
		route            string
		main, part, bank []byte
	}{{"/api/undo", entry, part, beforeBank}, {"/api/redo", after, afterPart, afterBank}} {
		current, _ := os.ReadFile(s.path)
		if r := studioCall(t, s.routes(), step.route, studioEdit{Revision: studioRevision(current)}); r.Code != 200 {
			t.Fatal(r.Code, r.Body.String())
		}
		main, _ := os.ReadFile(s.path)
		owned, _ := os.ReadFile(partPath)
		_, bank, _ := store.Read()
		if !bytes.Equal(main, step.main) || !bytes.Equal(owned, step.part) || !bytes.Equal(bank, step.bank) {
			t.Fatal(step.route, "did not restore all three files exactly")
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
	p, err := compileStudioSource(reopened.path, current)
	if err != nil || len(p.Clips) != 1 || len(p.Arrange.Placements) != 3 {
		t.Fatal("reopened print was incomplete", err)
	}
}

func TestSecondaryVocalPrintPreviewRecompUndoAndReopen(t *testing.T) {
	isolateState(t)
	s, entry, part, partPath := printSourcesStudio(t, t.TempDir())
	first := fakeTimelineTake(t, s, 0, 2*seq.TicksPerBar, 192000, .2)
	seen, err := project.ReadScoreRevision(s.path)
	if err != nil {
		t.Fatal(err)
	}
	request := studioEdit{Action: "listen", Revision: studioRevision(entry), SourceRevision: seen, TakeID: first.ID}
	if r := studioCall(t, s.routes(), "/api/takes", request); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	s.transport.stop()
	if main, _ := os.ReadFile(s.path); !bytes.Equal(main, entry) {
		t.Fatal("preview changed entry")
	}
	if owned, _ := os.ReadFile(partPath); !bytes.Equal(owned, part) {
		t.Fatal("preview wrote its arrangement candidate")
	}
	request.Action = "select"
	if r := studioCall(t, s.routes(), "/api/takes", request); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	after, _ := os.ReadFile(s.path)
	afterPart, _ := os.ReadFile(partPath)
	if bytes.Contains(after, []byte("arrange {")) || !bytes.Contains(after, []byte(vocalCompPrefix)) || !bytes.Contains(afterPart, []byte("place vocal-vox")) {
		t.Fatal("vocal metadata and placement were not committed together")
	}
	take, _ := s.takes.Get(first.ID)
	if len(take.Files) != 1 || take.Files[0].Path != "arrangement.cicada" {
		t.Fatal("journal lost the owning source")
	}
	for _, step := range []struct {
		route      string
		main, part []byte
	}{{"/api/undo", entry, part}, {"/api/redo", after, afterPart}} {
		current, _ := os.ReadFile(s.path)
		if r := studioCall(t, s.routes(), step.route, studioEdit{Revision: studioRevision(current)}); r.Code != 200 {
			t.Fatal(r.Code, r.Body.String())
		}
		main, _ := os.ReadFile(s.path)
		owned, _ := os.ReadFile(partPath)
		if !bytes.Equal(main, step.main) || !bytes.Equal(owned, step.part) {
			t.Fatal(step.route, "lost exact vocal sources")
		}
	}
	currentWithVocal, _ := os.ReadFile(s.path)
	if r := studioCall(t, s.routes(), "/api/mixer", studioEdit{Action: "vocal-chain", Track: "vox", Revision: studioRevision(currentWithVocal)}); r.Code != 200 {
		t.Fatal("vocal effects after print", r.Code, r.Body.String())
	}
	currentWithVocal, _ = os.ReadFile(s.path)
	if r := studioCall(t, s.routes(), "/api/automation", studioEdit{Action: "automation-point-set", Path: "vox.level", Revision: studioRevision(currentWithVocal), Value: json.RawMessage(`{"at":"@1.1.1","value":"-9dB","shape":"linear"}`)}); r.Code != 200 {
		t.Fatal("vocal automation after print", r.Code, r.Body.String())
	}
	second := fakeTimelineTake(t, s, seq.TicksPerBar, 2*seq.TicksPerBar, 192000, .6)
	current, _ := os.ReadFile(s.path)
	seen, _ = project.ReadScoreRevision(s.path)
	request = studioEdit{Action: "comp", Revision: studioRevision(current), SourceRevision: seen, TakeID: second.ID, Recording: &studioRecordingSettings{Start: "@2.1.1", End: "@3.1.1"}}
	if r := studioCall(t, s.routes(), "/api/takes", request); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	current, _ = os.ReadFile(s.path)
	owned, _ := os.ReadFile(partPath)
	comps, err := vocalComps(current)
	if err != nil || len(comps) != 1 || len(comps[0].Segments) != 2 || bytes.Count(owned, []byte("place vocal-vox")) != 1 {
		t.Fatal("recomp lost metadata or duplicated the placement", err)
	}
	if err := s.shutdown(); err != nil {
		t.Fatal(err)
	}
	reopened, err := newStudio(s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.shutdown()
	response := studioCall(t, reopened.routes(), "/api/takes", nil)
	var state struct{ Comps []vocalComp }
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil || len(state.Comps) != 1 || len(state.Comps[0].Segments) != 2 {
		t.Fatal("reopen lost comp review", err)
	}
}

func TestSecondaryPrintsRejectUnseenAndMidCommitWrites(t *testing.T) {
	isolateState(t)
	for _, kind := range []string{"sampler", "vocal"} {
		for _, mid := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/unseen", true: "/mid-commit"}[mid], func(t *testing.T) {
				s, entry, part, partPath := printSourcesStudio(t, t.TempDir())
				var payload any
				route := "/api/sampler"
				if kind == "sampler" {
					payload = printSamplerCommand(t, s)
				} else {
					take := fakeTimelineTake(t, s, 0, 2*seq.TicksPerBar, 192000, .2)
					seen, _ := project.ReadScoreRevision(s.path)
					payload = studioEdit{Action: "select", TakeID: take.ID, Revision: studioRevision(entry), SourceRevision: seen}
					route = "/api/takes"
				}
				store, _ := s.padStore()
				_, bankBefore, _ := store.Read()
				external := append(bytes.Clone(part), []byte("// another editor saved this\r\n")...)
				write := func() {
					if err := os.WriteFile(partPath, external, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if mid {
					s.afterStage = write
				} else {
					write()
				}
				if r := studioCall(t, s.routes(), route, payload); r.Code != http.StatusConflict {
					t.Fatal(r.Code, r.Body.String())
				}
				main, _ := os.ReadFile(s.path)
				owned, _ := os.ReadFile(partPath)
				_, bankAfter, _ := store.Read()
				if !bytes.Equal(main, entry) || !bytes.Equal(owned, external) || !bytes.Equal(bankBefore, bankAfter) || len(s.history.edits) != 0 {
					t.Fatal("refused print changed score, external file, bank or history")
				}
			})
		}
	}
}

func TestSecondaryVocalCrashChild(t *testing.T) {
	stage := os.Getenv("CICADA_PRINT_CRASH_STAGE")
	if stage == "" {
		return
	}
	dir := os.Getenv("CICADA_PRINT_CRASH_DIR")
	var s *studio
	if os.Getenv("CICADA_PRINT_CRASH_SONG") == "1" {
		s, _, _, _ = songSourcesStudio(t, dir)
	} else {
		s, _, _, _ = printSourcesStudio(t, dir)
	}
	target := fakeTimelineTake(t, s, 0, 2*seq.TicksPerBar, 192000, .2)
	if os.Getenv("CICADA_PRINT_CRASH_RETAINED") == "1" {
		other := fakeTimelineTake(t, s, 0, 2*seq.TicksPerBar, 192000, .6)
		for _, id := range []string{target.ID, other.ID} {
			source, _ := os.ReadFile(s.path)
			if err := s.commitTake(id, studioRevision(source), nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "target"), []byte(target.ID), 0600); err != nil {
		t.Fatal(err)
	}
	s.takes.Boundary = func(at takejournal.Stage) {
		if string(at) == stage {
			os.Exit(73)
		}
	}
	current, _ := os.ReadFile(s.path)
	if err := s.commitTake(target.ID, studioRevision(current), nil); err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash boundary not reached")
}

func TestSecondaryVocalCrashRecovery(t *testing.T) {
	isolateState(t)
	for _, sceneSong := range []bool{false, true} {
		for _, retained := range []bool{false, true} {
			for _, stage := range []takejournal.Stage{takejournal.Prepared, takejournal.SourceStaged, takejournal.SourceSwapped, takejournal.AuxiliarySwapped, takejournal.Source, takejournal.Committed} {
				for _, external := range []bool{false, true} {
					if external && stage != takejournal.SourceSwapped && stage != takejournal.AuxiliarySwapped {
						continue
					}
					t.Run(fmt.Sprintf("song=%v/retained=%v/%s/external=%v", sceneSong, retained, stage, external), func(t *testing.T) {
						t.Parallel()
						dir := t.TempDir()
						child := exec.Command(os.Args[0], "-test.run=^TestSecondaryVocalCrashChild$")
						child.Env = append(os.Environ(), "CICADA_PRINT_CRASH_DIR="+dir, "CICADA_PRINT_CRASH_STAGE="+string(stage))
						if sceneSong {
							child.Env = append(child.Env, "CICADA_PRINT_CRASH_SONG=1")
						}
						if retained {
							child.Env = append(child.Env, "CICADA_PRINT_CRASH_RETAINED=1")
						}
						output, err := child.CombinedOutput()
						var exit *exec.ExitError
						if !errors.As(err, &exit) || exit.ExitCode() != 73 {
							t.Fatalf("child did not reach boundary: %v\n%s", err, output)
						}
						partName := "arrangement.cicada"
						if sceneSong {
							partName = "song.cicada"
						}
						partPath := filepath.Join(dir, partName)
						part, _ := os.ReadFile(partPath)
						if external {
							part = append(part, []byte("// external arrangement edit\r\n")...)
							if err := os.WriteFile(partPath, part, 0600); err != nil {
								t.Fatal(err)
							}
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
						owned, _ := os.ReadFile(partPath)
						if external {
							current, _ := os.ReadFile(s.path)
							want := take.After
							if stage == takejournal.SourceSwapped {
								want = take.Before
							}
							if studioRevision(current) != want {
								t.Fatal("recovery did not preserve the compatible confirmed entry")
							}
							if take.Stage != takejournal.Conflict || !bytes.Equal(owned, part) || len(take.Files) == 0 {
								t.Fatal("external edit or pending comp was lost")
							}
						} else {
							current, _ := os.ReadFile(s.path)
							comps, err := vocalComps(current)
							if take.Stage != takejournal.Committed || err != nil || len(comps) != 1 || comps[0].Segments[0].Take != string(id) || !bytes.Contains(owned, []byte("place vocal-vox")) {
								t.Fatal("comp did not recover together", take.Stage, err)
							}
						}
					})
				}
			}
		}
	}
}

func TestSongSourcesVocalPreviewHistoryAudioAndReopen(t *testing.T) {
	isolateState(t)
	s, entry, song, owner := songSourcesStudio(t, t.TempDir())
	scenesPath := filepath.Join(filepath.Dir(s.path), "scenes.cicada")
	scenes, _ := os.ReadFile(scenesPath)
	baseline, err := compileStudioSource(s.path, entry)
	if err != nil {
		t.Fatal(err)
	}
	take := fakeTimelineTake(t, s, 0, 6*seq.TicksPerBar, 576000, 0)
	rawPath := filepath.Join(filepath.Dir(s.path), take.Asset.Path)
	raw, _ := os.ReadFile(rawPath)
	rawHash := sha256.Sum256(raw)
	seen, err := project.ReadScoreRevision(s.path)
	if err != nil {
		t.Fatal(err)
	}
	request := studioEdit{Action: "listen", Revision: studioRevision(entry), SourceRevision: seen, TakeID: take.ID}
	if r := studioCall(t, s.routes(), "/api/takes", request); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	s.transport.stop()
	if actual, _ := os.ReadFile(s.path); !bytes.Equal(actual, entry) {
		t.Fatal("preview changed entry")
	}
	if actual, _ := os.ReadFile(owner); !bytes.Equal(actual, song) {
		t.Fatal("preview converted the saved song")
	}
	request.Action = "select"
	if r := studioCall(t, s.routes(), "/api/takes", request); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	after, _ := os.ReadFile(s.path)
	afterSong, _ := os.ReadFile(owner)
	if !bytes.HasPrefix(after, entry) || bytes.Contains(after, []byte("arrange {")) || !bytes.Contains(afterSong, []byte("place vocal-vox")) || bytes.Contains(afterSong, []byte("song {")) {
		t.Fatal("conversion changed the wrong source")
	}
	printed, err := compileStudioSource(s.path, after)
	if err != nil {
		t.Fatal(err)
	}
	render := func(p *project.Project) [32]byte {
		cfg, err := schedule.CompileResident(p, filepath.Dir(s.path), 48000, 128)
		if err != nil {
			t.Fatal(err)
		}
		e, err := engine.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
		left, right := make([]float32, 128), make([]float32, 128)
		buffer := make([]byte, 1024)
		h := sha256.New()
		for at := 0; at < 576000; at += 128 {
			e.Render(left, right)
			for i := range left {
				binary.LittleEndian.PutUint32(buffer[i*8:], math.Float32bits(left[i]))
				binary.LittleEndian.PutUint32(buffer[i*8+4:], math.Float32bits(right[i]))
			}
			h.Write(buffer)
		}
		var sum [32]byte
		copy(sum[:], h.Sum(nil))
		return sum
	}
	beforeAudio, afterAudio := render(baseline), render(printed)
	if beforeAudio != afterAudio {
		t.Fatalf("first silent comp changed backing PCM: %x -> %x", beforeAudio, afterAudio)
	}
	t.Logf("six-bar backing PCM remains exact: %x", afterAudio)
	for _, step := range []struct {
		route      string
		main, part []byte
	}{{"/api/undo", entry, song}, {"/api/redo", after, afterSong}} {
		current, _ := os.ReadFile(s.path)
		if r := studioCall(t, s.routes(), step.route, studioEdit{Revision: studioRevision(current)}); r.Code != 200 {
			t.Fatal(r.Code, r.Body.String())
		}
		actualMain, _ := os.ReadFile(s.path)
		actualPart, _ := os.ReadFile(owner)
		if !bytes.Equal(actualMain, step.main) || !bytes.Equal(actualPart, step.part) {
			t.Fatal("history did not restore both source buffers", step.route)
		}
	}
	if actual, _ := os.ReadFile(scenesPath); !bytes.Equal(actual, scenes) {
		t.Fatal("conversion rewrote scene definitions")
	}
	if actual, _ := os.ReadFile(rawPath); sha256.Sum256(actual) != rawHash {
		t.Fatal("raw take changed")
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
	p, err := compileStudioSource(s.path, current)
	if err != nil || render(p) != afterAudio {
		t.Fatal("reopened backing changed", err)
	}
}

func TestSongSourcesVocalRejectsUnseenAndMidCommitSaves(t *testing.T) {
	isolateState(t)
	for _, mid := range []bool{false, true} {
		t.Run(fmt.Sprintf("mid=%v", mid), func(t *testing.T) {
			s, entry, song, owner := songSourcesStudio(t, t.TempDir())
			take := fakeTimelineTake(t, s, 0, 2*seq.TicksPerBar, 192000, .2)
			seen, err := project.ReadScoreRevision(s.path)
			if err != nil {
				t.Fatal(err)
			}
			external := append(bytes.Clone(song), []byte("// another editor saved this\r\n")...)
			write := func() {
				if err := os.WriteFile(owner, external, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mid {
				s.afterStage = write
			} else {
				write()
			}
			if r := studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "select", TakeID: take.ID, Revision: studioRevision(entry), SourceRevision: seen}); r.Code != 409 {
				t.Fatal(r.Code, r.Body.String())
			}
			current, _ := os.ReadFile(s.path)
			actual, _ := os.ReadFile(owner)
			if !bytes.Equal(current, entry) || !bytes.Equal(actual, external) || len(s.history.edits) != 0 {
				t.Fatal("conflicted conversion changed sources or history")
			}
		})
	}
}
