// Package render contains the offline PCM24 WAV target.
package render

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sort"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/acid"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type Options struct {
	SampleRate int
	Bars       int // zero renders the full arrangement
	TailSec    float64
}

type Report struct {
	SampleRate      int
	Bars            int
	Frames          int64
	Peak            float32
	OutputPeak      float32
	PreLimiterOvers int64
	CeilingSamples  int64
	ClippedSamples  int64
	TailFrames      int64
	writtenFrames   int64
	skipFrames      int
}

type trackRuntime struct {
	name           string
	voice          monoVoice
	mixer          mix.Track
	insert         *fx.Drive
	align          *mix.Delay
	drums          *drum.Kit
	drumPatterns   map[string][drum.LaneCount]*seq.Pattern
	activeDrums    [drum.LaneCount]*seq.Pattern
	patterns       map[string]seq.Pattern
	currentName    string
	current        seq.Pattern
	active         *seq.Pattern
	generation     uint64
	activeGen      uint64
	activeNoteID   int64
	pending        seq.Pattern
	pendingGen     uint64
	hasPendingGate bool
	transition     sceneTransition
	slideFrom      int64
	slideAt        int64
}

type sceneTransition struct {
	valid, carry   bool
	sourceNoteID   int64
	boundarySample int64
	targetSample   int64
	release        seq.Event
}

type monoVoice interface {
	NoteOn(note, velocity uint8, accent, slide bool)
	NoteOff()
	Next() float32
}

type customVoice struct{ *graph.Voice }

func (voice customVoice) NoteOn(note, velocity uint8, _ bool, slide bool) {
	voice.Voice.NoteOn(note, velocity, slide)
}

type acidVoice struct{ *acid.Voice }

func (voice acidVoice) NoteOn(note, velocity uint8, accent, slide bool) {
	voice.Voice.NoteOn(note, accent, slide, velocity)
}

type scheduled struct {
	track      int
	lane       drum.Lane
	generation uint64
	event      seq.Event
}

