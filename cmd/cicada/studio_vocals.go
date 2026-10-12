package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/sampleasset"
	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type studioRecordingSettings struct {
	Start        string   `json:"start"`
	End          string   `json:"end"`
	GainDB       *float64 `json:"gainDB,omitempty"`
	AuditionMode string   `json:"auditionMode,omitempty"`
	OffsetMS     float64  `json:"offsetMS,omitempty"`
	FadeMS       *float64 `json:"fadeMS,omitempty"`
	CrossfadeMS  *float64 `json:"crossfadeMS,omitempty"`
	Passes       int      `json:"passes,omitempty"`
}

func validateVocalGain(gainDB float64) error {
	if math.IsNaN(gainDB) || math.IsInf(gainDB, 0) || gainDB < -24 || gainDB > 18 {
		return fmt.Errorf("phrase gain must be finite and between -24 and +18 dB")
	}
	return nil
}

func vocalPhraseSettings(settings *studioRecordingSettings) (gainDB float64, mode string, err error) {
	mode = "context"
	if settings != nil {
		if err = validateVocalEdits(settings.OffsetMS, settings.FadeMS, settings.CrossfadeMS); err != nil {
			return
		}
		if settings.Passes < 0 || settings.Passes > 8 {
			err = fmt.Errorf("recording passes must be between 1 and 8")
			return
		}
		if settings.GainDB != nil {
			gainDB = *settings.GainDB
		}
		if settings.AuditionMode != "" {
			mode = settings.AuditionMode
		}
	}
	if err = validateVocalGain(gainDB); err != nil {
		return
	}
	if mode != "context" && mode != "solo" {
		err = fmt.Errorf("hear the phrase with the song or vocal only")
	}
	return
}

func validateVocalEdits(offsetMS float64, fadeMS, crossfadeMS *float64) error {
	if math.IsNaN(offsetMS) || math.IsInf(offsetMS, 0) || offsetMS < -500 || offsetMS > 500 {
		return fmt.Errorf("phrase timing advance must be finite and between -500 and +500 ms")
	}
	for name, value := range map[string]*float64{"fade": fadeMS, "crossfade": crossfadeMS} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 || *value > 100) {
			return fmt.Errorf("phrase %s must be finite and between 0 and 100 ms", name)
		}
	}
	return nil
}

func vocalEditValue(value *float64, fallback float64) float64 {
	if value != nil {
		return *value
	}
	return fallback
}

func vocalMillisecondsFrames(ms float64, rate int) int64 {
	return int64(math.Round(ms * float64(rate) / 1000))
}

func notationTicks(value string) (int64, error) { return notation.MusicalTicks(value, true) }

type vocalSegment struct {
	Take   string  `json:"take"`
	Start  int64   `json:"startTick"`
	End    int64   `json:"endTick"`
	GainDB float64 `json:"gainDB,omitempty"`
	// Positive offset reads later source frames at the same song position. Raw
	// take coordinates remain immutable; trimming either side keeps this mapping.
	OffsetMS    float64  `json:"offsetMS,omitempty"`
	FadeMS      *float64 `json:"fadeMS,omitempty"`
	CrossfadeMS *float64 `json:"crossfadeMS,omitempty"`
}

type vocalComp struct {
	Track    string         `json:"track"`
	Tempo    int64          `json:"tempoMilli"`
	Segments []vocalSegment `json:"segments"`
	Asset    string         `json:"asset"`
}

const vocalCompPrefix = "// cicada-vocal-comp "

func vocalComps(source []byte) ([]vocalComp, error) {
	var result []vocalComp
	for _, line := range strings.Split(string(source), "\n") {
		if !strings.HasPrefix(line, vocalCompPrefix) {
			continue
		}
		var c vocalComp
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, vocalCompPrefix)), &c); err != nil {
			return nil, fmt.Errorf("vocal comp metadata: %w", err)
		}
		if !studioPatternName.MatchString(c.Track) || len(c.Segments) > 4096 || c.Tempo < 20000 || c.Tempo > 300000 {
			return nil, fmt.Errorf("invalid vocal comp metadata")
		}
		for _, seg := range c.Segments {
			if seg.Start < 0 || seg.End <= seg.Start || seg.End > 1<<40 {
				return nil, fmt.Errorf("invalid vocal comp region")
			}
			if err := validateVocalGain(seg.GainDB); err != nil {
				return nil, fmt.Errorf("invalid vocal comp metadata: %w", err)
			}
			if err := validateVocalEdits(seg.OffsetMS, seg.FadeMS, seg.CrossfadeMS); err != nil {
				return nil, fmt.Errorf("invalid vocal comp metadata: %w", err)
			}
		}
		result = append(result, c)
	}
	return result, nil
}

