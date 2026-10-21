package main

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/kernel/cmd"
)

func patternPreviewFixture(t *testing.T) (*studio, []byte) {
	t.Helper()
	s := newTakeStudio(t, t.TempDir())
	call := func(edit studioEdit) {
		source, err := os.ReadFile(s.path)
		if err != nil {
			t.Fatal(err)
		}
		edit.Revision = studioRevision(source)
		endpoint := "/api/pattern"
		if edit.Action == "add-preset" {
			endpoint = "/api/instrument"
		}
		if r := studioCall(t, s.routes(), endpoint, edit); r.Code != 200 {
			t.Fatalf("fixture: %d %s", r.Code, r.Body)
		}
	}
	call(studioEdit{Action: "add-preset", Pattern: "silk-pluck", NewName: "pluck", Track: "keys"})
	call(studioEdit{Action: "create", NewName: "idea", Track: "keys", Length: 16, Starter: "arpeggio"})
	source, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	s.transport.audioNull = true
	return s, source
}

func TestPatternPreviewPreservesPausedSongAndRetiresChangedSource(t *testing.T) {
	s, source := patternPreviewFixture(t)
	tr := s.transport
	if err := tr.start(); err != nil {
		t.Fatal(err)
	}
	tr.pause()
	stream, position, options, last := tr.stream, tr.stream.Position(), tr.audioOptions, tr.last
	lease := soundTestLease(t, s)
	request := soundPreviewRequest{Action: "pattern", Lease: lease, Sequence: 1, Pattern: "idea", Track: "keys", Revision: studioRevision(source)}
	if r := studioCall(t, s.routes(), "/api/sound-preview", request); r.Code != 200 {
		t.Fatalf("preview: %d %s", r.Code, r.Body)
	}
	if !tr.soundPreviewState(lease).Active || tr.playing || !tr.previewLoop || !tr.previewOutputOnly {
		t.Fatal("part preview took over the song or input routing")
	}
	if got, _ := os.ReadFile(s.path); !bytes.Equal(got, source) {
		t.Fatal("audition wrote the score")
	}
	if tr.stream != stream || !reflect.DeepEqual(stream.Position(), position) || tr.audioOptions != options || tr.last != last {
		t.Fatal("audition changed the paused song, position or audio choices")
	}
	// Heartbeats keep this loop alive; a missing client still releases output.
	tr.mu.Lock()
	tr.previewTimer.Reset(150 * time.Millisecond)
	tr.mu.Unlock()
	if !s.patternPreviewState(lease).Active {
		t.Fatal("heartbeat lost the loop")
	}
	time.Sleep(250 * time.Millisecond)
	tr.mu.Lock()
	active := tr.preview != nil
	tr.mu.Unlock()
	if !active {
		t.Fatal("heartbeat did not retain its loop")
	}
	changed := append(bytes.Clone(source), []byte("\n// saved edit\n")...)
	if err := os.WriteFile(s.path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if result := s.patternPreviewState(lease); result.Active || result.Error == "" {
		t.Fatal("stale audio kept playing after the saved score changed")
	}
	request.Sequence = 2
	if r := studioCall(t, s.routes(), "/api/sound-preview", request); r.Code != 409 || !bytes.Contains(r.Body.Bytes(), []byte("saved score changed")) {
		t.Fatal("stale loop request was accepted")
	}
	request.Sequence, request.Revision = 3, studioRevision(changed)
	if r := studioCall(t, s.routes(), "/api/sound-preview", request); r.Code != 200 {
		t.Fatalf("changed preview: %d %s", r.Code, r.Body)
	}
	tr.mu.Lock()
	tr.previewTimer.Reset(100 * time.Millisecond)
	tr.mu.Unlock()
	time.Sleep(250 * time.Millisecond)
	tr.mu.Lock()
	active, hasAudio := tr.preview != nil, tr.audio != nil
	tr.mu.Unlock()
	if active || hasAudio || tr.stream != stream || !reflect.DeepEqual(stream.Position(), position) {
		t.Fatal("abandoned preview did not release output and preserve the paused song")
	}
}

func TestPatternPreviewActuallyRepeatsTheUnassignedPart(t *testing.T) {
	s, source := patternPreviewFixture(t)
	p, _, err := patternPreviewProject(s.path, soundPreviewRequest{Pattern: "idea", Track: "keys", Revision: studioRevision(source)})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Arrange.Placements) != 1 || p.Arrange.Placements[0].Track != "keys" || p.Arrange.Placements[0].Content != "idea" {
		t.Fatal("preview did not isolate the chosen part and sound")
	}
	prepared, err := compileLiveProjectLoopAtRate(s.path, p, 48000, true)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Engine.Close()
	if !prepared.Engine.Push(cmd.Command{Op: cmd.OpPlay, Track: 255}) {
		t.Fatal("preview did not accept Play")
	}
	var left, right [256]float32
	peak, late := 0.0, 0.0
	var wav []byte
	if os.Getenv("CICADA_PREVIEW_PROOF_DIR") != "" {
		wav = testwav.Bytes(48000, 2, 24, 48000*8, 1)
	}
	for frame := 0; frame < 48000*8; frame += len(left) {
		prepared.Engine.Render(left[:], right[:])
		for channel, values := range [][]float32{left[:], right[:]} {
			for index, v := range values {
				x := math.Abs(float64(v))
				if math.IsInf(x, 0) || math.IsNaN(x) {
					t.Fatal("non-finite preview")
				}
				peak = max(peak, x)
				if frame > 48000*6 {
					late = max(late, x)
				}
				if wav != nil {
					sample := int32(math.Round(float64(v) * 8388607))
					offset := 44 + ((frame+index)*2+channel)*3
					wav[offset], wav[offset+1], wav[offset+2] = byte(sample), byte(sample>>8), byte(sample>>16)
				}
			}
		}
	}
	if peak < .01 || late < .01 || peak > 1 {
		t.Fatalf("loop is silent, clipped or stopped after its first phrase: peak=%g late=%g", peak, late)
	}
	t.Logf("native_loop_frames=%d peak=%.6f peak_after_six_seconds=%.6f", 48000*8, peak, late)
	if dir := os.Getenv("CICADA_PREVIEW_PROOF_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "isolated-part-loop.wav"), wav, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for index := range p.Tracks {
		if p.Tracks[index].ID == "keys" {
			p.Tracks[index].Mixer.Mute = true
		}
	}
	muted, err := compileLiveProjectLoopAtRate(s.path, p, 48000, true)
	if err != nil {
		t.Fatal(err)
	}
	defer muted.Engine.Close()
	if !muted.Engine.Push(cmd.Command{Op: cmd.OpPlay, Track: 255}) {
		t.Fatal("muted preview did not accept Play")
	}
	mutedPeak := 0.0
	for frame := 0; frame < 48000*2; frame += len(left) {
		muted.Engine.Render(left[:], right[:])
		for index := range left {
			mutedPeak = max(mutedPeak, math.Abs(float64(left[index])), math.Abs(float64(right[index])))
		}
	}
	if mutedPeak > .000001 {
		t.Fatalf("unselected song tracks leaked into the isolated preview: %g", mutedPeak)
	}
	t.Logf("selected_track_muted_peak=%.9f", mutedPeak)
	for _, track := range []string{"vox", "missing"} {
		if _, _, err := patternPreviewProject(s.path, soundPreviewRequest{Pattern: "idea", Track: track, Revision: studioRevision(source)}); err == nil {
			t.Fatalf("incompatible track %s accepted", track)
		}
	}
}