// WAV renders the song arrangement from a valid score.
func WAV(score *notation.Score, opts Options, writer io.Writer) (Report, error) {
	var report Report
	if score == nil {
		return report, fmt.Errorf("nil score")
	}
	if opts.SampleRate == 0 {
		opts.SampleRate = 48_000
	}
	if math.IsNaN(opts.TailSec) || math.IsInf(opts.TailSec, 0) || opts.TailSec < 0 || opts.TailSec > 10 {
		return report, fmt.Errorf("tail must be 0 to 10 seconds")
	}
	clock, err := seq.NewClock(opts.SampleRate, score.TempoMilli)
	if err != nil {
		return report, err
	}
	if len(score.Song) == 0 {
		return report, fmt.Errorf("score has no song arrangement")
	}
	for _, entry := range score.Song {
		report.Bars += entry.Bars
	}
	if opts.Bars < 0 || opts.Bars > report.Bars {
		return report, fmt.Errorf("requested bars must be 0 to song length")
	}
	if opts.Bars > 0 {
		report.Bars = opts.Bars
	}
	if report.Bars < 1 || report.Bars > 256 {
		return report, fmt.Errorf("render supports 1 to 256 bars")
	}
	semantic, diagnostics := project.FromScore(score)
	if semantic == nil {
		for _, diagnostic := range diagnostics {
			if diagnostic.Severity == "error" {
				return report, fmt.Errorf("%s: %s", diagnostic.Code, diagnostic.Message)
			}
		}
		return report, fmt.Errorf("score cannot compile to a Cicada project")
	}
	report.SampleRate = opts.SampleRate
	report.TailFrames = int64(math.Round(opts.TailSec * float64(opts.SampleRate)))
	report.Frames = clock.SampleAtTick(int64(report.Bars)*seq.TicksPerBar) + report.TailFrames
	dataBytes := report.Frames * 6
	if dataBytes > int64(^uint32(0))-60 {
		return report, fmt.Errorf("WAV exceeds RIFF size limit")
	}
	tracks, err := compileTracks(score, semantic, opts.SampleRate)
	if err != nil {
		return report, err
	}
	insertLatency := 0
	for i := range tracks {
		if tracks[i].insert != nil {
			insertLatency = tracks[i].insert.LatencyFrames()
			report.skipFrames = insertLatency
			break
		}
	}
	limiter, err := mix.NewLimiter(opts.SampleRate)
	if err != nil {
		return report, err
	}
	if err := writeHeader(writer, opts.SampleRate, uint32(dataBytes)); err != nil {
		return report, err
	}
	var eventBuf [128]seq.Event
	block := make([]byte, 4096*6)
	var events []scheduled
	var position int64
	bar := 0
	for _, entry := range score.Song {
		if bar >= report.Bars {
			break
		}
		scene := findScene(score, entry.Scene)
		if scene == nil {
			return report, fmt.Errorf("unknown scene %s", entry.Scene)
		}
		for range entry.Bars {
			if bar >= report.Bars {
				break
			}
			if err := applyScene(tracks, scene); err != nil {
				return report, err
			}
			var nextScene *notation.Scene
			if bar+1 < report.Bars {
				nextScene = sceneAtBar(score, bar+1)
			}
			planSceneTransitions(tracks, nextScene, int64(bar+1)*seq.TicksPerBar, clock)
			end := clock.SampleAtTick(int64(bar+1) * seq.TicksPerBar)
			for position < end {
				frames := 4096
				if position+int64(frames) > end {
					frames = int(end - position)
				}
				events = events[:0]
				for ti := range tracks {
					if tracks[ti].drums != nil {
						for lane, pattern := range tracks[ti].activeDrums {
							if pattern == nil {
								continue
							}
							n, overflow := seq.EventsInBlock(pattern, clock, uint8(ti), uint8(lane), position, frames, eventBuf[:])
							if overflow {
								return report, fmt.Errorf("too many drum events in render block")
							}
							for _, event := range eventBuf[:n] {
								events = append(events, scheduled{track: ti, lane: drum.Lane(lane), event: event})
							}
						}
						continue
					}
					if tracks[ti].active == nil {
						continue
					}
					n, overflow := seq.EventsWithGatesInBlock(tracks[ti].active, clock, uint8(ti), 0, position, frames, eventBuf[:])
					if overflow {
						return report, fmt.Errorf("too many events in render block")
					}
					transition := tracks[ti].transition
					for _, event := range eventBuf[:n] {
						if transition.valid && event.Kind == seq.NoteOff && event.NoteID == transition.sourceNoteID {
							continue
						}
						if event.Kind == seq.NoteOn && tracks[ti].slideFrom != 0 && tracks[ti].activeNoteID == tracks[ti].slideFrom && event.Sample == tracks[ti].slideAt {
							event.Slide = true
						}
						events = append(events, scheduled{track: ti, generation: tracks[ti].generation, event: event})
					}
					if transition.valid && !transition.carry && transition.release.Sample >= position && transition.release.Sample < position+int64(frames) {
						events = append(events, scheduled{track: ti, generation: tracks[ti].generation, event: transition.release})
					}
					if tracks[ti].hasPendingGate {
						n, overflow = seq.EventsWithGatesInBlock(&tracks[ti].pending, clock, uint8(ti), 0, position, frames, eventBuf[:])
						if overflow {
							return report, fmt.Errorf("too many pending gate events in render block")
						}
						for _, event := range eventBuf[:n] {
							if event.Kind == seq.NoteOff {
								events = append(events, scheduled{track: ti, generation: tracks[ti].pendingGen, event: event})
							}
						}
					}
				}
				sort.Slice(events, func(i, j int) bool {
					if events[i].event.Sample != events[j].event.Sample {
						return events[i].event.Sample < events[j].event.Sample
					}
					if events[i].event.Kind != events[j].event.Kind {
						return events[i].event.Kind == seq.NoteOff
					}
					if events[i].track != events[j].track {
						return events[i].track < events[j].track
					}
					if events[i].lane != events[j].lane {
						priority := func(lane drum.Lane) int {
							if lane == drum.OH {
								return int(drum.CH)
							}
							if lane == drum.CH {
								return int(drum.OH)
							}
							return int(lane)
						}
						return priority(events[i].lane) < priority(events[j].lane)
					}
					return events[i].event.NoteID < events[j].event.NoteID
				})
				if err := renderBlock(writer, tracks, limiter, events, position, frames, block, &report); err != nil {
					return report, err
				}
				position += int64(frames)
			}
			bar++
		}
	}
	for ti := range tracks {
		if tracks[ti].drums != nil {
			for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
				tracks[ti].drums.NoteOff(lane)
			}
		} else {
			tracks[ti].voice.NoteOff()
		}
		tracks[ti].activeGen = 0
	}
	for position < report.Frames {
		frames := 4096
		if position+int64(frames) > report.Frames {
			frames = int(report.Frames - position)
		}
		if err := renderBlock(writer, tracks, limiter, nil, position, frames, block, &report); err != nil {
			return report, err
		}
		position += int64(frames)
	}
	if insertLatency != 0 {
		// Drive and the aligned dry tracks have the same 15-frame latency.
		// Drain it, then omit the initial 15 silent output frames so the WAV
		// remains aligned to the score and has exactly report.Frames frames.
		if err := renderBlock(writer, tracks, limiter, nil, position, insertLatency, block, &report); err != nil {
			return report, err
		}
	}
	if err := flushLimiter(writer, limiter, block, &report); err != nil {
		return report, err
	}
	if report.writtenFrames != report.Frames {
		return report, fmt.Errorf("limiter wrote %d frames, expected %d", report.writtenFrames, report.Frames)
	}
	if err := writeMetadata(writer, uint32(score.TempoMilli), uint32(report.Bars), uint32(report.TailFrames)); err != nil {
		return report, err
	}
	return report, nil
}

