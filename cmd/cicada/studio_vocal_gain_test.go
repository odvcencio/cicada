package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/project"
)

func vocalGainFixture(t *testing.T) (*studio, []byte, takejournal.Take) {
	t.Helper()
	s := newTakeStudio(t, t.TempDir())
	source := []byte(strings.Replace(strings.Replace(audioTakeScore, "cicada 2\n", "cicada 2\ntempo 120\n", 1), "song { main }", "song { main*2 }", 1))
	if err := os.WriteFile(s.path, source, 0600); err != nil {
		t.Fatal(err)
	}
	return s, source, fakeTimelineTake(t, s, 0, 2*seq.TicksPerBar, 192000, .125)
}

func vocalGainPrint(t *testing.T, s *studio, source []byte) (project.Asset, []byte) {
	t.Helper()
	p, err := compileStudioSource(s.path, source)
	if err != nil || len(p.Assets) != 1 {
		t.Fatalf("gain comp project: %+v %v", p, err)
	}
	data, err := os.ReadFile(takeRoot(s.path) + "/" + p.Assets[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	return p.Assets[0], data
}

func vocalGainSample(data []byte, frame int64) float32 {
	return math.Float32frombits(binary.LittleEndian.Uint32(data[44+frame*4:]))
}

func TestVocalGainLegacyUnityAndExactPhrasePrints(t *testing.T) {
	s, source, take := vocalGainFixture(t)
	rawBefore, err := os.ReadFile(takeRoot(s.path) + "/" + take.Asset.Path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := []byte(vocalCompPrefix + `{"track":"vox","tempoMilli":120000,"segments":[{"take":"old","startTick":0,"endTick":7680}],"asset":"old-print"}` + "\n")
	comps, err := vocalComps(legacy)
	if err != nil || len(comps) != 1 || comps[0].Segments[0].GainDB != 0 {
		t.Fatalf("legacy gain must default to unity: %+v %v", comps, err)
	}
	if err = s.commitTake(take.ID, studioRevision(source), nil); err != nil {
		t.Fatal(err)
	}
	unity, _ := os.ReadFile(s.path)
	unityAsset, unityPCM := vocalGainPrint(t, s, unity)
	if bytes.Contains(unity, []byte(`"gainDB"`)) || vocalGainSample(unityPCM, 24000) != .125 || vocalGainSample(unityPCM, 0) != 0 || vocalGainSample(unityPCM, 191999) != 0 {
		t.Fatal("legacy unity/fades changed")
	}
	boost := 20 * math.Log10(2)
	boosted, err := s.vocalTakeSource(unity, take, &studioRecordingSettings{Start: "@1.3.1", End: "@2.2.1", GainDB: &boost})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.commitTake(take.ID, studioRevision(unity), boosted); err != nil {
		t.Fatal(err)
	}
	boostedAsset, boostedPCM := vocalGainPrint(t, s, boosted)
	if vocalGainSample(boostedPCM, 24000) != .125 || vocalGainSample(boostedPCM, 72000) != .25 || vocalGainSample(boostedPCM, 150000) != .125 {
		t.Fatal("boost escaped the selected phrase or changed its gain")
	}
	if vocalGainSample(boostedPCM, 48000) != 0 || vocalGainSample(boostedPCM, 48001) != .25*float32(1./240) || vocalGainSample(boostedPCM, 119999) != 0 {
		t.Fatal("boost changed the existing de-click fades")
	}
	attenuation := -boost
	quiet, err := s.vocalTakeSource(boosted, take, &studioRecordingSettings{Start: "@1.4.1", End: "@2.1.1", GainDB: &attenuation})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.commitTake(take.ID, studioRevision(boosted), quiet); err != nil {
		t.Fatal(err)
	}
	_, quietPCM := vocalGainPrint(t, s, quiet)
	for frame, want := range map[int64]float32{24000: .125, 60000: .25, 84000: .0625, 108000: .25, 150000: .125} {
		if got := vocalGainSample(quietPCM, frame); got != want {
			t.Fatalf("gain at frame %d = %g, want %g", frame, got, want)
		}
	}
	comps, err = vocalComps(quiet)
	want := []vocalSegment{{Take: take.ID, Start: 0, End: 1920}, {Take: take.ID, Start: 1920, End: 2880, GainDB: boost}, {Take: take.ID, Start: 2880, End: 3840, GainDB: attenuation}, {Take: take.ID, Start: 3840, End: 4800, GainDB: boost}, {Take: take.ID, Start: 4800, End: 7680}}
	if err != nil || len(comps) != 1 || !reflect.DeepEqual(comps[0].Segments, want) {
		t.Fatalf("gain edits did not preserve neighboring choices: %+v %v", comps, err)
	}
	for _, old := range []struct {
		asset project.Asset
		data  []byte
	}{{unityAsset, unityPCM}, {boostedAsset, boostedPCM}} {
		current, err := os.ReadFile(takeRoot(s.path) + "/" + old.asset.Path)
		if err != nil || !bytes.Equal(current, old.data) {
			t.Fatal("gain edit changed an immutable prior print")
		}
	}
	for _, previous := range [][]byte{boosted, unity} {
		current, _ := os.ReadFile(s.path)
		response := studioCall(t, s.routes(), "/api/undo", studioEdit{Revision: studioRevision(current)})
		if response.Code != http.StatusOK {
			t.Fatalf("Undo gain: %d %s", response.Code, response.Body)
		}
		restored, _ := os.ReadFile(s.path)
		if !bytes.Equal(restored, previous) {
			t.Fatal("Undo lost the original gain/source choice")
		}
		p, err := compileStudioSource(s.path, restored)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = schedule.Compile(p, takeRoot(s.path), 48000, 128); err != nil {
			t.Fatalf("Undo lost a playable prior vocal print: %v", err)
		}
	}
	rawAfter, _ := os.ReadFile(takeRoot(s.path) + "/" + take.Asset.Path)
	if !bytes.Equal(rawBefore, rawAfter) || len(s.takes.Takes()) != 1 {
		t.Fatal("phrase gain changed the retained raw take")
	}
}

func TestVocalGainMergesOnlyEqualAdjacentChoices(t *testing.T) {
	old := []vocalSegment{{Take: "a", Start: 0, End: 100, GainDB: -3}}
	got := replaceVocalSegment(old, vocalSegment{Take: "a", Start: 25, End: 75, GainDB: 6})
	want := []vocalSegment{{Take: "a", Start: 0, End: 25, GainDB: -3}, {Take: "a", Start: 25, End: 75, GainDB: 6}, {Take: "a", Start: 75, End: 100, GainDB: -3}}
	if !reflect.DeepEqual(got, want) || old[0].End != 100 || old[0].GainDB != -3 {
		t.Fatalf("different gains merged or modified prior choice: %+v", got)
	}
	got = replaceVocalSegment(got, vocalSegment{Take: "a", Start: 25, End: 75, GainDB: -3})
	if !reflect.DeepEqual(got, old) {
		t.Fatalf("equal gains failed to merge: %+v", got)
	}
}

func TestVocalGainRejectsInvalidRequestsAndMetadata(t *testing.T) {
	s, source, take := vocalGainFixture(t)
	for _, gain := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -24.01, 18.01} {
		if _, err := s.vocalTakeSource(source, take, &studioRecordingSettings{GainDB: &gain}); err == nil {
			t.Fatalf("accepted invalid phrase gain %v", gain)
		}
		comp := vocalComp{Track: take.Track, Tempo: take.Timeline.TempoMilli, Segments: []vocalSegment{{Take: take.ID, Start: 0, End: 7680, GainDB: gain}}}
		if _, err := s.printVocalComp(comp, 48000); err == nil {
			t.Fatalf("printed invalid phrase gain %v", gain)
		}
		metadata := fmt.Sprintf("%s{\"track\":\"vox\",\"tempoMilli\":120000,\"segments\":[{\"take\":\"old\",\"startTick\":0,\"endTick\":7680,\"gainDB\":%g}]}\n", vocalCompPrefix, gain)
		if _, err := vocalComps([]byte(metadata)); err == nil {
			t.Fatalf("accepted invalid persisted phrase gain %v", gain)
		}
	}
	for _, gain := range []float64{-24, 0, 18} {
		if _, _, err := vocalPhraseSettings(&studioRecordingSettings{GainDB: &gain}); err != nil {
			t.Fatalf("rejected supported phrase gain %v: %v", gain, err)
		}
	}
	if _, err := s.vocalTakeSource(source, take, &studioRecordingSettings{AuditionMode: "invalid"}); err == nil {
		t.Fatal("accepted invalid audition mode")
	}
	if _, err := os.Stat(takeRoot(s.path) + "/audio/comps"); !os.IsNotExist(err) {
		t.Fatal("invalid settings created a comp print")
	}
	current, _ := os.ReadFile(s.path)
	if !bytes.Equal(current, source) {
		t.Fatal("invalid settings changed the score")
	}
}

func TestVocalGainAuditionKeepsIdentitiesAndRawSource(t *testing.T) {
	s, source, take := vocalGainFixture(t)
	rawBefore, _ := os.ReadFile(takeRoot(s.path) + "/" + take.Asset.Path)
	s.transport.audioNull = true
	boost := 20 * math.Log10(2)
	candidate, err := s.vocalTakeSource(source, take, &studioRecordingSettings{GainDB: &boost})
	if err != nil {
		t.Fatal(err)
	}
	p, err := compileStudioSource(s.path, candidate)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := project.CanonicalJSON(p)
	for _, mode := range []string{"", "context", "solo"} {
		filtered, err := vocalAuditionProject(p, take.Track, mode)
		if err != nil {
			t.Fatal(err)
		}
		if mode == "solo" {
			if len(filtered.Arrange.Placements) != 1 || filtered.Arrange.Placements[0].Track != take.Track {
				t.Fatal("vocal-only audition retained backing placements")
			}
			identity := *filtered
			identity.Arrange = p.Arrange
			canonical, _ := project.CanonicalJSON(&identity)
			if !bytes.Equal(canonical, before) {
				t.Fatal("vocal-only audition changed track/effect/asset identities")
			}
		} else {
			canonical, _ := project.CanonicalJSON(filtered)
			if !bytes.Equal(canonical, before) {
				t.Fatal("context audition changed the candidate project")
			}
		}
	}
	unchanged, _ := project.CanonicalJSON(p)
	if !bytes.Equal(unchanged, before) {
		t.Fatal("solo preparation modified the canonical candidate")
	}
	type audition struct {
		mode string
		gain *float64
		loop bool
		vox  float32
	}
	cases := []audition{{mode: "solo"}, {mode: "solo", gain: &boost, loop: true}, {mode: "", gain: &boost}, {mode: "context", gain: &boost}}
	for i := range cases {
		c := &cases[i]
		sequence := s.transport.meterSequence.Load()
		action := "listen"
		if c.loop {
			action = "listen-loop"
		}
		response := studioCall(t, s.routes(), "/api/takes", studioEdit{Action: action, TakeID: take.ID, Revision: studioRevision(source), Recording: &studioRecordingSettings{GainDB: c.gain, AuditionMode: c.mode}})
		if response.Code != http.StatusOK {
			t.Fatalf("audition %s: %d %s", c.mode, response.Code, response.Body)
		}
		state := s.transport.snapshot()
		mode := c.mode
		if mode == "" {
			mode = "context"
		}
		if state.Listening == nil || state.Listening.Mode != mode || state.Listening.Name != "Take in song" || state.Listening.Loop != c.loop {
			t.Fatalf("audition metadata: %+v", state.Listening)
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			meter := s.transport.latestMeter.Load()
			if meter != nil && meter.sequence > sequence && meter.frame.TrackCount == 2 && meter.frame.TrackIDs[0] == "vox" && meter.frame.TrackIDs[1] == "bass" && meter.frame.Tracks[0].Peak > .04 {
				bass := meter.frame.Tracks[1].Peak
				if mode == "solo" && bass == 0 || mode == "context" && bass > .001 {
					c.vox = meter.frame.Tracks[0].Peak
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		s.transport.stop()
		if c.vox == 0 {
			t.Fatalf("%s audition did not produce the expected native vocal/backing signal", mode)
		}
	}
	if math.Abs(float64(cases[1].vox/cases[0].vox)-2) > .01 {
		t.Fatalf("gain differed in native vocal-only audition: unity=%g boosted=%g", cases[0].vox, cases[1].vox)
	}
	current, _ := os.ReadFile(s.path)
	rawAfter, _ := os.ReadFile(takeRoot(s.path) + "/" + take.Asset.Path)
	if !bytes.Equal(current, source) || !bytes.Equal(rawBefore, rawAfter) || len(s.takes.Takes()) != 1 || len(s.history.historySnapshot().Edits) != 0 {
		t.Fatal("audition changed source, takes, raw input or Undo history")
	}
	if err := s.commitTake(take.ID, studioRevision(source), candidate); err != nil {
		t.Fatal(err)
	}
	committed, _ := os.ReadFile(s.path)
	auditionAsset, auditionPCM := vocalGainPrint(t, s, candidate)
	committedAsset, committedPCM := vocalGainPrint(t, s, committed)
	if auditionAsset != committedAsset || !bytes.Equal(auditionPCM, committedPCM) {
		t.Fatal("gain differed between audition and committed comp")
	}
}