// Replacing a phrase splits its neighbors; their raw take coordinates stay put.
func replaceVocalSegment(old []vocalSegment, next vocalSegment) []vocalSegment {
	var result []vocalSegment
	for _, seg := range old {
		if seg.End <= next.Start || seg.Start >= next.End {
			result = append(result, seg)
			continue
		}
		if seg.Start < next.Start {
			left := seg
			left.End = next.Start
			result = append(result, left)
		}
		if seg.End > next.End {
			right := seg
			right.Start = next.End
			result = append(result, right)
		}
	}
	if next.Take != "" {
		result = append(result, next)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Start < result[j].Start })
	// Adjacent pieces from the same pass and gain need no artificial join.
	merged := make([]vocalSegment, 0, len(result))
	for _, seg := range result {
		if len(merged) > 0 && sameVocalMapping(merged[len(merged)-1], seg) && merged[len(merged)-1].End == seg.Start {
			merged[len(merged)-1].End = seg.End
		} else {
			merged = append(merged, seg)
		}
	}
	return merged
}

func sameVocalMapping(a, b vocalSegment) bool {
	return a.Take == b.Take && a.GainDB == b.GainDB && a.OffsetMS == b.OffsetMS &&
		vocalEditValue(a.FadeMS, 5) == vocalEditValue(b.FadeMS, 5) &&
		vocalEditValue(a.CrossfadeMS, 0) == vocalEditValue(b.CrossfadeMS, 0)
}

func takeClock(t takejournal.Take) seq.Clock {
	return seq.Clock{SampleRate: int64(t.Rate), BPMMilli: t.Timeline.TempoMilli}
}
func takeOrigin(t takejournal.Take) int64 {
	if t.FirstBlock == nil {
		return 0
	}
	return int64(t.FirstBlock.RawFrame) - t.FirstBlock.Placement.EngineFrame
}
func takeCoverage(t takejournal.Take) (int64, int64) {
	c := takeClock(t)
	end := c.TickAtSample(c.SampleAtTick(t.Timeline.PlaybackStartTick) + int64(t.Frames) - takeOrigin(t))
	return t.Timeline.StartTick, min(t.Timeline.EndTick, end)
}

func recordingTimeline(p *project.Project, settings *studioRecordingSettings) (*takejournal.Timeline, error) {
	if _, _, err := vocalPhraseSettings(settings); err != nil {
		return nil, err
	}
	if p.Edition != 2 {
		return nil, fmt.Errorf("vocal recording requires an edition-2 score")
	}
	start, end := int64(0), studioArrangementEnd(p)
	var err error
	if settings != nil && settings.Start != "" {
		start, err = notationTicks(settings.Start)
		if err != nil {
			return nil, err
		}
	}
	if settings != nil && settings.End != "" {
		end, err = notationTicks(settings.End)
		if err != nil {
			return nil, err
		}
	}
	if start < 0 || end <= start || end > studioArrangementEnd(p) {
		return nil, fmt.Errorf("choose a recording range within the full song")
	}
	return &takejournal.Timeline{StartTick: start, EndTick: end, PlaybackStartTick: max(0, start-seq.TicksPerBar), TempoMilli: int64(p.TempoMilli)}, nil
}