func compileTracks(score *notation.Score, semantic *project.Project, sampleRate int) ([]trackRuntime, error) {
	programs := make(map[string]*instrument.Program, len(score.Instruments))
	for _, definition := range score.Instruments {
		program, ds := instrument.Compile(definition)
		if len(ds) > 0 {
			return nil, fmt.Errorf("instrument %s: %s", definition.Name, ds[0].Message)
		}
		programs[definition.Name] = program
	}
	authoredKits := make(map[string]project.Kit, len(score.Kits))
	for _, definition := range score.Kits {
		kit := project.Kit{ID: definition.Name, Lanes: map[string]string{}}
		for _, binding := range definition.Bindings {
			kit.Lanes[binding.Lane] = binding.Target
		}
		authoredKits[kit.ID] = kit
	}
	tracks := make([]trackRuntime, 0, len(score.Tracks))
	for _, source := range score.Tracks {
		mixerParams, err := project.CompileMixerParams(source)
		if err != nil {
			return nil, fmt.Errorf("track %s: %w", source.Name, err)
		}
		trackMix := mix.NewTrack(mixerParams.GainDB, mixerParams.Pan, mixerParams.Mute)
		kitDefinition, isAuthoredKit := authoredKits[source.Kind]
		if source.Kind == "drums" || isAuthoredKit {
			kit, err := drum.New(sampleRate, uint32(score.Seed))
			if err != nil {
				return nil, err
			}
			if isAuthoredKit {
				bindings, err := project.CompileKit(kitDefinition, programs)
				if err != nil {
					return nil, err
				}
				for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
					binding := bindings[lane]
					switch binding.Kind {
					case engine.KitLaneOff:
						err = kit.Disable(lane)
					case engine.KitLaneBuiltin:
						err = kit.SetRecipe(lane, binding.Recipe)
					case engine.KitLaneGraph:
						err = kit.SetGraph(lane, binding.Program)
					}
					if err != nil {
						return nil, fmt.Errorf("track %s lane %s: %w", source.Name, drum.Names[lane], err)
					}
				}
			} else {
				params, err := project.CompileDrumParams(source)
				if err != nil {
					return nil, fmt.Errorf("track %s: %w", source.Name, err)
				}
				for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
					if err := kit.SetParams(lane, params[lane]); err != nil {
						return nil, err
					}
				}
			}
			track := trackRuntime{name: source.Name, mixer: trackMix, drums: kit, drumPatterns: map[string][drum.LaneCount]*seq.Pattern{}}
			for _, pattern := range score.Patterns {
				if pattern.Kind != "drums" {
					continue
				}
				compiled, err := project.CompilePattern(score, pattern, source)
				if err != nil {
					return nil, err
				}
				var lanes [drum.LaneCount]*seq.Pattern
				for _, part := range compiled {
					for lane, name := range drum.Names {
						if part.Lane == name {
							p := part.Pattern
							lanes[lane] = &p
							break
						}
					}
				}
				track.drumPatterns[pattern.Name] = lanes
			}
			tracks = append(tracks, track)
			continue
		}
		if source.Kind == "acid" {
			params, err := project.CompileAcidParams(source)
			if err != nil {
				return nil, fmt.Errorf("track %s: %w", source.Name, err)
			}
			voice, err := acid.New(sampleRate)
			if err != nil {
				return nil, err
			}
			if err := voice.SetParams(params); err != nil {
				return nil, err
			}
			track := trackRuntime{name: source.Name, mixer: trackMix, voice: acidVoice{voice}, patterns: map[string]seq.Pattern{}}
			for _, pattern := range score.Patterns {
				if pattern.Kind != "acid" && pattern.Kind != "notes" {
					continue
				}
				compiled, err := project.CompilePattern(score, pattern, source)
				if err != nil {
					return nil, err
				}
				track.patterns[pattern.Name] = compiled[0].Pattern
			}
			tracks = append(tracks, track)
			continue
		}
		program := programs[source.Kind]
		if program == nil {
			return nil, fmt.Errorf("audio renderer does not yet implement %s track %s", source.Kind, source.Name)
		}
		if program.Mode != "mono" {
			return nil, fmt.Errorf("audio renderer does not yet implement poly voices")
		}
		overrides := make(map[string]string, len(source.Params))
		for _, param := range source.Params {
			if param.Name == "level" || param.Name == "pan" || param.Name == "insert" {
				continue
			}
			overrides[param.Name] = param.Value
		}
		kernelProgram, err := instrument.Lower(program, overrides)
		if err != nil {
			return nil, fmt.Errorf("track %s: %w", source.Name, err)
		}
		voice, err := graph.NewVoice(kernelProgram, sampleRate)
		if err != nil {
			return nil, err
		}
		track := trackRuntime{name: source.Name, mixer: trackMix, voice: customVoice{voice}, patterns: map[string]seq.Pattern{}}
		for _, pattern := range score.Patterns {
			if pattern.Kind != "notes" {
				continue
			}
			compiled, err := project.CompilePattern(score, pattern, source)
			if err != nil {
				return nil, err
			}
			track.patterns[pattern.Name] = compiled[0].Pattern
		}
		tracks = append(tracks, track)
	}
	var driveParams *fx.DriveParams
	for _, effect := range semantic.Effects {
		if effect.ID == "drive" {
			params, err := project.DriveParamsFromValues(effect.Params)
			if err != nil {
				return nil, err
			}
			driveParams = &params
		}
	}
	if driveParams != nil {
		hasInsert := false
		for _, track := range semantic.Tracks {
			if track.Mixer.Insert == "drive" {
				hasInsert = true
				break
			}
		}
		if hasInsert {
			for i := range tracks {
				if semantic.Tracks[i].Mixer.Insert == "drive" {
					insert, err := fx.NewDrive(sampleRate)
					if err != nil {
						return nil, err
					}
					if err := insert.SetParams(*driveParams); err != nil {
						return nil, err
					}
					insert.Reset()
					tracks[i].insert = insert
				} else {
					align, err := mix.NewDelay(15)
					if err != nil {
						return nil, err
					}
					tracks[i].align = align
				}
			}
		}
	}
	return tracks, nil
}

