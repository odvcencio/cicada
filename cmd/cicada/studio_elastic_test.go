package main

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/project"
)

func TestElasticVocalFollowsTempoAndUndoKeepsRawTake(t *testing.T) {
	isolateState(t)
	s, source, _ := tempoVocalStudio(t)
	take, _ := mappedVocalTake(t, s, 0, 4*3840, false, func(i int) float32 { return float32(.2 * math.Sin(2*math.Pi*220*float64(i)/48000)) })
	rawPath := filepath.Join(takeRoot(s.path), take.Asset.Path)
	raw, _ := os.ReadFile(rawPath)
	follow := true
	selection, envelope, err := s.vocalTakeResult(source, take, &studioRecordingSettings{Elastic: &follow})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.commitTakeFiles(take.ID, studioRevision(source), selection.Source, &envelope, selection.Files, ""); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(s.path)
	comps, err := vocalComps(before)
	if err != nil || len(comps) != 1 || !comps[0].Elastic {
		t.Fatal("elastic metadata", comps, err)
	}
	previous := comps[0].Asset
	revision, err := project.ReadScoreRevision(s.path)
	if err != nil {
		t.Fatal(err)
	}
	response := studioCall(t, s.routes(), "/api/arrange", studioEdit{Revision: studioRevision(before), SourceRevision: revision, Action: "set-tempo", Tempo: &edits.SetArrangementTempo{At: "@2.1.1", Value: "110"}})
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	after, _ := os.ReadFile(s.path)
	comps, err = vocalComps(after)
	if err != nil || len(comps) != 1 || comps[0].Asset == previous {
		t.Fatal("tempo edit did not publish a fresh print", err)
	}
	p, err := compileStudioSource(s.path, after)
	if err != nil {
		t.Fatal(err)
	}
	var asset project.Asset
	for _, a := range p.Assets {
		if a.Name == comps[0].Asset {
			asset = a
		}
	}
	clock, err := project.NewTempoMap(48000, int64(p.TempoMilli), project.SongTempoChanges(p))
	if err != nil {
		t.Fatal(err)
	}
	if asset.Frames != clock.SampleAtTick(4*3840) {
		t.Fatal("stretched duration", asset.Frames, clock.SampleAtTick(4*3840))
	}
	wav, err := os.ReadFile(filepath.Join(takeRoot(s.path), asset.Path))
	if err != nil {
		t.Fatal(err)
	}
	start, end := int(asset.Frames/4), int(asset.Frames*3/4)
	crossings := 0
	last := float32(0)
	for i := start; i < end; i++ {
		v := math.Float32frombits(binary.LittleEndian.Uint32(wav[44+i*4:]))
		if last <= 0 && v > 0 {
			crossings++
		}
		last = v
	}
	hz := float64(crossings) * 48000 / float64(end-start)
	if math.Abs(hz-220) > 2 {
		t.Fatal("stretch changed pitch", hz)
	}
	stillRaw, _ := os.ReadFile(rawPath)
	if !bytes.Equal(raw, stillRaw) {
		t.Fatal("elastic edit changed raw recording")
	}
	response = studioCall(t, s.routes(), "/api/undo", studioEdit{Revision: studioRevision(after)})
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	undone, _ := os.ReadFile(s.path)
	if !bytes.Equal(before, undone) {
		t.Fatal("Undo did not restore comp metadata and source exactly")
	}
}

func TestElasticSelectionUsesCurrentTempoWithoutChangingJournal(t *testing.T) {
	isolateState(t)
	s, source, owner := tempoVocalStudio(t)
	take, _ := mappedVocalTake(t, s, 0, 4*3840, false)
	part, err := os.ReadFile(owner)
	if err != nil {
		t.Fatal(err)
	}
	part = bytes.Replace(part, []byte("tempo=96"), []byte("tempo=108"), 1)
	if err = os.WriteFile(owner, part, 0600); err != nil {
		t.Fatal(err)
	}
	follow := true
	result, _, err := s.vocalTakeResult(source, take, &studioRecordingSettings{Elastic: &follow})
	if err != nil {
		t.Fatal(err)
	}
	comps, err := vocalComps(result.Source)
	if err != nil || !comps[0].Elastic {
		t.Fatal(err)
	}
	stored, err := s.takes.Get(take.ID)
	if err != nil || stored.Timeline.TempoChanges[1].BPMMilli != 96000 {
		t.Fatal("original tempo metadata changed", err)
	}
}

func TestElasticCodeBaseTempoSaveKeepsCompMetadataAndUndoTogether(t *testing.T) {
	isolateState(t)
	s, source, _ := tempoVocalStudio(t)
	take, _ := mappedVocalTake(t, s, 0, 4*3840, false)
	follow := true
	selection, env, err := s.vocalTakeResult(source, take, &studioRecordingSettings{Elastic: &follow})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.commitTakeFiles(take.ID, studioRevision(source), selection.Source, &env, selection.Files, ""); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(s.path)
	comps, _ := vocalComps(before)
	// Code changes the base tempo without using the Project settings form.
	candidate := bytes.Replace(before, []byte("tempo 120"), []byte("tempo 112"), 1)

	revision, _ := project.ReadScoreRevision(s.path)
	r := studioCall(t, s.routes(), "/api/source", studioEdit{Revision: studioRevision(before), SourceRevision: revision, Source: string(candidate)})
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	after, _ := os.ReadFile(s.path)
	comps, _ = vocalComps(after)
	if comps[0].Tempo != 112000 {
		t.Fatal("Code tempo save did not reprint", comps[0])
	}
	r = studioCall(t, s.routes(), "/api/undo", studioEdit{Revision: studioRevision(after)})
	undone, _ := os.ReadFile(s.path)
	if r.Code != 200 || !bytes.Equal(before, undone) {
		t.Fatal("Code tempo Undo did not restore print", r.Body.String())
	}
}
