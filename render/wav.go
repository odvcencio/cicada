// Package render contains the offline WAV and stems targets.
package render

import (
	"encoding/binary"
	"fmt"
	"io"
	"m31labs.dev/cicada/host/sampleasset"
	"math"
	"slices"
	"sort"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/acid"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/kernel/voice/guitar"
	"m31labs.dev/cicada/kernel/voice/modal"
	"m31labs.dev/cicada/kernel/voice/piano"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type Options struct {
	AssetRoot       string // project root used during resident audio preparation
	AssetDir        string // host-only root for pinned sample packs
	SamplerBaseline bool   // listening comparison: fixed layer/take and 2 ms release
	SampleRate      int
	Bits            int // 16, 24, or 32-bit IEEE float; zero defaults to 24
	Bars            int // zero renders from From through the song end
	From            int // zero-based start bar; zero starts at the song beginning
	TailSec         float64
	Dither          *bool   // nil enables deterministic TPDF on integer formats
	Normalize       bool    // peak normalize the post-limiter output to -1 dBFS
	Block           int     // zero selects 4096 frames
	MasterGainDB    float64 // export gain after authored DSP, before the safety limiter
	MasterBiasL     float32 // export DC correction after authored DSP, before export gain
	MasterBiasR     float32 // export DC correction after authored DSP, before export gain
}

type Report struct {
	SampleRate                int
	Bars                      int
	From                      int
	Frames                    int64
	Peak                      float32
	OutputPeak                float32
	PreLimiterOvers           int64
	CeilingSamples            int64
	ClippedSamples            int64
	TailFrames                int64
	MasterGainDB              float64
	MaxLimiterGainReductionDB float64
	writtenFrames             int64
	skipFrames                int
	rangeSkip                 int64
	metricStart               int64
	metricEnd                 int64
}

type trackRuntime struct {
	prepared       *preparedVoice
	legacyPoly     *graph.Poly
	audioSlots     map[string]uint8
	name           string
	voice          monoVoice
	poly           *graph.Pool
	mixer          mix.Track
	insert         *fx.Drive
	align          *mix.Delay
	sendA          float32
	sendB          float32
	sendPreGain    float32
	sendAPre       bool
	sendBPre       bool
	solo           bool
	muted          bool
	busSFX         bool
	drums          *drum.Kit
	drumPatterns   map[string][drum.LaneCount]*seq.Pattern
	activeDrums    [drum.LaneCount]*seq.Pattern
	patterns       map[string]seq.Pattern
	patternSlots   map[string]uint8
	currentName    string
	currentSlot    uint8
	current        seq.Pattern
	active         *seq.Pattern
	generation     uint64
	activeGen      uint64
	activeNoteID   int64
	pending        seq.Pattern
	pendingGen     uint64
	pendingSlot    uint8
	hasPendingGate bool
	parameters     *sceneTrackParameters
	transition     sceneTransition
	slideFrom      int64
	slideAt        int64
}

type busMixerState struct {
	masterProcessor      engine.StereoProcessor
	outputGain           float32
	musicMute, musicSolo bool
	sfxMute, sfxSolo     bool
	masterMute           bool
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

type polyVoice struct{ *graph.Pool }

func (voice polyVoice) NoteOn(note, velocity uint8, accent, slide bool) {
	panic("polyphonic rendering requires a cohort event")
}
func (voice polyVoice) NoteOff() { voice.Pool.ReleaseAll() }

type customVoice struct{ *graph.Voice }

type legacyPolyVoice struct{ *graph.Poly }

func (v legacyPolyVoice) NoteOn(note, velocity uint8, _ bool, slide bool) {
	v.Poly.NoteOn(note, velocity, slide)
}
func (v legacyPolyVoice) Next() float32 { return 0 } // stereo output is read directly

func (voice customVoice) NoteOn(note, velocity uint8, _ bool, slide bool) {
	voice.Voice.NoteOn(note, velocity, slide)
}

type modalVoice struct{ *modal.Voice }

func (voice modalVoice) NoteOn(note, velocity uint8, _ bool, slide bool) {
	voice.Voice.NoteOn(note, velocity, slide)
}

type acidVoice struct{ *acid.Voice }

type preparedVoice struct {
	engine.StereoVoice
	err error
}

func (v *preparedVoice) NoteOn(note, velocity uint8, _, _ bool) {
	v.err = v.StereoVoice.NoteOn(note, velocity)
}
func (v *preparedVoice) Next() float32 { return 0 } // stereo output is read directly

func (voice acidVoice) NoteOn(note, velocity uint8, accent, slide bool) {
	voice.Voice.NoteOn(note, accent, slide, velocity)
}

type pianoVoice struct {
	*piano.Instrument
	notes [4]uint8
	count uint8
}

func (voice *pianoVoice) NoteOn(note, velocity uint8, _ bool, slide bool) {
	_ = voice.NoteChord([4]uint8{note}, 1, velocity, slide)
}

func (voice *pianoVoice) NoteChord(notes [4]uint8, count, velocity uint8, slide bool) error {
	if slide {
		voice.NoteOff()
	}
	voice.notes, voice.count = notes, count
	for n := uint8(0); n < count; n++ {
		if err := voice.Instrument.NoteOn(notes[n], velocity); err != nil {
			return err
		}
	}
	return nil
}

func (voice *pianoVoice) NoteOff() {
	for n := uint8(0); n < voice.count; n++ {
		voice.Instrument.NoteOff(voice.notes[n])
	}
	voice.count = 0
}

func (voice *pianoVoice) Next() float32 {
	left, right := voice.NextStereo()
	return (left + right) * .5
}

type scheduled struct {
	track      int
	lane       drum.Lane
	generation uint64
	event      seq.Event
}

// WAV renders the song arrangement from a valid score.
func WAV(score *notation.Score, opts Options, writer io.Writer) (Report, error) {
	return renderWithOptions(score, opts, writer, "")
}

func renderWithOptions(score *notation.Score, opts Options, writer io.Writer, stemsDir string) (Report, error) {
	gain := float32(1)
	if opts.Normalize {
		preview := opts
		preview.Normalize = false
		report, err := renderWAV(score, preview, io.Discard, "", 1)
		if err != nil {
			return report, err
		}
		if report.OutputPeak > 0 {
			gain = float32(math.Pow(10, -1.0/20) / float64(report.OutputPeak))
		}
	}
	return renderWAV(score, opts, writer, stemsDir, gain)
}

func hasResidentSampler(score *notation.Score) bool {
	for _, sampler := range score.Samplers {
		if sampler.Asset != "" {
			return true
		}
	}
	return false
}

func renderWAV(score *notation.Score, opts Options, writer io.Writer, stemsDir string, outputGain float32) (Report, error) {
	if opts.AssetRoot == "" {
		opts.AssetRoot = opts.AssetDir
	}
	if opts.AssetDir == "" {
		opts.AssetDir = opts.AssetRoot
	}
	if score != nil && score.Arrange != nil {
		return renderScheduleWAV(score, opts, writer, stemsDir, outputGain)
	}
	var report Report
	if score == nil {
		return report, fmt.Errorf("nil score")
	}
	if opts.SampleRate == 0 {
		opts.SampleRate = 48_000
	}
	if opts.Bits == 0 {
		opts.Bits = 24
	}
	if opts.Block == 0 {
		opts.Block = 4096
	}
	if opts.Block < 1 || opts.Block > 4096 {
		return report, fmt.Errorf("WAV block must be 1 to 4096 frames")
	}
	if math.IsNaN(opts.MasterGainDB) || math.IsInf(opts.MasterGainDB, 0) || opts.MasterGainDB < -120 || opts.MasterGainDB > 24 || math.IsNaN(float64(opts.MasterBiasL)) || math.IsInf(float64(opts.MasterBiasL), 0) || math.IsNaN(float64(opts.MasterBiasR)) || math.IsInf(float64(opts.MasterBiasR), 0) {
		return report, fmt.Errorf("master gain or DC correction is out of range")
	}
	dither := opts.Dither == nil || *opts.Dither
	encoder, err := newWAVEncoder(opts.Bits, dither, score.Seed, outputGain)
	if err != nil {
		return report, err
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
	semantic, diagnostics := project.FromScore(score)
	if semantic == nil {
		for _, diagnostic := range diagnostics {
			if diagnostic.Severity == "error" {
				return report, fmt.Errorf("%s: %s", diagnostic.Code, diagnostic.Message)
			}
		}
		return report, fmt.Errorf("score cannot compile to a Cicada project")
	}
	schedule, err := project.CompileSchedule(semantic)
	if err != nil {
		return report, err
	}
	if len(schedule) > 0 {
		report.Bars = int(schedule[len(schedule)-1].EndTick / seq.TicksPerBar)
	}
	songBars := report.Bars
	if opts.From < 0 || opts.From >= songBars {
		return report, fmt.Errorf("start bar must be within the song arrangement")
	}
	if opts.Bars < 0 || opts.Bars > songBars-opts.From {
		return report, fmt.Errorf("requested bars must fit between the start bar and song end")
	}
	report.From = opts.From
	if opts.Bars == 0 {
		report.Bars -= opts.From
	} else {
		report.Bars = opts.Bars
	}
	if report.Bars < 1 || report.Bars > 256 {
		return report, fmt.Errorf("render supports 1 to 256 bars")
	}
	renderBars := report.From + report.Bars
	masterProcessor, err := project.PrepareMaster(semantic, opts.SampleRate)
	if err != nil {
		return report, err
	}
	busState := busMixerState{masterProcessor: masterProcessor, outputGain: 1}
	masterGainDB := opts.MasterGainDB
	authoredGainDB := float64(0)
	for _, bus := range semantic.Buses {
		switch bus.ID {
		case "music":
			busState.musicMute, busState.musicSolo = bus.Mixer.Mute, bus.Mixer.Solo
		case "sfx":
			busState.sfxMute, busState.sfxSolo = bus.Mixer.Mute, bus.Mixer.Solo
		}
	}
	if semantic.Master != nil {
		busState.masterMute = semantic.Master.Mixer.Mute
		if level := semantic.Master.Mixer.Level; level != nil && level.Unit == "db" && level.Number != nil {
			authoredGainDB = *level.Number
			masterGainDB += authoredGainDB
		}
	}
	if math.IsNaN(masterGainDB) || math.IsInf(masterGainDB, 0) || masterGainDB < -120 || masterGainDB > 24 {
		return report, fmt.Errorf("combined master gain is out of range")
	}
	report.SampleRate = opts.SampleRate
	report.MasterGainDB = masterGainDB
	report.TailFrames = int64(math.Ceil(opts.TailSec * float64(opts.SampleRate)))
	fromFrame := clock.SampleAtTick(int64(report.From) * seq.TicksPerBar)
	renderFrames := clock.SampleAtTick(int64(renderBars)*seq.TicksPerBar) + report.TailFrames
	report.Frames = renderFrames - fromFrame
	report.rangeSkip = fromFrame
	dataBytes := report.Frames * int64(encoder.frameBytes())
	if dataBytes > int64(^uint32(0))-60 {
		return report, fmt.Errorf("WAV exceeds RIFF size limit")
	}
	var prepared []engine.StereoVoiceFactory
	needsFileAudio := len(semantic.Assets) > 0 && semantic.NeedsSampleEngine() || len(semantic.Clips) > 0
	for _, sampler := range semantic.Samplers {
		needsFileAudio = needsFileAudio || sampler.Pack == ""
	}
	for _, track := range semantic.Tracks {
		needsFileAudio = needsFileAudio || semantic.Edition == 2 && track.Kind == "audio"
	}
	if needsFileAudio {
		prepared, err = sampleasset.Prepare(opts.AssetDir, semantic)
		if err != nil {
			return report, err
		}
		if err := sampleasset.ValidatePatterns(semantic, prepared, opts.SampleRate); err != nil {
			return report, err
		}
	}
	banks, err := preparePackVoices(score, opts)
	if err != nil {
		return report, err
	}
	tracks, err := compileTracksAll(score, semantic, opts.SampleRate, prepared, banks, opts.SamplerBaseline)
	if err != nil {
		return report, err
	}
	stopIsAction := true
	for _, pattern := range score.Patterns {
		if pattern.Name == "stop" {
			stopIsAction = false
			break
		}
	}
	var stems *stemOutput
	if stemsDir != "" {
		stems, err = newStemOutput(stemsDir, semantic, report)
		if err != nil {
			return report, err
		}
		defer stems.abort()
	}
	var delayA *fx.Delay
	var reverbB *fx.Reverb
	var compMusic *fx.Compressor
	var compSidechainTrack int
	for _, track := range tracks {
		if track.sendA > 0 || hasSceneParameters(semantic) {
			for _, effect := range semantic.Effects {
				if projectEffectKind(effect) == "delay" {
					params, err := project.DelayParamsFromValues(effect.Params)
					if err != nil {
						return report, err
					}
					delayA, err = fx.NewDelay(opts.SampleRate, score.TempoMilli)
					if err != nil {
						return report, err
					}
					if err := delayA.SetParams(params); err != nil {
						return report, err
					}
					delayA.Reset()
					break
				}
			}
			break
		}
	}
	for _, track := range tracks {
		if track.sendB > 0 || hasSceneParameters(semantic) {
			for _, effect := range semantic.Effects {
				if projectEffectKind(effect) == "reverb" {
					params, err := project.ReverbParamsFromValues(effect.Params)
					if err != nil {
						return report, err
					}
					reverbB, err = fx.NewReverb(opts.SampleRate)
					if err != nil {
						return report, err
					}
					if err := reverbB.SetParams(params); err != nil {
						return report, err
					}
					reverbB.Reset()
					break
				}
			}
			break
		}
	}
	compID := ""
	for _, bus := range semantic.Buses {
		if bus.ID == "music" && len(bus.Mixer.Inserts) == 1 {
			compID = bus.Mixer.Inserts[0]
		}
	}
	if compID == "" && semantic.Format == project.FormatID {
		for _, effect := range semantic.Effects {
			if projectEffectKind(effect) == "comp" {
				compID = effect.ID
			}
		}
	}
	for _, effect := range semantic.Effects {
		if effect.ID == compID && compID != "" {
			params, sidechain, err := project.CompSpecFromValues(effect.Params)
			if err != nil {
				return report, err
			}
			compMusic, err = fx.NewCompressor(opts.SampleRate)
			if err != nil {
				return report, err
			}
			if err := compMusic.SetParams(params); err != nil {
				return report, err
			}
			compMusic.Reset()
			if sidechain == "sfx" {
				compSidechainTrack = engine.SFXSidechain
			} else if sidechain != "" && sidechain != "music" {
				for index, track := range semantic.Tracks {
					if track.ID == sidechain {
						compSidechainTrack = index + 1
						break
					}
				}
			}
			break
		}
	}
	parameters, err := compileSceneParameters(semantic, tracks, opts.SampleRate, delayA, reverbB, compMusic)
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
	masterLatency := 0
	if masterProcessor != nil {
		masterLatency = masterProcessor.LatencyFrames()
	}
	report.skipFrames = insertLatency + masterLatency
	if stems != nil {
		stems.skipFrames = fromFrame + int64(insertLatency)
	}
	report.metricStart = fromFrame + int64(insertLatency+masterLatency)
	if report.From == 0 {
		report.metricStart = 0
	}
	report.metricEnd = renderFrames + int64(insertLatency+masterLatency)
	limiter, err := mix.NewLimiter(opts.SampleRate)
	if err != nil {
		return report, err
	}
	if err := writeWAVHeader(writer, opts.SampleRate, opts.Bits, uint32(dataBytes)); err != nil {
		return report, err
	}
	masterGain := float32(1)
	if masterGainDB != 0 {
		masterGain = float32(math.Pow(10, masterGainDB/20))
	}
	if masterProcessor != nil {
		masterGain = float32(math.Pow(10, authoredGainDB/20))
		busState.outputGain = float32(math.Pow(10, opts.MasterGainDB/20))
	}
	var eventBuf [128]seq.Event
	block := make([]byte, max(opts.Block, limiter.LatencyFrames())*encoder.frameBytes())
	eventCapacity := 0
	for i := range tracks {
		eventCapacity += 2*len(eventBuf) + 1 // active, pending releases, and transition
		if tracks[i].drums != nil {
			eventCapacity += (int(drum.LaneCount) - 1) * len(eventBuf)
		}
	}
	events := make([]scheduled, 0, eventCapacity)
	var position int64
	bar := 0
	for _, event := range schedule {
		entry := notation.SongEntry{Scene: semantic.Scenes[event.Scene].ID, Bars: int((event.EndTick - event.Tick) / seq.TicksPerBar)}
		if bar >= renderBars {
			break
		}
		scene := findScene(score, entry.Scene)
		if scene == nil {
			return report, fmt.Errorf("unknown scene %s", entry.Scene)
		}
		for entryBar := range entry.Bars {
			if bar >= renderBars {
				break
			}
			if err := applySceneBoundary(tracks, scene, stopIsAction, entryBar == 0); err != nil {
				return report, err
			}
			if entryBar == 0 {
				if err := parameters.apply(entry.Scene); err != nil {
					return report, err
				}
			}
			var nextScene *notation.Scene
			if bar+1 < renderBars {
				nextScene = sceneAtBar(score, semantic, schedule, bar+1)
			}
			planSceneTransitions(tracks, nextScene, int64(bar+1)*seq.TicksPerBar, clock, stopIsAction)
			end := clock.SampleAtTick(int64(bar+1) * seq.TicksPerBar)
			for position < end {
				frames := opts.Block
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
							n, overflow := seq.EventsInBlock(pattern, clock, uint8(ti), tracks[ti].currentSlot, position, frames, eventBuf[:])
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
					n, overflow := seq.EventsWithGatesInBlock(tracks[ti].active, clock, uint8(ti), tracks[ti].currentSlot, position, frames, eventBuf[:])
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
						n, overflow = seq.EventsWithGatesInBlock(&tracks[ti].pending, clock, uint8(ti), tracks[ti].pendingSlot, position, frames, eventBuf[:])
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
				slices.SortFunc(events, func(a, b scheduled) int {
					if a.event.Sample != b.event.Sample {
						if a.event.Sample < b.event.Sample {
							return -1
						}
						return 1
					}
					if a.event.Kind != b.event.Kind {
						if a.event.Kind == seq.NoteOff {
							return -1
						}
						return 1
					}
					if a.track != b.track {
						return a.track - b.track
					}
					if a.lane != b.lane {
						priority := func(lane drum.Lane) int {
							if lane == drum.OH {
								return int(drum.CH)
							}
							if lane == drum.CH {
								return int(drum.OH)
							}
							return int(lane)
						}
						return priority(a.lane) - priority(b.lane)
					}
					if a.event.NoteID < b.event.NoteID {
						return -1
					}
					if a.event.NoteID > b.event.NoteID {
						return 1
					}
					return 0
				})
				if err := renderBlock(writer, tracks, delayA, reverbB, compMusic, compSidechainTrack, opts.MasterBiasL, opts.MasterBiasR, masterGain, busState, limiter, stems, &encoder, events, position, frames, block, &report); err != nil {
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
	for position < renderFrames {
		frames := opts.Block
		if position+int64(frames) > renderFrames {
			frames = int(renderFrames - position)
		}
		if err := renderBlock(writer, tracks, delayA, reverbB, compMusic, compSidechainTrack, opts.MasterBiasL, opts.MasterBiasR, masterGain, busState, limiter, stems, &encoder, nil, position, frames, block, &report); err != nil {
			return report, err
		}
		position += int64(frames)
	}
	// Drain track and master delay in bounded blocks, then trim the initial
	// latency so exports retain their exact duration and score alignment.
	for remaining := insertLatency + masterLatency; remaining > 0; {
		frames := min(remaining, opts.Block)
		if err := renderBlock(writer, tracks, delayA, reverbB, compMusic, compSidechainTrack, opts.MasterBiasL, opts.MasterBiasR, masterGain, busState, limiter, stems, &encoder, nil, position, frames, block, &report); err != nil {
			return report, err
		}
		position += int64(frames)
		remaining -= frames
	}
	if err := flushLimiter(writer, limiter, stems, &encoder, block, &report); err != nil {
		return report, err
	}
	if report.writtenFrames != report.Frames {
		return report, fmt.Errorf("limiter wrote %d frames, expected %d", report.writtenFrames, report.Frames)
	}
	if err := writeMetadata(writer, uint32(score.TempoMilli), uint32(report.Bars), uint32(report.TailFrames)); err != nil {
		return report, err
	}
	if stems != nil {
		if err := stems.finish(uint32(score.TempoMilli)); err != nil {
			return report, err
		}
	}
	return report, nil
}

func compileTracks(score *notation.Score, semantic *project.Project, sampleRate int) ([]trackRuntime, error) {
	return compileTracksAll(score, semantic, sampleRate, nil, nil, false)
}
func compileTracksPrepared(score *notation.Score, semantic *project.Project, sampleRate int, prepared []engine.StereoVoiceFactory) ([]trackRuntime, error) {
	return compileTracksAll(score, semantic, sampleRate, prepared, nil, false)
}
func compileTracksWithPacks(score *notation.Score, semantic *project.Project, sampleRate int, banks map[string]*instrumentpack.Prepared, baseline bool) ([]trackRuntime, error) {
	return compileTracksAll(score, semantic, sampleRate, nil, banks, baseline)
}
func compileTracksAll(score *notation.Score, semantic *project.Project, sampleRate int, prepared []engine.StereoVoiceFactory, banks map[string]*instrumentpack.Prepared, baseline bool) ([]trackRuntime, error) {
	score, _ = notation.ResolvePresets(score)
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
	for sourceIndex, source := range score.Tracks {
		mixerParams, err := project.CompileMixerParams(source)
		if err != nil {
			return nil, fmt.Errorf("track %s: %w", source.Name, err)
		}
		trackMix := mix.NewTrack(mixerParams.GainDB, mixerParams.Pan, mixerParams.Mute)
		if len(prepared) == len(score.Tracks) && prepared[len(tracks)] != nil {
			voice, err := prepared[len(tracks)].NewStereoVoice(sampleRate)
			if err != nil {
				return nil, err
			}
			wrapper := &preparedVoice{StereoVoice: voice}
			track := trackRuntime{name: source.Name, mixer: trackMix, voice: wrapper, prepared: wrapper, patterns: map[string]seq.Pattern{}}
			if source.Kind == "audio" && score.Version == 2 {
				track.audioSlots = map[string]uint8{}
				slots, err := project.ClipSlots(semantic, semantic.Tracks[len(tracks)])
				if err != nil {
					return nil, err
				}
				for slot, name := range slots {
					if name != nil {
						track.audioSlots[*name] = uint8(slot)
					}
				}
			} else {
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
			}
			tracks = append(tracks, track)
			continue
		}

		if bank := banks[source.Kind]; bank != nil {
			voice := &packVoice{}
			if baseline {
				voice, err = legacyBaseline(bank, sampleRate)
			} else {
				voice.instrument, err = bank.New(sampleRate)
			}
			if err != nil {
				return nil, err
			}
			track := trackRuntime{name: source.Name, mixer: trackMix, voice: voice, patterns: map[string]seq.Pattern{}}
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
			continue
		}
		kitDefinition, isAuthoredKit := authoredKits[source.Kind]
		if source.Kind == "drums" || isAuthoredKit {
			kit, err := drum.New(sampleRate, uint32(score.Seed))
			if err != nil {
				return nil, err
			}
			if isAuthoredKit {
				bindings, err := project.CompileKitTrackAtSampleRate(kitDefinition, programs, semantic.Tracks[sourceIndex].Params, sampleRate)
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
					case engine.KitLaneModeled:
						err = kit.SetModeled(lane, binding.Model, binding.ModelParams, binding.ModelLevelDB, binding.ModelPan)
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
				lanes := project.BuiltinDrumLanes(score, source)
				for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
					if lanes[lane] {
						err = kit.SetParams(lane, params[lane])
					} else {
						err = kit.Disable(lane)
					}
					if err != nil {
						return nil, err
					}
				}
			}
			track := trackRuntime{name: source.Name, mixer: trackMix, drums: kit, drumPatterns: map[string][drum.LaneCount]*seq.Pattern{}}
			for _, pattern := range score.Patterns {
				if pattern.Kind != "drums" {
					continue
				}
				if !patternBoundToTrack(score, pattern.Name, source.Name) {
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
		if source.Kind == "guitar" {
			params, err := project.CompileGuitarParams(source)
			if err != nil {
				return nil, err
			}
			voice, err := guitar.New(sampleRate, params)
			if err != nil {
				return nil, err
			}
			track := trackRuntime{name: source.Name, mixer: trackMix, voice: voice, patterns: map[string]seq.Pattern{}}
			used := make(map[string]bool)
			for _, scene := range score.Scenes {
				for _, binding := range scene.Bindings {
					if binding.Track == source.Name {
						used[binding.Pattern] = true
					}
				}
			}
			for _, pattern := range score.Patterns {
				if pattern.Kind != "notes" || !used[pattern.Name] {
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
				if !patternBoundToTrack(score, pattern.Name, source.Name) {
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
		if source.Kind == "piano" && programs[source.Kind] == nil {
			voice, err := piano.New(sampleRate)
			if err != nil {
				return nil, err
			}
			sustain, err := project.CompilePianoSustain(source)
			if err != nil {
				return nil, fmt.Errorf("track %s: %w", source.Name, err)
			}
			if err := voice.SetSustain(sustain); err != nil {
				return nil, err
			}
			track := trackRuntime{name: source.Name, mixer: trackMix, voice: &pianoVoice{Instrument: voice}, patterns: map[string]seq.Pattern{}}
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
			continue
		}
		if profile, ok := modal.ParseTrackKind(source.Kind); ok {
			voice, err := modal.NewVoice(profile, sampleRate)
			if err != nil {
				return nil, err
			}
			track := trackRuntime{name: source.Name, mixer: trackMix, voice: modalVoice{voice}, patterns: map[string]seq.Pattern{}}
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
			continue
		}
		program := programs[source.Kind]
		if program == nil {
			return nil, fmt.Errorf("audio renderer does not yet implement %s track %s", source.Kind, source.Name)
		}
		overrides := make(map[string]string, len(source.Params))
		for _, param := range source.Params {
			if param.Name == "octave" && !program.HasParameter("octave") {
				continue
			}
			if project.IsMixerSourceParam(param.Name) {
				continue
			}
			overrides[param.Name] = param.Value
		}
		kernelProgram, err := instrument.Lower(program, overrides)
		if err != nil {
			return nil, fmt.Errorf("track %s: %w", source.Name, err)
		}
		track := trackRuntime{name: source.Name, mixer: trackMix, patterns: map[string]seq.Pattern{}}
		if program.Mode == "poly" {
			if project.GraphPolyphony(semantic, source.Kind) == 4 {
				pool, err := graph.NewPool(kernelProgram, sampleRate)
				if err != nil {
					return nil, err
				}
				track.poly, track.voice = pool, polyVoice{pool}
			} else {
				track.legacyPoly, err = graph.NewPoly(kernelProgram, sampleRate)
				track.voice = legacyPolyVoice{track.legacyPoly}
			}
		} else {
			var voice *graph.Voice
			voice, err = graph.NewVoice(kernelProgram, sampleRate)
			track.voice = customVoice{voice}
		}
		if err != nil {
			return nil, err
		}
		assignedPatterns := make(map[string]bool)
		for _, slot := range semantic.Tracks[sourceIndex].Slots {
			if slot != nil {
				assignedPatterns[*slot] = true
			}
		}
		for _, pattern := range score.Patterns {
			if pattern.Kind != "notes" {
				continue
			}
			if !patternBoundToTrack(score, pattern.Name, source.Name) {
				continue
			}
			compiled, err := project.CompilePattern(score, pattern, source)
			if err != nil {
				return nil, err
			}
			if assignedPatterns[pattern.Name] {
				if err := project.ValidateGraphDelayPattern(kernelProgram, sampleRate, compiled[0].Pattern); err != nil {
					return nil, fmt.Errorf("CICADA-PARAM: track %s pattern %s: %w", source.Name, pattern.Name, err)
				}
			}
			track.patterns[pattern.Name] = compiled[0].Pattern
		}
		tracks = append(tracks, track)
	}
	for i := range tracks {
		// Chance uses the compiled per-track pattern slot, just as playback
		// does. A lane number (or a constant zero) selects a different stream.
		tracks[i].patternSlots = make(map[string]uint8)
		for slot, pattern := range semantic.Tracks[i].Slots {
			if pattern != nil {
				tracks[i].patternSlots[*pattern] = uint8(slot)
			}
		}
		tracks[i].sendPreGain = 1
		tracks[i].sendA = float32(semantic.Tracks[i].Mixer.SendA)
		tracks[i].sendB = float32(semantic.Tracks[i].Mixer.SendB)
		tracks[i].sendAPre = semantic.Tracks[i].Mixer.SendPre
		tracks[i].sendBPre = semantic.Tracks[i].Mixer.SendPre
		for _, send := range semantic.Tracks[i].Mixer.Sends {
			kind := send.To
			for _, effect := range semantic.Effects {
				if effect.ID == send.To {
					kind = projectEffectKind(effect)
					break
				}
			}
			if kind == "delay" {
				tracks[i].sendAPre = tracks[i].sendAPre || send.Tap == "pre"
			} else if kind == "reverb" {
				tracks[i].sendBPre = tracks[i].sendBPre || send.Tap == "pre"
			}
		}
		tracks[i].muted = semantic.Tracks[i].Mixer.Mute
		tracks[i].solo = semantic.Tracks[i].Mixer.Solo
		tracks[i].busSFX = semantic.Tracks[i].Mixer.Bus == "sfx"
	}
	anySolo := false
	for i := range tracks {
		anySolo = anySolo || tracks[i].solo
	}
	for i := range tracks {
		tracks[i].muted = tracks[i].muted || anySolo && !tracks[i].solo
	}
	for i := range tracks {
		inserts := semantic.Tracks[i].Mixer.Inserts
		if len(inserts) == 0 && semantic.Tracks[i].Mixer.Insert != "none" {
			inserts = []string{semantic.Tracks[i].Mixer.Insert}
		}
		if len(inserts) == 0 {
			continue
		}
		for _, effect := range semantic.Effects {
			if effect.ID != inserts[0] || projectEffectKind(effect) != "drive" {
				continue
			}
			params, err := project.DriveParamsFromValues(effect.Params)
			if err != nil {
				return nil, err
			}
			insert, err := fx.NewDrive(sampleRate)
			if err != nil {
				return nil, err
			}
			if err := insert.SetParams(params); err != nil {
				return nil, err
			}
			insert.Reset()
			tracks[i].insert = insert
			break
		}
	}
	if hasDriveInsert(semantic) {
		for i := range tracks {
			if tracks[i].insert == nil {
				align, err := mix.NewDelay(fx.DriveLatencyFrames)
				if err != nil {
					return nil, err
				}
				tracks[i].align = align
			}
		}
	}
	return tracks, nil
}

func projectEffectKind(effect project.Effect) string {
	if effect.Kind != "" {
		return effect.Kind
	}
	return effect.ID
}

func hasDriveInsert(p *project.Project) bool {
	if p == nil {
		return false
	}
	for _, track := range p.Tracks {
		inserts := track.Mixer.Inserts
		if len(inserts) == 0 && track.Mixer.Insert != "none" {
			inserts = []string{track.Mixer.Insert}
		}
		for _, id := range inserts {
			for _, effect := range p.Effects {
				if effect.ID == id && projectEffectKind(effect) == "drive" {
					return true
				}
			}
		}
	}
	return false
}

func findScene(score *notation.Score, name string) *notation.Scene {
	for i := range score.Scenes {
		if score.Scenes[i].Name == name {
			return &score.Scenes[i]
		}
	}
	return nil
}

func sceneAtBar(score *notation.Score, p *project.Project, schedule []engine.ScheduleEvent, bar int) *notation.Scene {
	tick := int64(bar) * seq.TicksPerBar
	i := sort.Search(len(schedule), func(i int) bool { return tick < schedule[i].EndTick })
	if i == len(schedule) {
		return nil
	}
	return findScene(score, p.Scenes[schedule[i].Scene].ID)
}

func planSceneTransitions(tracks []trackRuntime, next *notation.Scene, boundaryTick int64, clock seq.Clock, stopIsAction bool) {
	for ti := range tracks {
		track := &tracks[ti]
		track.transition = sceneTransition{}
		if next == nil || track.active == nil || track.drums != nil {
			continue
		}
		for _, binding := range next.Bindings {
			if binding.Track != track.name || binding.Pattern == "keep" || binding.Pattern == "off" || binding.Pattern == "stop" && stopIsAction || binding.Pattern == track.currentName {
				continue
			}
			target, ok := track.patterns[binding.Pattern]
			if !ok {
				continue // applyScene reports the invalid binding at the boundary
			}
			track.transition = sceneTransitionFor(track.active, &target, uint8(ti), track.currentSlot, track.patternSlots[binding.Pattern], boundaryTick, clock)
			break
		}
	}
}

func sceneTransitionFor(source, target *seq.Pattern, track, sourceSlot, targetSlot uint8, boundaryTick int64, clock seq.Clock) sceneTransition {
	if boundaryTick < seq.TicksPerStep || boundaryTick%seq.TicksPerStep != 0 {
		return sceneTransition{}
	}
	lastStep := boundaryTick/seq.TicksPerStep - 1
	index := uint8(lastStep % int64(source.Len))
	step, err := seq.UnpackStep(source.Steps[index])
	if err != nil || !step.Gate || !step.Slide || !seq.ProbabilityHit(step.Probability, source.Seed, track, sourceSlot, lastStep/int64(source.Len), index) {
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
	carry := err == nil && target.Chords[targetIndex].Count == 0 && next.Gate && !next.Tie && seq.ProbabilityHit(next.Probability, target.Seed, track, targetSlot, targetStep/int64(target.Len), targetIndex)
	return sceneTransition{
		valid: true, carry: carry, sourceNoteID: sourceNoteID,
		boundarySample: clock.SampleAtTick(boundaryTick), targetSample: clock.SampleAtTick(boundaryTick),
		release: seq.Event{Kind: seq.NoteOff, NoteID: sourceNoteID, Tick: offTick, Sample: clock.SampleAtTick(offTick), Track: track},
	}
}

func applyScene(tracks []trackRuntime, scene *notation.Scene, stopIsAction bool) error {
	return applySceneBoundary(tracks, scene, stopIsAction, false)
}
func applySceneBoundary(tracks []trackRuntime, scene *notation.Scene, stopIsAction, restartClips bool) error {
	for _, binding := range scene.Bindings {
		for ti := range tracks {
			if tracks[ti].name != binding.Track {
				continue
			}
			if binding.Pattern == "stop" && stopIsAction {
				binding.Pattern = "off"
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
				if tracks[ti].currentName == binding.Pattern && !(restartClips && tracks[ti].audioSlots != nil) {
					continue
				}
				if tracks[ti].audioSlots != nil {
					slot, ok := tracks[ti].audioSlots[binding.Pattern]
					if !ok {
						return fmt.Errorf("track %s cannot play clip %s", binding.Track, binding.Pattern)
					}
					if err := tracks[ti].prepared.SelectSlot(slot, 0, true); err != nil {
						return err
					}
					tracks[ti].currentName = binding.Pattern
					continue
				}
				if tracks[ti].drums != nil {
					lanes, ok := tracks[ti].drumPatterns[binding.Pattern]
					if !ok {
						return fmt.Errorf("track %s cannot play pattern %s", binding.Track, binding.Pattern)
					}
					tracks[ti].activeDrums = lanes
					tracks[ti].currentName = binding.Pattern
					tracks[ti].currentSlot = tracks[ti].patternSlots[binding.Pattern]
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
					tracks[ti].pendingSlot = tracks[ti].currentSlot
					tracks[ti].hasPendingGate = true
				}
				tracks[ti].generation++
				tracks[ti].currentName = binding.Pattern
				tracks[ti].currentSlot = tracks[ti].patternSlots[binding.Pattern]
				tracks[ti].current = pattern
				tracks[ti].active = &tracks[ti].current
			}
		}
	}
	return nil
}

func renderBlock(w io.Writer, tracks []trackRuntime, delayA *fx.Delay, reverbB *fx.Reverb, compMusic *fx.Compressor, compSidechainTrack int, masterBiasL, masterBiasR, masterGain float32, busState busMixerState, limiter *mix.Limiter, stems *stemOutput, encoder *wavEncoder, events []scheduled, start int64, frames int, buffer []byte, report *Report) error {
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
			if track.poly != nil {
				key := graph.Cohort{NoteID: event.event.NoteID, Generation: event.generation}
				if event.event.Kind == seq.NoteOff {
					track.poly.Release(key)
				} else {
					if !event.event.Slide {
						track.poly.ReleaseAll()
					}
					notes, count := event.event.Notes, event.event.NoteCount
					if count == 0 {
						notes[0] = event.event.Note
						count = 1
					}
					if _, err := track.poly.NoteOn(notes, count, event.event.Velocity, event.event.Slide, key); err != nil {
						return err
					}
				}
			}
			if event.event.Kind == seq.NoteOff {
				if track.activeGen == event.generation && track.activeNoteID == event.event.NoteID {
					if track.poly == nil {
						track.voice.NoteOff()
					}
					track.activeGen = 0
					track.hasPendingGate = false
				}
			} else {
				if track.poly == nil {
					if voice, ok := track.voice.(*pianoVoice); ok {
						notes, count := event.event.Notes, event.event.NoteCount
						if count == 0 {
							notes[0], count = event.event.Note, 1
						}
						if err := voice.NoteChord(notes, count, event.event.Velocity, event.event.Slide); err != nil {
							return err
						}
					} else {
						track.voice.NoteOn(event.event.Note, event.event.Velocity, event.event.Accent, event.event.Slide)
					}
				}
				if track.prepared != nil && track.prepared.err != nil {
					return fmt.Errorf("sampler %s: %w", track.name, track.prepared.err)
				}
				track.activeGen = event.generation
				track.activeNoteID = event.event.NoteID
				track.hasPendingGate = false
				track.slideFrom, track.slideAt = 0, 0
			}
			eventIndex++
		}
		var dry mix.Dry
		var sendAL, sendAR, sendBL, sendBR, sideL, sideR float32
		for ti := range tracks {
			if tracks[ti].parameters != nil {
				tracks[ti].parameters.advance(&tracks[ti])
			}
			var l, r float32
			if tracks[ti].drums != nil {
				l, r = tracks[ti].drums.NextStereo()
				if tracks[ti].drums.Fault() {
					return fmt.Errorf("drum DSP fault on %s", tracks[ti].name)
				}
			} else if voice, ok := tracks[ti].voice.(*pianoVoice); ok {
				l, r = voice.NextStereo()
			} else if tracks[ti].prepared != nil {
				l, r = tracks[ti].prepared.NextStereo()
			} else if tracks[ti].legacyPoly != nil {
				l, r = tracks[ti].legacyPoly.NextStereo()
			} else if tracks[ti].poly != nil {
				l = tracks[ti].poly.Next()
				r = l
			} else {
				if voice, ok := tracks[ti].voice.(*packVoice); ok {
					l, r = voice.NextStereo()
					if voice.fault != nil {
						return fmt.Errorf("sampler %s: %w", tracks[ti].name, voice.fault)
					}
				} else {
					mono := tracks[ti].voice.Next()
					l, r = mono, mono
				}
			}
			if tracks[ti].insert != nil {
				l, r = tracks[ti].insert.Process(l, r)
				if tracks[ti].insert.Fault() {
					return fmt.Errorf("drive DSP fault on %s", tracks[ti].name)
				}
			} else if tracks[ti].align != nil {
				l, r = tracks[ti].align.Process(l, r)
			}
			if tracks[ti].sendA > 0 && !tracks[ti].muted {
				if tracks[ti].sendAPre {
					sendAL += l * tracks[ti].sendA * tracks[ti].sendPreGain
					sendAR += r * tracks[ti].sendA * tracks[ti].sendPreGain
				} else {
					sendAL += l * tracks[ti].mixer.Left * tracks[ti].sendA
					sendAR += r * tracks[ti].mixer.Right * tracks[ti].sendA
				}
			}
			if tracks[ti].sendB > 0 && !tracks[ti].muted {
				if tracks[ti].sendBPre {
					sendBL += l * tracks[ti].sendB * tracks[ti].sendPreGain
					sendBR += r * tracks[ti].sendB * tracks[ti].sendPreGain
				} else {
					sendBL += l * tracks[ti].mixer.Left * tracks[ti].sendB
					sendBR += r * tracks[ti].mixer.Right * tracks[ti].sendB
				}
			}
			if tracks[ti].busSFX {
				dry.AddSFX(l, r, tracks[ti].mixer)
			} else {
				dry.Add(l, r, tracks[ti].mixer)
			}
			if stems != nil {
				stems.track(ti, l, r, tracks[ti].mixer, tracks[ti].busSFX)
			}
			if compSidechainTrack == ti+1 {
				sideL, sideR = l*tracks[ti].mixer.Left, r*tracks[ti].mixer.Right
			}
		}
		if delayA != nil {
			returnL, returnR := delayA.Process(sendAL, sendAR)
			if delayA.Fault() {
				return fmt.Errorf("delay return DSP fault")
			}
			dry.AddReturn(returnL, returnR)
			if stems != nil {
				stems.returnA(returnL, returnR)
			}
		} else if stems != nil {
			stems.returnA(0, 0)
		}
		if reverbB != nil {
			returnL, returnR := reverbB.Process(sendBL, sendBR)
			if reverbB.Fault() {
				return fmt.Errorf("reverb return DSP fault")
			}
			dry.AddReturn(returnL, returnR)
			if stems != nil {
				stems.returnB(returnL, returnR)
			}
		} else if stems != nil {
			stems.returnB(0, 0)
		}
		left, right := dry.Music()
		sfxL, sfxR := dry.SFX()
		if busState.sfxMute || busState.musicSolo && !busState.sfxSolo {
			sfxL, sfxR = 0, 0
		}
		if stems != nil {
			stems.buses(left, right, sfxL, sfxR)
			stems.appendMixFrame(sample)
		}
		if compMusic != nil {
			if compSidechainTrack == 0 {
				left, right = compMusic.Process(left, right)
			} else if compSidechainTrack == engine.SFXSidechain {
				left, right = compMusic.ProcessSidechain(left, right, sfxL, sfxR)
			} else {
				left, right = compMusic.ProcessSidechain(left, right, sideL, sideR)
			}
			if compMusic.Fault() {
				return fmt.Errorf("music compressor DSP fault")
			}
		}
		if busState.musicMute || busState.sfxSolo && !busState.musicSolo {
			left, right = 0, 0
		}
		left, right = left+sfxL, right+sfxR
		if busState.masterMute {
			left, right = 0, 0
		}
		if busState.masterProcessor == nil && (masterBiasL != 0 || masterBiasR != 0) {
			left += masterBiasL
			right += masterBiasR
		}
		if masterGain != 1 {
			left *= masterGain
			right *= masterGain
		}
		if busState.masterProcessor != nil {
			left, right = busState.masterProcessor.Process(left, right)
			if busState.masterProcessor.Fault() {
				return fmt.Errorf("master chain DSP fault")
			}
			left = (left + masterBiasL) * busState.outputGain
			right = (right + masterBiasR) * busState.outputGain
		}
		if sample >= report.metricStart && sample < report.metricEnd {
			if abs := float32(math.Max(math.Abs(float64(left)), math.Abs(float64(right)))); abs > report.Peak {
				report.Peak = abs
			}
			if math.Abs(float64(left)) > limiter.Ceiling() {
				report.PreLimiterOvers++
			}
			if math.Abs(float64(right)) > limiter.Ceiling() {
				report.PreLimiterOvers++
			}
		}
		outL, outR, ready := limiter.Process(left, right)
		if limiter.Fault() {
			return fmt.Errorf("non-finite master input")
		}
		if reduction := limiter.GainReductionDB(); reduction > report.MaxLimiterGainReductionDB {
			report.MaxLimiterGainReductionDB = reduction
		}
		if !ready {
			continue
		}
		if report.skipFrames > 0 {
			report.skipFrames--
			continue
		}
		if report.rangeSkip > 0 {
			encoder.advanceDither()
			report.rangeSkip--
			continue
		}
		if stems != nil {
			stems.appendMaster(outL*encoder.gain, outR*encoder.gain)
		}
		encoder.writeFrame(buffer[outFrames*encoder.frameBytes():], outL, outR, limiter.Ceiling(), report)
		outFrames++
	}
	if outFrames == 0 {
		if stems != nil {
			return stems.flush()
		}
		return nil
	}
	n, err := w.Write(buffer[:outFrames*encoder.frameBytes()])
	if err != nil {
		return err
	}
	if n != outFrames*encoder.frameBytes() {
		return io.ErrShortWrite
	}
	report.writtenFrames += int64(outFrames)
	if stems != nil {
		return stems.flush()
	}
	return nil
}

func flushLimiter(w io.Writer, limiter *mix.Limiter, stems *stemOutput, encoder *wavEncoder, buffer []byte, report *Report) error {
	frames := limiter.LatencyFrames()
	outFrames := 0
	for i := 0; i < frames; i++ {
		left, right, ready := limiter.Process(0, 0)
		if !ready || limiter.Fault() {
			return fmt.Errorf("master limiter failed while flushing")
		}
		if report.skipFrames > 0 {
			report.skipFrames--
			continue
		}
		if report.rangeSkip > 0 {
			encoder.advanceDither()
			report.rangeSkip--
			continue
		}
		encoder.writeFrame(buffer[outFrames*encoder.frameBytes():], left, right, limiter.Ceiling(), report)
		if stems != nil {
			stems.appendMaster(left*encoder.gain, right*encoder.gain)
		}
		outFrames++
	}
	n, err := w.Write(buffer[:outFrames*encoder.frameBytes()])
	if err != nil {
		return err
	}
	if n != outFrames*encoder.frameBytes() {
		return io.ErrShortWrite
	}
	report.writtenFrames += int64(outFrames)
	if stems != nil {
		return stems.flush()
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

func patternBoundToTrack(score *notation.Score, pattern, track string) bool {
	for _, scene := range score.Scenes {
		for _, binding := range scene.Bindings {
			if binding.Track == track && binding.Pattern == pattern {
				return true
			}
		}
	}
	return false
}