func findScene(score *notation.Score, name string) *notation.Scene {
	for i := range score.Scenes {
		if score.Scenes[i].Name == name {
			return &score.Scenes[i]
		}
	}
	return nil
}

func sceneAtBar(score *notation.Score, bar int) *notation.Scene {
	for _, entry := range score.Song {
		if bar < entry.Bars {
			return findScene(score, entry.Scene)
		}
		bar -= entry.Bars
	}
	return nil
}

func planSceneTransitions(tracks []trackRuntime, next *notation.Scene, boundaryTick int64, clock seq.Clock) {
	for ti := range tracks {
		track := &tracks[ti]
		track.transition = sceneTransition{}
		if next == nil || track.active == nil || track.drums != nil {
			continue
		}
		for _, binding := range next.Bindings {
			if binding.Track != track.name || binding.Pattern == "keep" || binding.Pattern == "off" || binding.Pattern == track.currentName {
				continue
			}
			target, ok := track.patterns[binding.Pattern]
			if !ok {
				continue // applyScene reports the invalid binding at the boundary
			}
			track.transition = sceneTransitionFor(track.active, &target, uint8(ti), boundaryTick, clock)
			break
		}
	}
}

func sceneTransitionFor(source, target *seq.Pattern, track uint8, boundaryTick int64, clock seq.Clock) sceneTransition {
	if boundaryTick < seq.TicksPerStep || boundaryTick%seq.TicksPerStep != 0 {
		return sceneTransition{}
	}
	lastStep := boundaryTick/seq.TicksPerStep - 1
	index := uint8(lastStep % int64(source.Len))
	step, err := seq.UnpackStep(source.Steps[index])
	if err != nil || !step.Gate || !step.Slide || !seq.ProbabilityHit(step.Probability, source.Seed, track, 0, lastStep/int64(source.Len), index) {
		return sceneTransition{}
	}
	startTick := lastStep * seq.TicksPerStep
	if lastStep&1 != 0 {
		startTick += seq.SwingDelayTicks(source.SwingPermille)
	}
	onset := seq.RatchetTick(startTick, boundaryTick, step.Ratchet, step.Ratchet-1)
	gateTicks := (boundaryTick - onset) * int64(source.GatePercent) / 100
	if gateTicks < 30 {
		gateTicks = 30
	}
	offTick := onset + gateTicks
	sourceNoteID := lastStep*8 + int64(step.Ratchet)
	targetStep := boundaryTick / seq.TicksPerStep
	targetIndex := uint8(targetStep % int64(target.Len))
	next, err := seq.UnpackStep(target.Steps[targetIndex])
	carry := err == nil && next.Gate && !next.Tie && seq.ProbabilityHit(next.Probability, target.Seed, track, 0, targetStep/int64(target.Len), targetIndex)
	return sceneTransition{
		valid: true, carry: carry, sourceNoteID: sourceNoteID,
		boundarySample: clock.SampleAtTick(boundaryTick), targetSample: clock.SampleAtTick(boundaryTick),
		release: seq.Event{Kind: seq.NoteOff, NoteID: sourceNoteID, Tick: offTick, Sample: clock.SampleAtTick(offTick), Track: track},
	}
}