// Scene songs gain ordinary placements so a continuous vocal can span blocks.
// Explicit scene bindings restart a part; Keep carries its phase to the next block.
func vocalArrangementSource(source []byte, p *project.Project) ([]byte, error) {
	if p.Arrange != nil {
		return source, nil
	}
	root, w, err := notation.ParseTree(source)
	if err != nil {
		return nil, err
	}
	var start, end int
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if w.Type(n) == "song_decl" {
			start, end = int(n.StartByte()), int(n.EndByte())
			break
		}
	}
	if end == 0 {
		return nil, fmt.Errorf("full-song recording needs a song or arrangement")
	}
	scenes := map[string]project.Scene{}
	for _, v := range p.Scenes {
		scenes[v.ID] = v
	}
	var text strings.Builder
	text.WriteString("arrange {\n")
	at := int64(0)
	counter := 0
	type activePart struct {
		content string
		start   int64
	}
	active := map[string]activePart{}
	flush := func(track string, part activePart, until int64) {
		if until <= part.start {
			return
		}
		counter++
		fmt.Fprintf(&text, "  place backing-%d %s %s { at = %dticks length = %dticks }\n", counter, track, part.content, part.start, until-part.start)
	}
	for i, entry := range p.Song {
		fmt.Fprintf(&text, "  marker block-%d { at = %dticks }\n", i+1, at)
		scene := scenes[entry.Scene]
		for _, track := range p.Tracks {
			binding := scene.Bindings[track.ID]
			if binding == "" || binding == "keep" {
				continue
			}
			if part, ok := active[track.ID]; ok {
				flush(track.ID, part, at)
				delete(active, track.ID)
			}
			if binding != "off" {
				active[track.ID] = activePart{binding, at}
			}
		}
		at += int64(entry.Bars) * seq.TicksPerBar
	}
	for _, track := range p.Tracks {
		if part, ok := active[track.ID]; ok {
			flush(track.ID, part, at)
		}
	}
	text.WriteString("}\n")
	return replaceSongSpan(source, start, end, []byte(text.String()))
}

