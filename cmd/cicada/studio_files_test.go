package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
)

func copyStudioProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"cicada.mod", "main.cicada", "parts/voices.cicada", "parts/patterns.cicada"} {
		data, err := os.ReadFile(filepath.Join("../../examples/studio-project", name))
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, name)
		if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(root, "main.cicada")
}
func TestStudioMultiFileSession(t *testing.T) {
	path := copyStudioProject(t)
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.shutdown()
	h := s.routes()
	before, _ := os.ReadFile(path)
	part := filepath.Join(filepath.Dir(path), "parts/patterns.cicada")
	original, _ := os.ReadFile(part)
	listing := studioCall(t, h, "/api/files", nil)
	var state struct {
		Files    []studioProjectFile
		Revision string `json:"projectRevision"`
	}
	if listing.Code != 200 || json.Unmarshal(listing.Body.Bytes(), &state) != nil || len(state.Files) != 3 {
		t.Fatal(listing.Body.String())
	}
	edited := strings.Replace(string(original), "1 . 5 .", "2 . 6 .", 1)
	checked := studioCall(t, h, "/api/files", studioEdit{File: "parts/patterns.cicada", Action: "check", Revision: studioRevision(original), Source: "pattern riff { nonsense }"})
	if !strings.Contains(checked.Body.String(), `"valid":false`) || !strings.Contains(checked.Body.String(), "patterns.cicada") {
		t.Fatal("missing per-file diagnostic", checked.Body.String())
	}
	saved := studioCall(t, h, "/api/files", studioEdit{File: "parts/patterns.cicada", Revision: studioRevision(original), Source: edited})
	if saved.Code != 200 {
		t.Fatal(saved.Body.String())
	}
	main, _ := os.ReadFile(path)
	if !bytes.Equal(main, before) {
		t.Fatal("part save changed entry")
	}
	content, _ := os.ReadFile(part)
	if string(content) != edited {
		t.Fatal("part was not saved")
	}
	if response := studioCall(t, h, "/api/files", studioEdit{File: "../other.cicada", Revision: "x", Source: edited}); response.Code != 400 {
		t.Fatal("escaping file admitted")
	}
	if response := studioCall(t, h, "/api/files", studioEdit{File: "parts/patterns.cicada", Revision: studioRevision(original), Source: edited}); response.Code != 409 {
		t.Fatal("stale save admitted")
	}
	studioRenderParity(t, path)
	s.transport.audioNull = true
	s.transport.audioBackend = "null"
	if err = s.transport.start(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(part, original, 0644); err != nil {
		t.Fatal(err)
	}
	s.transport.poll()
	if !s.transport.snapshot().Pending {
		t.Fatal("external part change was not queued")
	}
	after := studioCall(t, h, "/api/files", nil)
	var external struct {
		Revision string `json:"projectRevision"`
	}
	json.Unmarshal(after.Body.Bytes(), &external)
	if external.Revision == state.Revision { // Return to original is intentionally the original fingerprint.
		if string(content) == string(original) {
			t.Fatal("fixture did not change")
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".cicada-studio-") || strings.HasSuffix(entry.Name(), ".revision") {
			t.Fatal("revision litter in project")
		}
	}
}

// Compare live native and serialized worklet engines with the actual offline
// float WAV, including the safety limiter's latency. All three use project files.
func studioRenderParity(t *testing.T, path string) {
	t.Helper()
	score, ds, err := project.LoadScore(path, nil)
	if err != nil || hasDiagnosticErrors(ds) {
		t.Fatal(err, ds)
	}
	p, ds := project.FromScore(score)
	if p == nil || hasDiagnosticErrors(ds) {
		t.Fatal(ds)
	}
	root, err := studioProjectRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := schedule.Compile(p, root, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := compileLiveProjectAtRate(path, p, 48000)
	if err != nil {
		t.Fatal(err)
	}
	live := prepared.Engine
	if err != nil {
		t.Fatal(err)
	}
	cfg.LoopSong = true
	image, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := kernelimage.Decode(image, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	worklet, err := engine.New(decoded)
	if err != nil {
		t.Fatal(err)
	}
	var wav bytes.Buffer
	report, err := render.WAV(score, render.Options{AssetRoot: root, SampleRate: 48000, Bits: 32, Block: 128}, &wav)
	if err != nil {
		t.Fatal(err)
	}
	live.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	worklet.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	latency := int64(live.LatencyFrames())
	var l, r, wl, wr [128]float32
	nonzero := false
	var previewPCM bytes.Buffer
	for frame := int64(0); frame < report.Frames+latency; frame += 128 {
		if frame == report.Frames {
			live.Push(cmd.Command{Op: cmd.OpStop, Track: 0xff})
			worklet.Push(cmd.Command{Op: cmd.OpStop, Track: 0xff})
		}
		live.Render(l[:], r[:])
		worklet.Render(wl[:], wr[:])
		if frame < 4096 {
			for i := range l {
				binary.Write(&previewPCM, binary.LittleEndian, l[i])
				binary.Write(&previewPCM, binary.LittleEndian, r[i])
			}
		}
		for i := range l {
			if l[i] != wl[i] || r[i] != wr[i] {
				t.Fatalf("native/image drift at %d", frame+int64(i))
			}
			sourceFrame := frame + int64(i) - latency
			if sourceFrame < 0 || sourceFrame >= report.Frames {
				continue
			}
			at := 44 + int(sourceFrame)*8
			left, right := math.Float32frombits(binary.LittleEndian.Uint32(wav.Bytes()[at:])), math.Float32frombits(binary.LittleEndian.Uint32(wav.Bytes()[at+4:]))
			if math.Abs(float64(l[i]-left)) > 2e-7 || math.Abs(float64(r[i]-right)) > 2e-7 {
				t.Fatalf("live/render drift at %d: %g/%g", sourceFrame, l[i], left)
			}
			nonzero = nonzero || l[i] != 0 || r[i] != 0
		}
	}
	if prefix := os.Getenv("CICADA_STUDIO_PARITY_PREFIX"); prefix != "" && len(p.Samplers) > 0 {
		if err := os.WriteFile(prefix+".image", image, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(prefix+".pcm", previewPCM.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if !nonzero {
		t.Fatal("silent project")
	}
	if allocs := testing.AllocsPerRun(100, func() { live.Render(l[:], r[:]) }); allocs != 0 {
		t.Fatalf("audio callback allocations %g", allocs)
	}
}