func TestPatternPreviewStopsWhenAudioRoutingOrProjectSourcesChange(t *testing.T) {
	s, source := patternPreviewFixture(t)
	lease := soundTestLease(t, s)
	request := soundPreviewRequest{Action: "pattern", Lease: lease, Sequence: 1, Pattern: "idea", Track: "keys", Revision: studioRevision(source)}
	if r := studioCall(t, s.routes(), "/api/sound-preview", request); r.Code != 200 {
		t.Fatal(r.Body)
	}
	options := s.transport.audioOptions
	options.PeriodFrames = 512
	if err := s.transport.configureAudio(options); err != nil {
		t.Fatal(err)
	}
	if s.transport.soundPreviewState(lease).Active || s.transport.preview != nil {
		t.Fatal("changing the device route left an abandoned preview alive")
	}
	request.Sequence = 2
	if r := studioCall(t, s.routes(), "/api/sound-preview", request); r.Code != 200 {
		t.Fatal(r.Body)
	}
	// A legacy preview may fail during preparation. It must not leave the
	// previous loop unowned and running while the new request has no result.
	s.transport.beginPreview()
	if s.transport.preview != nil {
		t.Fatal("legacy preview preparation left an orphaned part loop")
	}
	path := copyStudioProject(t)
	projectStudio, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = projectStudio.shutdown() })
	projectStudio.transport.audioNull = true
	rootSource, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lease = soundTestLease(t, projectStudio)
	request = soundPreviewRequest{Action: "pattern", Lease: lease, Sequence: 1, Pattern: "riff", Track: "lead", Revision: studioRevision(rootSource)}
	if r := studioCall(t, projectStudio.routes(), "/api/sound-preview", request); r.Code != 200 {
		t.Fatal(r.Body)
	}
	part := filepath.Join(filepath.Dir(path), "parts/patterns.cicada")
	data, err := os.ReadFile(part)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(part, append(data, []byte("\n// saved part edit\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if result := projectStudio.patternPreviewState(lease); result.Active || result.Error == "" {
		t.Fatal("a changed project source did not retire the old loop")
	}
}

func TestPatternPreviewResolvesUnassignedRegisterAndPreservesPlayedGestures(t *testing.T) {
	s, source := patternPreviewFixture(t)
	source = bytes.Replace(source, []byte("track keys pluck {"), []byte("track keys pluck { octave = 4 "), 1)
	if err := os.WriteFile(s.path, source, 0600); err != nil {
		t.Fatal(err)
	}
	request := soundPreviewRequest{Pattern: "idea", Track: "keys", Revision: studioRevision(source)}
	p, _, err := patternPreviewProject(s.path, request)
	if err != nil {
		t.Fatal(err)
	}
	for _, pattern := range p.Patterns {
		if pattern.ID == "idea" && int(pattern.Data[0].Note) != 60+int(p.Key.Root) {
			t.Fatalf("library-only part ignored selected track register: %d", pattern.Data[0].Note)
		}
	}
	source = bytes.Replace(source, []byte("1 . 3 ."), []byte("c4 . 3 ."), 1)
	source = append(source, []byte("\npattern free performance length=7680ticks {\n note 60 at=17ticks length=703ticks velocity=82 {\n gesture at=0ticks bend=0ct pressure=0.2 timbre=0.5;\n gesture at=123ticks bend=-30ct pressure=0.7 timbre=0.8;\n }\n}\n")...)
	if err := os.WriteFile(s.path, source, 0600); err != nil {
		t.Fatal(err)
	}
	request.Revision = studioRevision(source)
	p, _, err = patternPreviewProject(s.path, request)
	if err != nil {
		t.Fatal(err)
	}
	for _, pattern := range p.Patterns {
		if pattern.ID == "idea" && pattern.Data[0].Note != 60 {
			t.Fatal("absolute pitch changed with the preview register")
		}
	}
	original, err := compileStudioSource(s.path, source)
	if err != nil {
		t.Fatal(err)
	}
	request.Pattern = "free"
	preview, _, err := patternPreviewProject(s.path, request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original.Patterns[len(original.Patterns)-1].Performance, preview.Patterns[len(preview.Patterns)-1].Performance) || preview.Arrange.Placements[0].LengthTicks != 7680 {
		t.Fatal("preview changed recorded timing or gestures")
	}
	if current, _ := os.ReadFile(s.path); !bytes.Equal(current, source) {
		t.Fatal("retargeting wrote the original score")
	}
	if strings.Contains(string(source), "keys = idea") {
		t.Fatal("test fixture accidentally assigned the library-only part")
	}
}