func applyScene(tracks []trackRuntime, scene *notation.Scene) error {
	for _, binding := range scene.Bindings {
		for ti := range tracks {
			if tracks[ti].name != binding.Track {
				continue
			}
			switch binding.Pattern {
			case "keep":
			case "off":
				if tracks[ti].drums != nil {
					tracks[ti].activeDrums = [drum.LaneCount]*seq.Pattern{}
					for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
						tracks[ti].drums.NoteOff(lane)
					}
					tracks[ti].currentName = ""
					continue
				}
				tracks[ti].active = nil
				tracks[ti].currentName = ""
				tracks[ti].hasPendingGate = false
				tracks[ti].activeGen = 0
				tracks[ti].voice.NoteOff()
			default:
				if tracks[ti].currentName == binding.Pattern {
					continue
				}
				if tracks[ti].drums != nil {
					lanes, ok := tracks[ti].drumPatterns[binding.Pattern]
					if !ok {
						return fmt.Errorf("track %s cannot play pattern %s", binding.Track, binding.Pattern)
					}
					tracks[ti].activeDrums = lanes
					tracks[ti].currentName = binding.Pattern
					continue
				}
				pattern, ok := tracks[ti].patterns[binding.Pattern]
				if !ok {
					return fmt.Errorf("track %s cannot play pattern %s", binding.Track, binding.Pattern)
				}
				transition := tracks[ti].transition
				if transition.valid && tracks[ti].activeNoteID == transition.sourceNoteID && tracks[ti].activeGen == tracks[ti].generation {
					if transition.carry {
						tracks[ti].slideFrom, tracks[ti].slideAt = transition.sourceNoteID, transition.targetSample
					} else if transition.release.Sample <= transition.boundarySample {
						tracks[ti].voice.NoteOff()
						tracks[ti].activeGen = 0
					}
				}
				if tracks[ti].active != nil && tracks[ti].activeGen == tracks[ti].generation {
					tracks[ti].pending = tracks[ti].current
					tracks[ti].pendingGen = tracks[ti].generation
					tracks[ti].hasPendingGate = true
				}
				tracks[ti].generation++
				tracks[ti].currentName = binding.Pattern
				tracks[ti].current = pattern
				tracks[ti].active = &tracks[ti].current
			}
		}
	}
	return nil
}