// Comp audio is a bounded mono print; every source pass remains in the journal.
// Only this track's generated asset/clip/placement is replaced. Older prints
// stay on disk so Undo, Redo, recovery and exported projects retain their audio.
func (s *studio) vocalTakeSource(source []byte, t takejournal.Take, settings *studioRecordingSettings) ([]byte, error) {
	gainDB, _, err := vocalPhraseSettings(settings)
	if err != nil {
		return nil, err
	}
	p, err := compileStudioSource(s.path, source)
	if err != nil {
		return nil, err
	}
	if t.Timeline == nil {
		return nil, fmt.Errorf("choose a Session arrangement take")
	}
	if int64(p.TempoMilli) != t.Timeline.TempoMilli {
		return nil, fmt.Errorf("restore the recording tempo before selecting this take")
	}
	found := false
	for _, tr := range p.Tracks {
		if tr.ID == t.Track && tr.Kind == "audio" {
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("take target must be an existing audio track")
	}
	lo, hi := takeCoverage(t)
	if settings != nil {
		if settings.Start != "" {
			lo, err = notationTicks(settings.Start)
			if err != nil {
				return nil, err
			}
		}
		if settings.End != "" {
			hi, err = notationTicks(settings.End)
			if err != nil {
				return nil, err
			}
		}
	}
	from, to := takeCoverage(t)
	if lo < from || hi > to || hi <= lo || hi > studioArrangementEnd(p) {
		return nil, fmt.Errorf("choose a region covered by this take (%s to %s)", listeningPosition(from), listeningPosition(to))
	}
	comps, err := vocalComps(source)
	if err != nil {
		return nil, err
	}
	next := vocalComp{Track: t.Track, Tempo: t.Timeline.TempoMilli}
	var previous *vocalComp
	for i := range comps {
		if comps[i].Track == t.Track {
			previous = &comps[i]
			next.Segments = comps[i].Segments
			break
		}
	}
	if previous != nil && previous.Tempo != next.Tempo {
		return nil, fmt.Errorf("vocal comp tempo differs from this take")
	}
	segment := vocalSegment{Take: t.ID, Start: lo, End: hi, GainDB: gainDB}
	if settings != nil {
		segment.OffsetMS, segment.FadeMS, segment.CrossfadeMS = settings.OffsetMS, settings.FadeMS, settings.CrossfadeMS
	}
	next.Segments = replaceVocalSegment(next.Segments, segment)
	asset, err := s.printVocalComp(next, t.Rate)
	if err != nil {
		return nil, err
	}
	next.Asset = asset.Name
	source, err = vocalArrangementSource(source, p)
	if err != nil {
		return nil, err
	}
	// Remove just the managed block and its placement, preserving every other byte.
	if previous != nil {
		begin := []byte("// cicada-vocal-begin " + t.Track + "\n")
		finish := []byte("// cicada-vocal-end " + t.Track + "\n")
		a := bytes.Index(source, begin)
		if a < 0 {
			return nil, fmt.Errorf("vocal comp declarations were edited; restore them before selecting")
		}
		b := bytes.Index(source[a:], finish)
		if b < 0 {
			return nil, fmt.Errorf("vocal comp declarations are incomplete")
		}
		// Generic score edits can append an effect or another declaration before
		// the trailing managed comment. Delete only our print declarations and
		// metadata, retaining those user/application edits byte for byte.
		block := source[a : a+b+len(finish)]
		kept, e := removeVocalPrintDeclarations(block, previous, begin, finish)
		if e != nil {
			return nil, e
		}
		source, err = replaceSongSpan(source, a, a+b+len(finish), kept)
		if err != nil {
			return nil, err
		}
	}
	root, w, err := notation.ParseTree(source)
	if err != nil {
		return nil, err
	}
	var edits []studioSpanEdit
	foundArrangement := false
	placeID := "vocal-" + t.Track
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if w.Type(n) != "arrange_decl" {
			continue
		}
		foundArrangement = true
		for j := 0; j < n.NamedChildCount(); j++ {
			child := n.NamedChild(j)
			if w.Type(child) == "place_decl" && w.Text(w.Field(child, "name")) == placeID {
				if previous == nil {
					return nil, fmt.Errorf("placement name %s already exists", placeID)
				}
				edits = append(edits, studioSpanEdit{start: int(child.StartByte()), end: int(child.EndByte()), text: ""})
			}
		}
		first, last := next.Segments[0].Start, next.Segments[len(next.Segments)-1].End
		line := fmt.Sprintf("\n  place %s %s %s-clip { at = %dticks length = %dticks }\n", placeID, t.Track, asset.Name, first, last-first)
		edits = append(edits, studioSpanEdit{start: int(n.EndByte()) - 1, end: int(n.EndByte()) - 1, text: line})
	}
	if !foundArrangement {
		return nil, fmt.Errorf("open the score file containing the arrangement before selecting a vocal")
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	source, err = patchStudioSpans(source, edits)
	if err != nil {
		return nil, err
	}
	metadata, _ := json.Marshal(next)
	text := fmt.Sprintf("\n// cicada-vocal-begin %s\n%s%s\nasset %s %s { sha256 = %s format = wav frames = %d rate = %dHz channels = 1 source = recorded }\nclip %s-clip %s { start = 0frames end = %dframes }\n// cicada-vocal-end %s\n", t.Track, vocalCompPrefix, metadata, asset.Name, strconv.Quote(asset.Path), strconv.Quote(asset.SHA256), asset.Frames, asset.RateHz, asset.Name, asset.Name, asset.Frames, t.Track)
	return append(source, []byte(text)...), nil
}

func removeVocalPrintDeclarations(block []byte, previous *vocalComp, begin, finish []byte) ([]byte, error) {
	root, w, err := notation.ParseTree(block)
	if err != nil {
		return nil, err
	}
	edits := []studioSpanEdit{{start: 0, end: len(begin)}, {start: len(block) - len(finish), end: len(block)}}
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		kind, name := w.Type(n), w.Text(w.Field(n, "name"))
		if kind == "asset_decl" && name == previous.Asset || kind == "clip_decl" && name == previous.Asset+"-clip" {
			edits = append(edits, studioSpanEdit{start: int(n.StartByte()), end: int(n.EndByte())})
		}
	}
	for at := 0; at < len(block); {
		end := bytes.IndexByte(block[at:], '\n')
		if end < 0 {
			end = len(block) - at - 1
		}
		end += at + 1
		if bytes.HasPrefix(block[at:end], []byte(vocalCompPrefix)) {
			var metadata vocalComp
			if json.Unmarshal(bytes.TrimSpace(block[at+len(vocalCompPrefix):end]), &metadata) == nil && metadata.Track == previous.Track {
				edits = append(edits, studioSpanEdit{start: at, end: end})
			}
		}
		at = end
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	return patchStudioSpans(block, edits)
}

func (s *studio) printVocalComp(c vocalComp, rate int) (project.Asset, error) {
	var result project.Asset
	if len(c.Segments) == 0 {
		return result, fmt.Errorf("choose a vocal phrase before printing")
	}
	for _, seg := range c.Segments {
		if err := validateVocalGain(seg.GainDB); err != nil {
			return result, err
		}
		if err := validateVocalEdits(seg.OffsetMS, seg.FadeMS, seg.CrossfadeMS); err != nil {
			return result, err
		}
	}
	clock := seq.Clock{SampleRate: int64(rate), BPMMilli: c.Tempo}
	first, last := c.Segments[0].Start, c.Segments[len(c.Segments)-1].End
	origin := clock.SampleAtTick(first)
	frames := clock.SampleAtTick(last) - origin
	if frames <= 0 || frames > 16<<20 {
		return result, fmt.Errorf("vocal comp exceeds the 64 MiB mono PCM budget")
	}
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
	var left, right [4096]float32
	streams := map[string]*sampleasset.WAVSource{}
	defer func() {
		for _, stream := range streams {
			stream.Close()
		}
	}()
	type printRegion struct {
		seg                   vocalSegment
		take                  takejournal.Take
		stream                *sampleasset.WAVSource
		start, end, raw, fade int64
		gain                  float32
		fadeIn, fadeOut       bool
	}
	regions := make([]printRegion, 0, len(c.Segments))
	for _, seg := range c.Segments {
		take, err := s.takes.Get(seg.Take)
		if err != nil {
			return result, err
		}
		if take.Timeline == nil || take.Track != c.Track || take.Rate != rate || take.Timeline.TempoMilli != c.Tempo {
			return result, fmt.Errorf("comp takes must share track, sample rate and tempo")
		}
		a, b := takeCoverage(take)
		if seg.Start < a || seg.End > b {
			return result, fmt.Errorf("comp region exceeds retained take")
		}
		stream := streams[take.ID]
		if stream == nil {
			stream, err = sampleasset.OpenStream(takeRoot(s.path), take.Asset)
			if err != nil {
				return result, err
			}
			streams[take.ID] = stream
		}
		count := clock.SampleAtTick(seg.End) - clock.SampleAtTick(seg.Start)
		offset := clock.SampleAtTick(seg.Start) - origin
		raw := takeOrigin(take) + clock.SampleAtTick(seg.Start) - clock.SampleAtTick(take.Timeline.PlaybackStartTick) + vocalMillisecondsFrames(seg.OffsetMS, rate)
		if raw < 0 || raw+count > int64(take.Frames) {
			return result, fmt.Errorf("phrase timing needs recorded audio beyond this selection; trim the phrase or reduce its advance")
		}
		fade := int64(rate / 200)
		if seg.FadeMS != nil {
			fade = vocalMillisecondsFrames(*seg.FadeMS, rate)
		}
		regions = append(regions, printRegion{seg: seg, take: take, stream: stream, start: offset, end: offset + count, raw: raw, fade: min(fade, count/2), gain: float32(math.Pow(10, seg.GainDB/20)), fadeIn: true, fadeOut: true})
	}
	type printJoin struct {
		left, right int
		start, end  int64
	}
	var joins []printJoin
	for i := 1; i < len(regions); i++ {
		a, b := &regions[i-1], &regions[i]
		if a.end != b.start {
			continue
		}
		// Either phrase can request a join. Handles come from the original
		// passes, bounded by their actual retained PCM rather than padded silence.
		n := vocalMillisecondsFrames(max(vocalEditValue(a.seg.CrossfadeMS, 0), vocalEditValue(b.seg.CrossfadeMS, 0)), rate)
		if n < 2 {
			continue
		}
		start := max(a.end-min(n/2, (a.end-a.start)/2), a.start-a.raw, b.start-b.raw)
		end := min(b.start+min(n-n/2, (b.end-b.start)/2), a.start+int64(a.take.Frames)-a.raw, b.start+int64(b.take.Frames)-b.raw)
		if end-start < 2 {
			continue
		}
		a.fadeOut, b.fadeIn = false, false
		joins = append(joins, printJoin{i - 1, i, start, end})
	}
	for _, region := range regions {
		count := region.end - region.start
		for at := int64(0); at < count; {
			n := min(int64(len(left)), count-at)
			if err := region.stream.ReadFrames(context.Background(), region.raw+at, left[:n], right[:n]); err != nil {
				return result, err
			}
			for i := int64(0); i < n; i++ {
				gain := region.gain
				if region.fade > 0 {
					if region.fadeIn {
						gain *= float32(min(1, float64(at+i)/float64(region.fade)))
					}
					if region.fadeOut {
						gain *= float32(min(1, float64(count-1-at-i)/float64(region.fade)))
					}
				}
				v := (left[i]*0.5 + right[i]*0.5) * gain
				binary.LittleEndian.PutUint32(data[44+(region.start+at+i)*4:], math.Float32bits(v))
			}
			at += n
		}
	}
	var otherLeft, otherRight [4096]float32
	for _, join := range joins {
		a, b := regions[join.left], regions[join.right]
		for at := join.start; at < join.end; {
			n := min(int64(len(left)), join.end-at)
			if err := a.stream.ReadFrames(context.Background(), a.raw+at-a.start, left[:n], right[:n]); err != nil {
				return result, err
			}
			if err := b.stream.ReadFrames(context.Background(), b.raw+at-b.start, otherLeft[:n], otherRight[:n]); err != nil {
				return result, err
			}
			for i := int64(0); i < n; i++ {
				blend := float32(at+i-join.start) / float32(join.end-join.start-1)
				v := (left[i]*.5+right[i]*.5)*a.gain*(1-blend) + (otherLeft[i]*.5+otherRight[i]*.5)*b.gain*blend
				binary.LittleEndian.PutUint32(data[44+(at+i)*4:], math.Float32bits(v))
			}
			at += n
		}
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	result = project.Asset{Name: "vocal-" + hash[:16], Path: "audio/comps/" + hash + ".wav", SHA256: hash, Format: "wav", Frames: frames, RateHz: rate, Channels: 1, Source: "recorded"}
	root, err := os.OpenRoot(takeRoot(s.path))
	if err != nil {
		return result, err
	}
	defer root.Close()
	if err = root.MkdirAll("audio/comps", 0700); err != nil {
		return result, err
	}
	if _, err := root.Stat(result.Path); err == nil {
		return result, takejournal.Verify(root, result.Path, result)
	} else if !os.IsNotExist(err) {
		return result, err
	}
	temporary := fmt.Sprintf("audio/comps/.print-%d.tmp", time.Now().UnixNano())
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return result, err
	}
	defer root.Remove(temporary)
	n, err := file.Write(data)
	if err == nil && n != len(data) {
		err = fmt.Errorf("incomplete comp write")
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return result, err
	}
	if err = root.Rename(temporary, result.Path); err != nil {
		return result, err
	}
	for _, dir := range []string{"audio/comps", "audio", "."} {
		if err = takejournal.SyncDirectory(filepath.Join(takeRoot(s.path), dir)); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (s *studio) finishVocalCapture(id string) error {
	if s.captureID != id || s.captureRecorder == nil {
		return nil
	}
	s.transport.stop()
	drainErr := s.transport.disarmCapture()
	// A failed audio start may already have released its device.
	if err := s.captureRecorder.Close(); drainErr == nil {
		drainErr = err
	}
	snapshot := s.captureRecorder.Snapshot()
	loop := s.vocalLoop
	s.captureID = ""
	s.captureRecorder = nil
	s.vocalLoop = nil
	if loop != nil {
		return s.finishVocalLoop(loop, snapshot, drainErr)
	}
	if err := s.finalizeCaptureTake(id, snapshot, drainErr); err != nil {
		return err
	}
	if err := s.takes.Publish(id); err != nil {
		return err
	}
	take, _ := s.takes.Get(id)
	if take.Timeline != nil && (snapshot.Incomplete || drainErr != nil) {
		return fmt.Errorf("take retained with input gaps; audition it before choosing a comp region")
	}
	return s.commitTake(id, take.Expected, nil)
}

func (s *studio) watchVocalCapture(id string, r *capture.Recorder) {
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		if s.captureID != id || s.captureRecorder != r {
			s.mu.Unlock()
			return
		}
		snapshot := r.Snapshot()
		if snapshot.Completed || snapshot.Error != "" {
			err := s.finishVocalCapture(id)
			if err != nil {
				s.transport.setError(err)
			}
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
	}
}

func (s *studio) startAudioTake() error {
	take, err := s.takes.Get(s.captureID)
	if err != nil {
		return err
	}
	if take.Timeline == nil {
		return s.transport.startCapture(capture.Calibration{})
	}
	source, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	if studioRevision(source) != take.Expected {
		return errTakeRevision
	}
	p, err := compileStudioSource(s.path, source)
	if err != nil {
		return err
	}
	// The armed track's previous comp must not mask the new input.
	copyProject := *p
	if p.Arrange != nil {
		arrangement := *p.Arrange
		arrangement.Placements = nil
		for _, place := range p.Arrange.Placements {
			if place.Track != take.Track {
				arrangement.Placements = append(arrangement.Placements, place)
			}
		}
		copyProject.Arrange = &arrangement
	} else {
		copyProject.Scenes = make([]project.Scene, len(p.Scenes))
		for i, scene := range p.Scenes {
			copyProject.Scenes[i] = scene
			bindings := map[string]string{}
			for k, v := range scene.Bindings {
				bindings[k] = v
			}
			bindings[take.Track] = "off"
			copyProject.Scenes[i].Bindings = bindings
		}
	}
	prepared, err := compileLiveProjectAtRate(s.path, &copyProject, take.Rate)
	if err != nil {
		return err
	}
	owned := true
	defer func() {
		if owned {
			_ = prepared.Engine.Close()
		}
	}()
	listening := &studioListening{Kind: "range", Name: "Recording", StartTick: take.Timeline.PlaybackStartTick, EndTick: take.Timeline.EndTick, Loop: s.vocalLoop != nil}
	r := listening.playbackRange()
	prepared.Range = &r
	fingerprint, err := playSourceHashBytes(s.path, source)
	if err != nil {
		return err
	}
	s.transport.stop()
	countIn, err := capture.DefaultCountIn(take.Rate, take.Timeline.TempoMilli)
	if err != nil {
		return err
	}
	clock := takeClock(take)
	duration := clock.SampleAtTick(take.Timeline.EndTick) - clock.SampleAtTick(take.Timeline.PlaybackStartTick)
	if s.vocalLoop != nil {
		duration *= int64(s.vocalLoop.passes)
	}
	calibration, err := s.transport.recordingCalibration()
	if err != nil {
		return err
	}
	if err = s.captureRecorder.BeginRange(countIn, calibration, duration); err != nil {
		return err
	}
	owned = false // startPrepared consumes the engine on success and failure.
	if err = s.transport.startPrepared(-1, "", &prepared, fingerprint, listening); err != nil {
		return err
	}
	go s.watchVocalCapture(take.ID, s.captureRecorder)
	return nil
}

// Audition filtering keeps the candidate's canonical track/effect/asset
// identities intact. Only the prepared placement list changes; source and mix
// mute/solo controls are never written.
func vocalAuditionProject(p *project.Project, track, mode string) (*project.Project, error) {
	if mode == "" || mode == "context" {
		return p, nil
	}
	if mode != "solo" {
		return nil, fmt.Errorf("hear the phrase with the song or vocal only")
	}
	if p.Arrange == nil {
		return nil, fmt.Errorf("vocal audition requires an arrangement")
	}
	copyProject, arrangement := *p, *p.Arrange
	arrangement.Placements = nil
	for _, place := range p.Arrange.Placements {
		if place.Track == track {
			arrangement.Placements = append(arrangement.Placements, place)
		}
	}
	copyProject.Arrange = &arrangement
	return &copyProject, nil
}

func (s *studio) listenVocalTake(candidate, original []byte, take takejournal.Take, settings *studioRecordingSettings, loop bool) error {
	_, mode, err := vocalPhraseSettings(settings)
	if err != nil {
		return err
	}
	p, err := compileStudioSource(s.path, candidate)
	if err != nil {
		return err
	}
	p, err = vocalAuditionProject(p, take.Track, mode)
	if err != nil {
		return err
	}
	rate, err := s.transport.ensureSampleRate()
	if err != nil {
		return err
	}
	prepared, err := compileLiveProjectAtRate(s.path, p, rate)
	if err != nil {
		return err
	}
	owned := true
	defer func() {
		if owned {
			_ = prepared.Engine.Close()
		}
	}()
	lo, hi := takeCoverage(take)
	if settings != nil {
		if settings.Start != "" {
			lo, err = notationTicks(settings.Start)
			if err != nil {
				return err
			}
		}
		if settings.End != "" {
			hi, err = notationTicks(settings.End)
			if err != nil {
				return err
			}
		}
	}
	listening := &studioListening{Kind: "range", Name: "Take in song", Mode: mode, StartTick: lo, EndTick: hi, Loop: loop}
	r := listening.playbackRange()
	prepared.Range = &r
	fingerprint, err := playSourceHashBytes(s.path, original)
	if err != nil {
		return err
	}
	s.transport.stop()
	owned = false
	return s.transport.startPrepared(-1, "", &prepared, fingerprint, listening)
}
