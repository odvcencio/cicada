package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"slices"
	"sort"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/host/elastic"
	"m31labs.dev/cicada/host/sampleasset"
	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/project"
)

// Tempo edits and their derived audio are published by the same existing
// revision-checked transaction, so Undo restores both sources and old prints.
func (s *studio) elasticTempoIntents(result *edits.Result) ([]edits.Intent, error) {
	comps, err := vocalComps(result.Source)
	if err != nil {
		return nil, err
	}
	found := false
	for _, c := range comps {
		found = found || c.Elastic
	}
	if !found {
		return nil, nil
	}
	overrides := map[string][]byte{}
	for _, f := range result.Files {
		overrides[f.Path] = f.After
	}
	p, err := compileStudioSourceWithOverrides(s.path, result.Source, overrides)
	if err != nil {
		return nil, err
	}
	var intents []edits.Intent
	for _, c := range comps {
		if !c.Elastic || c.Tempo == int64(p.TempoMilli) && slices.Equal(c.TempoChanges, project.SongTempoChanges(p)) {
			continue
		}
		previous := c.Asset
		rate := 0
		for _, asset := range p.Assets {
			if asset.Name == previous {
				rate = asset.RateHz
			}
		}
		if rate == 0 {
			return nil, fmt.Errorf("elastic vocal %s has no retained print", c.Track)
		}
		c.Tempo, c.TempoChanges = int64(p.TempoMilli), project.SongTempoChanges(p)
		asset, err := s.printVocalComp(c, rate)
		if err != nil {
			return nil, err
		}
		c.Asset = asset.Name
		intents = append(intents, &edits.PlaceVocalComp{Comp: c, Previous: &edits.VocalPrevious{Asset: previous}, Asset: edits.PrintAsset{Path: asset.Path, SHA256: asset.SHA256, Frames: asset.Frames, RateHz: asset.RateHz}})
	}
	return intents, nil
}

// elasticVocalTake prepares a derived take for this print only. The journal,
// source recording, original tempo and calibration remain authoritative.
func (s *studio) elasticVocalTake(t takejournal.Take, c vocalComp) (takejournal.Take, error) {
	if t.Timeline == nil {
		return t, fmt.Errorf("elastic audio needs a retained Session take")
	}
	if t.Timeline.TempoMilli == c.Tempo && slices.Equal(t.Timeline.TempoChanges, c.TempoChanges) {
		return t, nil
	}
	source, err := takeClock(t)
	if err != nil {
		return t, err
	}
	target, err := project.NewTempoMap(t.Rate, c.Tempo, c.TempoChanges)
	if err != nil {
		return t, err
	}
	begin := t.Timeline.PlaybackStartTick
	base := takeOrigin(t)
	if base < 0 {
		begin = max(begin, source.TickAtSample(source.SampleAtTick(begin)-base)+1)
	}
	_, end := takeCoverage(t)
	if begin > t.Timeline.StartTick || end <= begin {
		return t, fmt.Errorf("recording has no complete elastic source interval")
	}
	raw := base + source.SampleAtTick(begin) - source.SampleAtTick(t.Timeline.PlaybackStartTick)
	from := source.SampleAtTick(begin)
	to := target.SampleAtTick(begin)
	sourceFrames, targetFrames := source.SampleAtTick(end)-from, target.SampleAtTick(end)-to
	if raw < 0 || raw+sourceFrames > int64(t.Frames) || targetFrames <= 0 || targetFrames > 16<<20 {
		return t, fmt.Errorf("elastic vocal exceeds its retained audio or 64 MiB print budget")
	}
	ticks := []int64{begin, end}
	for _, changes := range [][]project.TempoChange{t.Timeline.TempoChanges, c.TempoChanges} {
		for _, change := range changes {
			tick := int64(change.Bar) * seq.TicksPerBar
			if tick > begin && tick < end {
				ticks = append(ticks, tick)
			}
		}
	}
	sort.Slice(ticks, func(i, j int) bool { return ticks[i] < ticks[j] })
	ticks = slices.Compact(ticks)
	anchors := make([]elastic.Anchor, 0, len(ticks))
	for _, tick := range ticks {
		anchors = append(anchors, elastic.Anchor{Source: source.SampleAtTick(tick) - from, Target: target.SampleAtTick(tick) - to})
	}
	stream, err := sampleasset.OpenStream(takeRoot(s.path), t.Asset)
	if err != nil {
		return t, err
	}
	defer stream.Close()
	data := monoVocalWAV(t.Rate, targetFrames)
	right := make([]float32, max(4096, t.Rate/10))
	err = elastic.Render(context.Background(), elastic.Config{Rate: t.Rate, SourceFrames: sourceFrames, TargetFrames: targetFrames, Anchors: anchors}, func(ctx context.Context, at int64, values []float32) error {
		if err := stream.ReadFrames(ctx, raw+at, values, right[:len(values)]); err != nil {
			return err
		}
		for i := range values {
			values[i] = values[i]*.5 + right[i]*.5
		}
		return nil
	}, func(_ context.Context, at int64, values []float32) error {
		for i, v := range values {
			binary.LittleEndian.PutUint32(data[44+(at+int64(i))*4:], math.Float32bits(v))
		}
		return nil
	})
	if err != nil {
		return t, err
	}
	asset, err := s.storeVocalPCM(data, t.Rate, targetFrames, "elastic")
	if err != nil {
		return t, err
	}
	timeline := *t.Timeline
	timeline.PlaybackStartTick = begin
	timeline.EndTick = end
	timeline.TempoMilli = c.Tempo
	timeline.TempoChanges = slices.Clone(c.TempoChanges)
	t.Asset, t.Frames, t.Channels, t.FirstBlock, t.Timeline = asset, uint64(targetFrames), 1, nil, &timeline
	return t, nil
}

func monoVocalWAV(rate int, frames int64) []byte {
	data := make([]byte, 44+frames*4)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 3)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], uint32(rate))
	binary.LittleEndian.PutUint32(data[28:], uint32(rate*4))
	binary.LittleEndian.PutUint16(data[32:], 4)
	binary.LittleEndian.PutUint16(data[34:], 32)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(frames*4))
	return data
}