func renderBlock(w io.Writer, tracks []trackRuntime, limiter *mix.Limiter, events []scheduled, start int64, frames int, buffer []byte, report *Report) error {
	eventIndex := 0
	outFrames := 0
	for frame := 0; frame < frames; frame++ {
		sample := start + int64(frame)
		for eventIndex < len(events) && events[eventIndex].event.Sample == sample {
			event := events[eventIndex]
			track := &tracks[event.track]
			if track.drums != nil {
				track.drums.Hit(event.lane, event.event.Velocity, event.event.Accent)
				eventIndex++
				continue
			}
			if event.event.Kind == seq.NoteOff {
				if track.activeGen == event.generation && track.activeNoteID == event.event.NoteID {
					track.voice.NoteOff()
					track.activeGen = 0
					track.hasPendingGate = false
				}
			} else {
				track.voice.NoteOn(event.event.Note, event.event.Velocity, event.event.Accent, event.event.Slide)
				track.activeGen = event.generation
				track.activeNoteID = event.event.NoteID
				track.hasPendingGate = false
				track.slideFrom, track.slideAt = 0, 0
			}
			eventIndex++
		}
		var dry mix.Dry
		for ti := range tracks {
			var l, r float32
			if tracks[ti].drums != nil {
				l, r = tracks[ti].drums.NextStereo()
				if tracks[ti].drums.Fault() {
					return fmt.Errorf("drum DSP fault on %s", tracks[ti].name)
				}
			} else {
				mono := tracks[ti].voice.Next()
				l, r = mono, mono
			}
			if tracks[ti].insert != nil {
				l, r = tracks[ti].insert.Process(l, r)
				if tracks[ti].insert.Fault() {
					return fmt.Errorf("drive DSP fault on %s", tracks[ti].name)
				}
			} else if tracks[ti].align != nil {
				l, r = tracks[ti].align.Process(l, r)
			}
			dry.Add(l, r, tracks[ti].mixer)
		}
		left, right := dry.Music()
		if abs := float32(math.Max(math.Abs(float64(left)), math.Abs(float64(right)))); abs > report.Peak {
			report.Peak = abs
		}
		if math.Abs(float64(left)) > limiter.Ceiling() {
			report.PreLimiterOvers++
		}
		if math.Abs(float64(right)) > limiter.Ceiling() {
			report.PreLimiterOvers++
		}
		outL, outR, ready := limiter.Process(left, right)
		if limiter.Fault() {
			return fmt.Errorf("non-finite master input")
		}
		if !ready {
			continue
		}
		if report.skipFrames > 0 {
			report.skipFrames--
			continue
		}
		writePCMFrame(buffer[outFrames*6:], outL, outR, limiter.Ceiling(), report)
		outFrames++
	}
	if outFrames == 0 {
		return nil
	}
	n, err := w.Write(buffer[:outFrames*6])
	if err != nil {
		return err
	}
	if n != outFrames*6 {
		return io.ErrShortWrite
	}
	report.writtenFrames += int64(outFrames)
	return nil
}

