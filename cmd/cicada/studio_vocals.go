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
	"slices"
	"sort"
	"strings"
	"time"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/host/sampleasset"
	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/fx/ampcab"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type studioRecordingSettings struct {
	Elastic      *bool    `json:"elastic,omitempty"`
	Start        string   `json:"start"`
	End          string   `json:"end"`
	GainDB       *float64 `json:"gainDB,omitempty"`
	AuditionMode string   `json:"auditionMode,omitempty"`
	OffsetMS     float64  `json:"offsetMS,omitempty"`
	FadeMS       *float64 `json:"fadeMS,omitempty"`
	CrossfadeMS  *float64 `json:"crossfadeMS,omitempty"`
	Passes       int      `json:"passes,omitempty"`
}

func validateVocalGain(gainDB float64) error { return edits.ValidateVocalGain(gainDB) }

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
	return edits.ValidateVocalEdits(offsetMS, fadeMS, crossfadeMS)
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

// The comp types live in the edit service, which writes their metadata comment.
type (
	vocalSegment = edits.VocalSegment
	vocalComp    = edits.VocalComp
)

const vocalCompPrefix = edits.VocalCompPrefix

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
		if err := edits.ValidateVocalComp(c); err != nil {
			return nil, err
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

func takeClock(t takejournal.Take) (*project.TempoMap, error) {
	return t.Timeline.Clock(t.Rate)
}
func takeOrigin(t takejournal.Take) int64 {
	if t.FirstBlock == nil {
		return 0
	}
	return int64(t.FirstBlock.RawFrame) - t.FirstBlock.Placement.EngineFrame
}
func takeCoverage(t takejournal.Take) (int64, int64) {
	return t.Coverage()
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
	return &takejournal.Timeline{StartTick: start, EndTick: end, PlaybackStartTick: max(0, start-seq.TicksPerBar), TempoMilli: int64(p.TempoMilli), TempoChanges: project.SongTempoChanges(p)}, nil
}

// vocalTakeSource is the candidate source for selecting a take; see
// vocalTakeEnvelope for the checks and the intent behind it.
func (s *studio) vocalTakeSource(source []byte, t takejournal.Take, settings *studioRecordingSettings) ([]byte, error) {
	candidate, _, err := s.vocalTakeEnvelope(source, t, settings)
	return candidate, err
}

// Source-only compatibility helpers must never silently drop an owned file.
func (s *studio) vocalTakeEnvelope(source []byte, t takejournal.Take, settings *studioRecordingSettings) ([]byte, edits.Envelope, error) {
	result, env, err := s.vocalTakeResult(source, t, settings)
	if err != nil {
		return nil, env, err
	}
	if len(result.Files) != 0 {
		return nil, env, fmt.Errorf("vocal comp changes secondary files; use the project result")
	}
	return result.Source, env, nil
}

// vocalSourceCompiler hands the edit service the plan of the score being
// edited. The candidate is not compiled here: the commit path compiles it with
// its asset, as it always has.
type vocalSourceCompiler struct {
	source []byte
	plan   *edits.Plan
}

func (c vocalSourceCompiler) Compile(source []byte, _ map[string][]byte) (*edits.Plan, error) {
	if bytes.Equal(source, c.source) {
		return c.plan, nil
	}
	return &edits.Plan{Revision: edits.Revision(source)}, nil
}

// Comp audio is a bounded mono print; every source pass remains in the journal.
// Only this track's generated asset/clip/placement is replaced. Older prints
// stay on disk so Undo, Redo, recovery and exported projects retain their audio.
//
// vocalTakeResult checks the take against the score, prints the comp audio,
// and applies the PlaceVocalComp intent. It returns all candidate files and
// the envelope the commit records in the edit log.
func (s *studio) vocalTakeResult(source []byte, t takejournal.Take, settings *studioRecordingSettings) (*edits.Result, edits.Envelope, error) {
	var env edits.Envelope
	gainDB, _, err := vocalPhraseSettings(settings)
	if err != nil {
		return nil, env, err
	}
	p, err := compileStudioSource(s.path, source)
	if err != nil {
		return nil, env, err
	}
	if t.Timeline == nil {
		return nil, env, fmt.Errorf("choose a Session arrangement take")
	}
	comps, err := vocalComps(source)
	if err != nil {
		return nil, env, err
	}
	follow := false
	for _, comp := range comps {
		if comp.Track == t.Track {
			follow = comp.Elastic
		}
	}
	if settings != nil && settings.Elastic != nil {
		follow = *settings.Elastic
	}
	if !follow && (int64(p.TempoMilli) != t.Timeline.TempoMilli || !slices.Equal(project.SongTempoChanges(p), t.Timeline.TempoChanges)) {
		return nil, env, fmt.Errorf("enable Follow song tempo or restore the recording tempo map before selecting this take")
	}
	found := false
	for _, tr := range p.Tracks {
		if tr.ID == t.Track && tr.Kind == "audio" {
			found = true
		}
	}
	if !found {
		return nil, env, fmt.Errorf("take target must be an existing audio track")
	}
	lo, hi := takeCoverage(t)
	if settings != nil {
		if settings.Start != "" {
			lo, err = notationTicks(settings.Start)
			if err != nil {
				return nil, env, err
			}
		}
		if settings.End != "" {
			hi, err = notationTicks(settings.End)
			if err != nil {
				return nil, env, err
			}
		}
	}
	from, to := takeCoverage(t)
	if lo < from || hi > to || hi <= lo || hi > studioArrangementEnd(p) {
		return nil, env, fmt.Errorf("choose a region covered by this take (%s to %s)", listeningPosition(from), listeningPosition(to))
	}
	next := vocalComp{Track: t.Track, Tempo: t.Timeline.TempoMilli, TempoChanges: slices.Clone(t.Timeline.TempoChanges), Elastic: follow}
	if follow {
		next.Tempo, next.TempoChanges = int64(p.TempoMilli), project.SongTempoChanges(p)
	}
	var previous *vocalComp
	for i := range comps {
		if comps[i].Track == t.Track {
			previous = &comps[i]
			next.Segments = comps[i].Segments
			break
		}
	}
	if previous != nil && !follow && (previous.Tempo != next.Tempo || !slices.Equal(previous.TempoChanges, next.TempoChanges)) {
		return nil, env, fmt.Errorf("vocal comp tempo differs from this take")
	}
	segment := vocalSegment{Take: t.ID, Start: lo, End: hi, GainDB: gainDB}
	if settings != nil {
		segment.OffsetMS, segment.FadeMS, segment.CrossfadeMS = settings.OffsetMS, settings.FadeMS, settings.CrossfadeMS
	}
	next.Segments = replaceVocalSegment(next.Segments, segment)
	asset, err := s.printVocalComp(next, t.Rate)
	if err != nil {
		return nil, env, err
	}
	next.Asset = asset.Name
	intent := &edits.PlaceVocalComp{Comp: next, Asset: edits.PrintAsset{Path: asset.Path, SHA256: asset.SHA256, Frames: asset.Frames, RateHz: asset.RateHz}}
	if previous != nil {
		intent.Previous = &edits.VocalPrevious{Asset: previous.Asset}
	}
	opts, err := s.editOptions(source, nil)
	if err != nil {
		return nil, env, err
	}
	opts.Compiler = vocalSourceCompiler{source: source, plan: project.EditPlan(p, source)}
	env = edits.Envelope{Version: edits.EnvelopeVersion, Revision: studioRevision(source), Intents: []edits.Intent{intent}}
	result, err := edits.Apply(source, env, opts)
	if err != nil {
		return nil, env, err
	}
	return result, env, nil
}

func (s *studio) printVocalComp(c vocalComp, rate int) (project.Asset, error) {
	var result project.Asset
	if err := s.openTakes(); err != nil {
		return result, err
	}
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
	if err := edits.ValidateVocalComp(c); err != nil {
		return result, err
	}
	clock, err := project.NewTempoMap(rate, c.Tempo, c.TempoChanges)
	if err != nil {
		return result, err
	}
	first, last := c.Segments[0].Start, c.Segments[len(c.Segments)-1].End
	origin := clock.SampleAtTick(first)
	frames := clock.SampleAtTick(last) - origin
	if frames <= 0 || frames > 16<<20 {
		return result, fmt.Errorf("vocal comp exceeds the 64 MiB mono PCM budget")
	}
	data := monoVocalWAV(rate, frames)
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
		if c.Elastic {
			take, err = s.elasticVocalTake(take, c)
			if err != nil {
				return result, err
			}
		}
		if take.Timeline == nil || take.Track != c.Track || take.Rate != rate || take.Timeline.TempoMilli != c.Tempo || !slices.Equal(take.Timeline.TempoChanges, c.TempoChanges) {
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
	return s.storeVocalPCM(data, rate, frames, "comps")
}

func (s *studio) storeVocalPCM(data []byte, rate int, frames int64, directory string) (project.Asset, error) {
	var result project.Asset
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	result = project.Asset{Name: "vocal-" + hash[:16], Path: "audio/" + directory + "/" + hash + ".wav", SHA256: hash, Format: "wav", Frames: frames, RateHz: rate, Channels: 1, Source: "recorded"}
	root, err := os.OpenRoot(takeRoot(s.path))
	if err != nil {
		return result, err
	}
	defer root.Close()
	if err = root.MkdirAll("audio/"+directory, 0700); err != nil {
		return result, err
	}
	if _, err := root.Stat(result.Path); err == nil {
		return result, takejournal.Verify(root, result.Path, result)
	} else if !os.IsNotExist(err) {
		return result, err
	}
	temporary := fmt.Sprintf("audio/%s/.print-%d.tmp", directory, time.Now().UnixNano())
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
	for _, dir := range []string{"audio/" + directory, "audio", "."} {
		if err = takejournal.SyncDirectory(filepath.Join(takeRoot(s.path), dir)); err != nil {
			return result, err
		}
	}
	return result, nil
}

type automationTakeCommit struct {
	sourceRevision string
	receipt        func([]byte, string, *project.Project)
}

func (s *studio) finishVocalCapture(id string) error {
	if s.captureID != id || s.captureRecorder == nil {
		return nil
	}
	// A concurrent automation pass belongs to this capture's playback player.
	// Freeze it before stopping that player and retain its own recovery draft.
	pass := s.automationRecording
	var draftErr error
	rebase := false
	if pass != nil && pass.CaptureID == id {
		pass.Active = false
		if pass.MonitorFollower != nil && pass.MonitorFollower.broken.Load() {
			pass.Invalid = true
		}
		s.releaseAutomationMonitor(pass)
		draftErr = s.persistAutomationRecording(true)
		if take, err := s.takes.Get(id); err == nil && take.Expected == pass.Revision && checkStudioSourceRevision(s.path, pass.SourceRevision) == nil {
			rebase = true
		}
	}
	defer func() { s.captureElastic = nil }()
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
	// Guard the project sources as well as the main file when the draft may be
	// advanced. An outside edit must never be mistaken for our take commit.
	var commitErr error
	var committedSource []byte
	var committedRevision string
	var committedProject *project.Project
	receipt := func(source []byte, revision string, p *project.Project) {
		committedSource, committedRevision, committedProject = source, revision, p
	}
	if loop != nil {
		if rebase {
			commitErr = s.finishVocalLoop(loop, snapshot, drainErr, automationTakeCommit{pass.SourceRevision, receipt})
		} else {
			commitErr = s.finishVocalLoop(loop, snapshot, drainErr)
		}
	} else {
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
		if rebase {
			commitErr = s.commitTakeFiles(id, take.Expected, nil, nil, nil, pass.SourceRevision, receipt)
		} else {
			commitErr = s.commitTake(id, take.Expected, nil)
		}
	}
	if commitErr != nil {
		return commitErr
	}
	if rebase {
		if committedRevision == "" || committedProject == nil {
			return fmt.Errorf("audio take committed but automation draft remains at its original revision")
		}
		pass.Revision, pass.SourceRevision, pass.Project, pass.CaptureID = studioRevision(committedSource), committedRevision, committedProject, ""
		if err := s.persistAutomationRecording(true); err != nil {
			return err
		}
	}
	return draftErr
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
		if pass := s.automationRecording; pass != nil && pass.Prepared {
			return fmt.Errorf("prepared live amp Write needs a single-pass recording range; choose a range or discard the preparation")
		}
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
	pass := s.automationRecording
	var initialTargets ampcab.Params
	if pass != nil && pass.Prepared {
		if s.vocalLoop != nil || !strings.HasPrefix(pass.Path, take.Track+".") || pass.Revision != take.Expected {
			return fmt.Errorf("prepared live amp Write needs its matching track and one recording pass")
		}
		if err := checkStudioSourceRevision(s.path, pass.SourceRevision); err != nil {
			return err
		}
		initialTargets, err = initialAmpTargets(p, take.Track, take.Timeline.PlaybackStartTick)
		if err != nil {
			return err
		}
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
	clock, err := takeClock(take)
	if err != nil {
		return err
	}
	countInBars := 1
	if p.Live != nil && p.Live.CountInBars != nil {
		countInBars = *p.Live.CountInBars
	}
	countIn, err := capture.NewCountIn(take.Rate, clock.ClockAtTick(take.Timeline.PlaybackStartTick).BPMMilli, countInBars)
	if err != nil {
		return err
	}
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
	var beforePlay []func(*liveplay.Player, studioAudioDevice) error
	if pass != nil && pass.Prepared {
		beforePlay = append(beforePlay, func(player *liveplay.Player, audio studioAudioDevice) error {
			device, ok := audio.(studioAmpFollowDevice)
			if !ok {
				return fmt.Errorf("this audio backend cannot bind the live amp monitor")
			}
			target, err := player.ResolvePreviewTrack(take.Track)
			if err != nil {
				return err
			}
			follower := &studioAmpFollower{player: player, target: target, initial: initialTargets}
			if err = device.SetAmpFollower(follower); err != nil {
				return err
			}
			initial := 0.0
			for _, address := range project.ParamAddresses(p) {
				if address.Address == pass.Path {
					if n, ok := address.Value.(float64); ok {
						initial = n
					}
				}
			}
			for _, lane := range p.Automation {
				if lane.Path == pass.Path {
					if n, ok := project.AutomationValue(p, lane, take.Timeline.StartTick); ok {
						initial = n
					}
				}
			}
			param := kernel.ParamAmpDrive
			if strings.HasSuffix(pass.Path, ".amp_tone") {
				param = kernel.ParamAmpTone
			} else if strings.HasSuffix(pass.Path, ".amp_output") {
				param = kernel.ParamAmpOutput
			}
			version, err := player.SetResolvedPreview(target, param, float32(initial))
			if err != nil {
				_ = device.SetAmpFollower(nil)
				return err
			}
			pass.Project, pass.CaptureID, pass.MonitorFollower = p, take.ID, follower
			pass.Preview = studioAutomationPreview{Player: player, Version: version}
			pass.Points = []recordedAutomationPoint{{take.Timeline.StartTick, initial}}
			pass.Prepared, pass.Active, pass.Epoch = false, true, 0
			s.transport.monitorWrite = true
			if err = s.persistAutomationRecording(true); err != nil {
				_ = device.SetAmpFollower(nil)
				s.transport.monitorWrite = false
				return err
			}
			return nil
		})
	}
	if err = s.transport.startPrepared(-1, "", &prepared, fingerprint, listening, beforePlay...); err != nil {
		if pass != nil && pass.Monitor {
			pass.Prepared, pass.Active, pass.CaptureID, pass.MonitorFollower = true, false, "", nil
			pass.Preview = studioAutomationPreview{}
			_ = s.persistAutomationRecording(true)
			s.transport.stop()
			_ = s.transport.disarmCapture()
			_ = s.captureRecorder.Close()
			s.captureID, s.captureRecorder, s.vocalLoop = "", nil, nil
		}
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

func (s *studio) listenVocalTake(candidate, original []byte, take takejournal.Take, settings *studioRecordingSettings, loop bool, files ...edits.File) error {
	_, mode, err := vocalPhraseSettings(settings)
	if err != nil {
		return err
	}
	overrides := make(map[string][]byte, len(files))
	for _, file := range files {
		overrides[file.Path] = file.After
	}
	p, err := compileStudioSourceWithOverrides(s.path, candidate, overrides)
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