func flushLimiter(w io.Writer, limiter *mix.Limiter, buffer []byte, report *Report) error {
	frames := limiter.LatencyFrames()
	for i := 0; i < frames; i++ {
		left, right, ready := limiter.Process(0, 0)
		if !ready || limiter.Fault() {
			return fmt.Errorf("master limiter failed while flushing")
		}
		writePCMFrame(buffer[i*6:], left, right, limiter.Ceiling(), report)
	}
	n, err := w.Write(buffer[:frames*6])
	if err != nil {
		return err
	}
	if n != frames*6 {
		return io.ErrShortWrite
	}
	report.writtenFrames += int64(frames)
	return nil
}

func writePCMFrame(dst []byte, left, right float32, ceiling float64, report *Report) {
	for channel, value := range [...]float32{left, right} {
		abs := math.Abs(float64(value))
		if abs > float64(report.OutputPeak) {
			report.OutputPeak = float32(abs)
		}
		if abs >= ceiling-1e-7 {
			report.CeilingSamples++
		}
		if abs > 1 {
			report.ClippedSamples++
		}
		pcm := int32(math.Round(float64(value) * 8388607))
		index := channel * 3
		dst[index] = byte(pcm)
		dst[index+1] = byte(pcm >> 8)
		dst[index+2] = byte(pcm >> 16)
	}
}

func writeHeader(w io.Writer, sampleRate int, dataBytes uint32) error {
	var header [44]byte
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], dataBytes+60)
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], 2)
	binary.LittleEndian.PutUint32(header[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(header[28:32], uint32(sampleRate*6))
	binary.LittleEndian.PutUint16(header[32:34], 6)
	binary.LittleEndian.PutUint16(header[34:36], 24)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], dataBytes)
	n, err := w.Write(header[:])
	if err != nil {
		return err
	}
	if n != len(header) {
		return io.ErrShortWrite
	}
	return nil
}

// The trailing cica chunk makes musical duration independently verifiable.
// Standard WAV readers skip this private chunk after the PCM data.
func writeMetadata(w io.Writer, tempoMilli, bars, tailFrames uint32) error {
	var chunk [24]byte
	copy(chunk[:4], "cica")
	binary.LittleEndian.PutUint32(chunk[4:8], 16)
	binary.LittleEndian.PutUint32(chunk[8:12], 1)
	binary.LittleEndian.PutUint32(chunk[12:16], tempoMilli)
	binary.LittleEndian.PutUint32(chunk[16:20], bars)
	binary.LittleEndian.PutUint32(chunk[20:24], tailFrames)
	n, err := w.Write(chunk[:])
	if err != nil {
		return err
	}
	if n != len(chunk) {
		return io.ErrShortWrite
	}
	return nil
}
