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
	"strings"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/host/keyboard"
	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/dsp/detmath"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/acid"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/kernel/voice/guitar"
	keyvoice "m31labs.dev/cicada/kernel/voice/keyboard"
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
	// MasterInputGainDB is export gain at the master input: after the music
	// and SFX buses sum and the authored master level, before the score's
	// master insert chain. Loudness targeting uses it when the chain holds a
	// limiter. Without a prepared master chain it equals MasterGainDB.
	MasterInputGainDB float64
	MasterBiasL       float32 // export DC correction after the master chain and all gain, before the limiter
	MasterBiasR       float32 // export DC correction after the master chain and all gain, before the limiter
	// MaxResidentPCM caps, in bytes, the decoded PCM of all sampler packs the
	// render loads. Zero selects instrumentpack.DefaultMaxResidentPCM (1 GiB).
	MaxResidentPCM int64
	// MasterCache, when set, lets repeated renders of one score skip
	// synthesis: a render replays the signal that entered the master stage when
	// it differs from the stored capture only in MasterInputGainDB,
	// MasterGainDB, MasterBiasL, MasterBiasR, Bits, Dither or Normalize. A
	// difference in anything else (the score, From, Bars, TailSec, SampleRate,
	// Block, AssetRoot, AssetDir or SamplerBaseline) makes the render capture a
	// new signal in place of the stored one. The cache matches scores by
	// pointer, so do not modify a score between renders that share a cache. See
	// MasterCache.
	MasterCache *MasterCache
	// NoteTap, when set, receives each note start (seq.NoteOn, drum hits
	// included) and release (seq.NoteOff) that the render plays, in render
	// order, from either render path. The events are the scheduler's own, so
	// chains, scene phase, placements, ties, chance and transposition are
	// already applied. It is called on the render goroutine.
	NoteTap func(track int, event seq.Event)
	// Jobs is the most render lanes, goroutines that synthesize tracks at the
	// same time, that the render may use. Zero, the default, and 1 render
	// serially: a caller that does not ask for lanes gets none. AutoJobs
	// picks the smaller of 4 and half the usable CPUs; a count is honoured up
	// to runtime.NumCPU. A render never sets up more lanes than the song has
	// tracks. Every value writes the same bytes: lanes only synthesize each
	// track's own signal between control changes, and the render mixes the
	// tracks in track order as the serial render does. Report.LaneReport says
	// what the render chose and why it stayed serial if it did.
	Jobs int
}

type Report struct {
	// NormalizeGain is the gain peak normalization applied: 1 when Normalize
	// is off or was skipped. NormalizeSkipped is empty unless Normalize was
	// requested and the range's output peak was below one 24-bit step (2^-23,
	// a silent range or the end of a tail); it then says so, and the WAV is
	// written at gain 1.
	NormalizeGain    float32
	NormalizeSkipped string

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
	// MaxMasterChainLimiterReductionDB is the largest gain reduction of a
	// limiter declared in the score's master insert chain.
	MaxMasterChainLimiterReductionDB float64
	writtenFrames                    int64
	skipFrames                       int
	rangeSkip                        int64
	metricStart                      int64
	metricEnd                        int64
	chainStart                       int64
	chainEnd                         int64
	tempoChanges                     []project.TempoChange
	// LaneReport says how the render used its lanes (Options.Jobs).
	LaneReport
}

// masterChainWindow returns the half-open range of render sample positions
// whose master-chain limiter reduction lands on delivered output frames. A
// limiter reduction read at position s applies to the chain output at s, which
// the render delivers at s minus the track insert and master chain latency, so
// the window starts that much after the first delivered source frame. A render
// from bar zero keeps every position so full-song reports are unchanged.
func masterChainWindow(from int, fromFrame, endFrame int64, insertLatency, masterLatency int) (start, end int64) {
	shift := int64(insertLatency + masterLatency)
	start = fromFrame + shift
	if from == 0 {
		start = 0
	}
	return start, endFrame + shift
}

// noteMasterChainReduction records a master-chain limiter reduction read at
// render sample position at, ignoring discarded preroll and flush frames.
func (r *Report) noteMasterChainReduction(at int64, db float64) {
	if at >= r.chainStart && at < r.chainEnd {
		r.MaxMasterChainLimiterReductionDB = max(r.MaxMasterChainLimiterReductionDB, db)
	}
}

type trackRuntime struct {
	prepared       *preparedVoice
	legacyPoly     *graph.Poly
	audioSlots     map[string]uint8
	name           string
	voice          monoVoice
	expressive     expressionVoice
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
	noteTap        func(int, seq.Event) // from Options.NoteTap
	tapTick        int64                // tick of the scene boundary being applied
	tapIndex       int
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

	chain                                 []string
	chainIndex                            int
	chainDue, startTick, pendingStartTick int64
	automation                            *sceneParameters
}

type busMixerState struct {
	masterProcessor      engine.StereoProcessor
	masterLimiter        interface{ LimiterGainReductionDB() float64 }
	masterCache          *MasterCache // captures or replays the signal entering the master stage
	cacheReplay          bool
	outputGain           float32
	musicMute, musicSolo bool
	sfxMute, sfxSolo     bool
	masterMute           bool
	// lanes, when set, synthesizes the tracks of each segment on several
	// goroutines before renderBlock mixes them (Options.Jobs).
	lanes *trackLanes
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

type expressionVoice interface {
	SetExpression(seq.Expression)
}

func (track *trackRuntime) applyExpression(params seq.Expression) {
	if track.poly != nil {
		track.poly.SetExpression(graph.Cohort{NoteID: track.activeNoteID, Generation: track.activeGen}, params)
	} else if track.expressive != nil {
		track.expressive.SetExpression(params)
	}
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

// trimmedVoice applies a fixed calibration factor to one voice's output.
type trimmedVoice struct {
	keyvoice.Voice
	gain float32
}

func (v trimmedVoice) NextStereo() (float32, float32) {
	l, r := v.Voice.NextStereo()
	return l * v.gain, r * v.gain
}

type pianoVoice struct {
	Instrument keyvoice.Voice
	notes      [4]uint8
	count      uint8
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

func (voice *pianoVoice) NextStereo() (float32, float32) { return voice.Instrument.NextStereo() }
func (voice *pianoVoice) SetSustain(v float32) error     { return voice.Instrument.SetSustain(v) }

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

// normalizeFloor is the smallest output peak that peak normalization raises to
// -1 dBFS: 2^-23, one step of 24-bit PCM. A quieter range holds nothing a
// 24-bit file can show: it is silent, or it holds only the end of a decaying
// tail, whose gain (0.89/peak, above 2^22) would amplify only the arithmetic
// of that tail. Such a tail's peak was 2.2e-44 when voices left their state
// in subnormal numbers, and the gain overflowed ("invalid WAV output gain");
// with that state flushed (kernel/dsp/subnormal) the peak is exactly zero. For
// such a range normalization is skipped: the WAV is written at gain 1, as a
// silent range always was, and Report.NormalizeSkipped says why. The floor
// also caps the gain normalization can add after a flush at 0.89*2^23.
const normalizeFloor = 1.0 / (1 << 23)

func renderWithOptions(score *notation.Score, opts Options, writer io.Writer, stemsDir string) (Report, error) {
	gain := float32(1)
	skipped := ""
	if opts.Normalize {
		preview := opts
		preview.Normalize = false
		preview.NoteTap = nil
		report, err := renderWAV(score, preview, io.Discard, "", 1)
		if err != nil {
			return report, err
		}
		if report.OutputPeak < normalizeFloor {
			skipped = fmt.Sprintf("peak normalization skipped: bars %d to %d and the %g s tail are silent (output peak %g, below one 24-bit step, 2^-23); the WAV is written at unity gain",
				report.From+1, report.From+report.Bars, opts.TailSec, report.OutputPeak)
		} else {
			gain = float32(detmath.Pow(10, -1.0/20) / float64(report.OutputPeak))
		}
		final, err := renderWAV(score, opts, writer, stemsDir, gain)
		if final.SerialReason == SerialReplay {
			// The second render replayed the first one's mix: report how the
			// synthesis used its lanes.
			final.LaneReport = report.LaneReport
		}
		final.NormalizeGain, final.NormalizeSkipped = gain, skipped
		return final, err
	}
	report, err := renderWAV(score, opts, writer, stemsDir, gain)
	report.NormalizeGain = gain
	return report, err
}

func hasAudioClipTrack(score *notation.Score) bool {
	for _, track := range score.Tracks {
		if track.Kind == "audio" {
			return true
		}
	}
	return false
}

func hasSerialTrackEffects(score *notation.Score) bool {
	for _, track := range score.Tracks {
		mixer, err := project.CompileMixerParams(track)
		if err != nil {
			return true
		}
		if len(mixer.Inserts) > 1 {
			return true
		}
		if len(mixer.Inserts) == 1 {
			for _, effect := range score.Effects {
				if effect.Name != mixer.Inserts[0] {
					continue
				}
				kind := effect.Kind
				if kind == "" {
					kind = effect.Name
				}
				// The direct mixer builds only a drive insert (compileTracksAll).
				// Every other kind, chorus included, runs in the engine; the
				// direct mixer used to play a single chorus as no insert.
				if kind != "drive" {
					return true
				}
			}
		}
	}
	return false
}

// laneStepNotes gives every step of a drum lane pattern its lane number as the
// note, as the engine's lane patterns carry it (packProjectStep in
// project/runtime.go). The scheduler seeds each hit's humanize offset with the
// step's note (seq.HumanizeVelocity), so with the General MIDI pitch that
// project.CompilePattern writes for check, the direct mixer drew different
// velocities from the engine, and `cicada render` differed from Studio. The
// direct mixer plays a hit by its lane, never by this note.
func laneStepNotes(p *seq.Pattern, lane uint8) error {
	for i := 0; i < int(p.Len); i++ {
		step, err := seq.UnpackStep(p.Steps[i])
		if err != nil {
			return err
		}
		step.Note = lane
		if p.Steps[i], err = seq.PackStep(step); err != nil {
			return err
		}
	}
	return nil
}

// hasTapeEcho reports whether the score declares a tape echo. The direct WAV
// mixer has no tape return, so such a score renders through the engine.
func hasTapeEcho(score *notation.Score) bool {
	for _, effect := range score.Effects {
		if effect.Kind == "tape" {
			return true
		}
	}
	return false
}

// hasSamplerExpression reports whether a single-asset sampler track plays a
// pattern with expression rows, through a scene or its chain. The engine plays
// a sampler's bend, vibrato and pressure rows; the direct mixer's sampler
// voice has no note expression and played such rows as if they were absent.
// Rows on other tracks keep the direct mixer, which plays them.
func hasSamplerExpression(score *notation.Score) bool {
	samplers := map[string]bool{}
	for _, sampler := range score.Samplers {
		if sampler.Asset != "" {
			samplers[sampler.Name] = true
		}
	}
	rows := map[string]bool{}
	for _, pattern := range score.Patterns {
		rows[pattern.Name] = len(pattern.Expression) > 0
	}
	sampled := map[string]bool{}
	for _, track := range score.Tracks {
		if !samplers[track.Kind] {
			continue
		}
		sampled[track.Name] = true
		for _, entry := range track.Chain {
			if rows[entry.Text] {
				return true
			}
		}
	}
	for _, scene := range score.Scenes {
		for _, binding := range scene.Bindings {
			if sampled[binding.Track] && rows[binding.Pattern] {
				return true
			}
		}
	}
	return false
}

// UsesScheduledEngine reports whether a render goes through the streamed
// engine path rather than the direct WAV mixer. The two paths inject DC
// correction at different points relative to export gain.
func UsesScheduledEngine(score *notation.Score, opts Options) bool {
	return scheduledEngineReason(score, opts) != ""
}

// scheduledEngineReason names the first feature that needs the streamed
// engine, or returns "" when the direct WAV mixer can render the score.
func scheduledEngineReason(score *notation.Score, opts Options) string {
	if score == nil {
		return ""
	}
	for _, pattern := range score.Patterns {
		if pattern.Performance != nil {
			return "performance pattern " + pattern.Name
		}
	}
	// Modulators and routes (spec 24) need the engine, which owns the control grid.
	switch {
	case len(score.Modulators) > 0 || len(score.Routes) > 0:
		return "modulators"
	case score.Arrange != nil:
		return "an arrangement"
	case hasTapeEcho(score):
		return "a tape echo"
	case hasAudioClipTrack(score):
		return "audio clips"
	case hasSerialTrackEffects(score):
		return "serial track effects"
	case hasKitSampler(score):
		return "kit sampler lanes"
	case movesInstrumentParam(score):
		return "a live instrument parameter"
	case hasPackSampler(score) && !opts.SamplerBaseline:
		return "sample packs"
	case hasSamplerExpression(score):
		return "sampler expression rows"
	}
	return ""
}

func renderWAV(score *notation.Score, opts Options, writer io.Writer, stemsDir string, outputGain float32) (Report, error) {
	if opts.AssetRoot == "" {
		opts.AssetRoot = opts.AssetDir
	}
	if opts.AssetDir == "" {
		opts.AssetDir = opts.AssetRoot
	}
	if reason := scheduledEngineReason(score, opts); reason != "" {
		// The baseline is a direct-mixer mode; the engine would ignore it.
		if opts.SamplerBaseline && hasPackSampler(score) {
			return Report{}, fmt.Errorf("--sampler-baseline needs the direct WAV mixer, but this score uses %s, which only the scheduled engine renders", reason)
		}
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
	if math.IsNaN(opts.MasterGainDB) || math.IsInf(opts.MasterGainDB, 0) || opts.MasterGainDB < -120 || opts.MasterGainDB > 24 || math.IsNaN(opts.MasterInputGainDB) || math.IsInf(opts.MasterInputGainDB, 0) || opts.MasterInputGainDB < -120 || opts.MasterInputGainDB > 24 || math.IsNaN(float64(opts.MasterBiasL)) || math.IsInf(float64(opts.MasterBiasL), 0) || math.IsNaN(float64(opts.MasterBiasR)) || math.IsInf(float64(opts.MasterBiasR), 0) {
		return report, fmt.Errorf("master gain or DC correction is out of range")
	}
	dither := opts.Dither == nil || *opts.Dither
	encoder, err := newWAVEncoder(opts.Bits, dither, score.Seed, outputGain)
	if err != nil {
		return report, err
	}
	if err := checkTail(opts.TailSec); err != nil {
		return report, err
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
	meterMap := semantic.MeterMap()
	if len(schedule) > 0 {
		bars, _ := meterMap.BarAt(schedule[len(schedule)-1].EndTick)
		report.Bars = int(bars)
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
	busState.masterLimiter, _ = masterProcessor.(interface{ LimiterGainReductionDB() float64 })
	authoredGainDB, hasLevel := authoredMasterLevelDB(semantic)
	masterGainDB, masterGain, outputGain := directMasterGains(opts, authoredGainDB, hasLevel, masterProcessor != nil)
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
	}
	if math.IsNaN(masterGainDB) || math.IsInf(masterGainDB, 0) || masterGainDB < -120 || masterGainDB > 24 {
		return report, fmt.Errorf("combined master gain is out of range")
	}
	report.SampleRate = opts.SampleRate
	report.MasterGainDB = masterGainDB
	report.TailFrames = int64(math.Ceil(opts.TailSec * float64(opts.SampleRate)))
	tempoChanges := project.SongTempoChanges(semantic)
	tempoMap, err := project.NewTempoMap(meterMap, opts.SampleRate, score.TempoMilli, tempoChanges)
	if err != nil {
		return report, err
	}
	report.tempoChanges = project.ExcerptTempoChanges(semantic.TempoMilli, tempoChanges, report.From, report.Bars)
	if len(report.tempoChanges) > maxWAVTempoChanges {
		return report, fmt.Errorf("bars %d to %d hold %d tempo changes; the WAV timing chunk records at most %d, so render a shorter range", report.From+1, report.From+report.Bars, len(report.tempoChanges), maxWAVTempoChanges)
	}
	fromFrame := tempoMap.SampleAtTick(meterMap.BarStart(int64(report.From)))
	renderFrames := tempoMap.SampleAtTick(meterMap.BarStart(int64(renderBars))) + report.TailFrames
	report.Frames = renderFrames - fromFrame
	report.rangeSkip = fromFrame
	dataBytes := report.Frames * int64(encoder.frameBytes())
	if dataBytes > int64(^uint32(0))-int64(36+metadataBytes(len(report.tempoChanges))) {
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
	for i := range tracks {
		for _, ref := range score.Tracks[i].Chain {
			tracks[i].chain = append(tracks[i].chain, ref.Text)
		}
	}
	jobs := resolveJobs(opts.Jobs)
	report.noteLanes(opts.Jobs, jobs, 1, len(tracks), laneCounts{}, SerialReplay)
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
	if len(tracks) > 0 {
		tracks[0].automation = parameters
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
	report.chainStart, report.chainEnd = masterChainWindow(report.From, fromFrame, renderFrames, insertLatency, masterLatency)
	limiter, err := mix.NewLimiter(opts.SampleRate)
	if err != nil {
		return report, err
	}
	if err := writeWAVHeaderTempo(writer, opts.SampleRate, opts.Bits, uint32(dataBytes), len(report.tempoChanges)); err != nil {
		return report, err
	}
	busState.outputGain = outputGain
	var eventBuf [128]seq.Event
	block := make([]byte, max(opts.Block, limiter.LatencyFrames())*encoder.frameBytes())
	if cache := opts.MasterCache; cache != nil && stemsDir == "" && opts.NoteTap == nil {
		key := masterCacheKey{score: score, sampleRate: opts.SampleRate, block: opts.Block, from: opts.From, bars: opts.Bars, tailSec: opts.TailSec, assetRoot: opts.AssetRoot, assetDir: opts.AssetDir, baseline: opts.SamplerBaseline}
		use, replay, err := cache.begin(key, renderFrames+int64(insertLatency+masterLatency))
		if err != nil {
			return report, err
		}
		if use {
			busState.masterCache, busState.cacheReplay = cache, replay
		}
		if replay {
			return renderMasterReplay(writer, score, opts, cache, report, &encoder, limiter, busState, masterGain, block)
		}
	}
	// Only synthesis uses lanes; a replay above has none to synthesize.
	lanes := newTrackLanes(tracks, jobs, opts.Block)
	if lanes != nil {
		defer lanes.close()
		busState.lanes = lanes
	}
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
		entryBars, _ := meterMap.WholeBars(event.Tick, event.EndTick-event.Tick)
		entry := notation.SongEntry{Scene: semantic.Scenes[event.Scene].ID, Bars: int(entryBars)}
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
			for ti := range tracks {
				tracks[ti].noteTap, tracks[ti].tapTick, tracks[ti].tapIndex = opts.NoteTap, meterMap.BarStart(int64(bar)), ti
			}
			if err := applySceneBoundary(tracks, scene, stopIsAction, entryBar == 0); err != nil {
				return report, err
			}
			clock = tempoMap.ClockAtTick(meterMap.BarStart(int64(bar)))
			if entryBar == 0 {
				// The tempo steps first, so a delay division set by the same
				// scene fades once to its final length.
				if delayA != nil && delayA.SetTempo(clock.BPMMilli) != nil {
					return report, fmt.Errorf("delay does not fit the tempo of scene %s", entry.Scene)
				}
				if err := parameters.apply(entry.Scene); err != nil {
					return report, err
				}
			}
			var nextScene *notation.Scene
			if bar+1 < renderBars {
				nextScene = sceneAtBar(score, semantic, schedule, bar+1)
			}
			planSceneTransitions(tracks, nextScene, meterMap.BarStart(int64(bar+1)), tempoMap.SampleAtTick, stopIsAction)
			end := clock.SampleAtTick(meterMap.BarStart(int64(bar + 1)))
			for position < end {
				if err := advanceRenderChains(tracks, clock.TickAtSample(position)); err != nil {
					return report, err
				}
				blockEnd := nextRenderChainSample(tracks, clock, end)
				frames := opts.Block
				if position+int64(frames) > blockEnd {
					frames = int(blockEnd - position)
				}
				events = events[:0]
				for ti := range tracks {
					if tracks[ti].drums != nil {
						for lane, pattern := range tracks[ti].activeDrums {
							if pattern == nil {
								continue
							}
							n, overflow := seq.EventsAtTickInBlock(pattern, clock, uint8(ti), tracks[ti].currentSlot, tracks[ti].startTick, position, frames, eventBuf[:])
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
					n, overflow := seq.EventsWithGatesAtTickInBlock(tracks[ti].active, clock, uint8(ti), tracks[ti].currentSlot, tracks[ti].startTick, position, frames, eventBuf[:])
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
						n, overflow = seq.EventsWithGatesAtTickInBlock(&tracks[ti].pending, clock, uint8(ti), tracks[ti].pendingSlot, tracks[ti].pendingStartTick, position, frames, eventBuf[:])
						if overflow {
							return report, fmt.Errorf("too many pending gate events in render block")
						}
						for _, event := range eventBuf[:n] {
							// A note that glides into the next pattern keeps sounding
							// until the target note starts, which a swung odd step
							// delays past the slide's own overlap release; the engine
							// skips that release the same way (processPatternEvents).
							if event.Kind == seq.NoteOff && !(tracks[ti].slideFrom != 0 && event.NoteID == tracks[ti].slideFrom && event.Sample <= tracks[ti].slideAt) {
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
						return expressionEventPriority(a.event.Kind) - expressionEventPriority(b.event.Kind)
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
				if opts.NoteTap != nil {
					for _, event := range events {
						if event.event.Kind != seq.NoteExpression {
							opts.NoteTap(event.track, event.event)
						}
					}
				}
				if err := renderBlock(writer, tracks, delayA, reverbB, compMusic, compSidechainTrack, opts.MasterBiasL, opts.MasterBiasR, masterGain, busState, limiter, stems, &encoder, events, position, frames, block, &report); err != nil {
					return report, err
				}
				position += int64(frames)
			}
			bar++
		}
	}
	for ti := range tracks {
		tracks[ti].noteTap, tracks[ti].tapTick, tracks[ti].tapIndex = opts.NoteTap, meterMap.BarStart(int64(renderBars)), ti
		if tracks[ti].drums != nil {
			for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
				tracks[ti].drums.NoteOff(lane)
			}
		} else {
			tracks[ti].tapRelease()
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
	if busState.masterCache != nil {
		if err := busState.masterCache.finishCapture(); err != nil {
			return report, err
		}
	}
	if err := finishWAV(writer, score, limiter, stems, &encoder, block, &report); err != nil {
		return report, err
	}
	if stems != nil {
		if err := stems.finish(uint32(score.TempoMilli)); err != nil {
			return report, err
		}
	}
	report.noteLanes(opts.Jobs, jobs, lanes.lanes(), len(tracks), lanes.counts(), "")
	return report, nil
}

// authoredMasterLevelDB returns the master level the score sets in dB, and
// whether it sets one. Both renderers apply it with the export gain.
func authoredMasterLevelDB(p *project.Project) (float64, bool) {
	if p.Master == nil {
		return 0, false
	}
	if level := p.Master.Mixer.Level; level != nil && level.Unit == "db" && level.Number != nil {
		return *level.Number, true
	}
	return 0, false
}

// directMasterGains returns the combined master gain in dB that a direct
// render reports, and the gain factors its master stage applies ahead of the
// master chain (with the authored level) and after it. Without a master chain
// every gain goes in ahead of the safety limiter as one factor.
func directMasterGains(opts Options, authoredGainDB float64, hasLevel, hasChain bool) (gainDB float64, before, after float32) {
	gainDB = opts.MasterGainDB
	if !hasChain {
		gainDB += opts.MasterInputGainDB
	}
	if hasLevel {
		gainDB += authoredGainDB
	}
	before, after = 1, 1
	if gainDB != 0 {
		before = float32(detmath.Pow(10, gainDB/20))
	}
	if hasChain {
		before = float32(detmath.Pow(10, (authoredGainDB+opts.MasterInputGainDB)/20))
		after = float32(detmath.Pow(10, opts.MasterGainDB/20))
	}
	return gainDB, before, after
}

// finishWAV drains the safety limiter and appends the timing chunk.
func finishWAV(w io.Writer, score *notation.Score, limiter *mix.Limiter, stems *stemOutput, encoder *wavEncoder, buffer []byte, report *Report) error {
	if err := flushLimiter(w, limiter, stems, encoder, buffer, report); err != nil {
		return err
	}
	if report.writtenFrames != report.Frames {
		return fmt.Errorf("limiter wrote %d frames, expected %d", report.writtenFrames, report.Frames)
	}
	return writeMetadata(w, uint32(score.TempoMilli), uint32(report.Bars), uint32(report.TailFrames), report.tempoChanges)
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
					if !patternBoundToTrack(score, pattern.Name, source.Name) {
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
				if !patternBoundToTrack(score, pattern.Name, source.Name) {
					continue
				}
				compiled, err := project.CompilePattern(score, pattern, source)
				if err != nil {
					return nil, err
				}
				if compiled[0].Pattern.Expression != nil {
					return nil, fmt.Errorf("track %s: sample voices do not support per-note expression", source.Name)
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
				bindings, err := project.CompileKitTrackWithSamplers(kitDefinition, programs, semantic.SamplerPackSet(), semantic.Tracks[sourceIndex].Params, sampleRate)
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
					case engine.KitLaneSampler:
						// The direct mixer has no pack voices; UsesScheduledEngine sends such scores to the engine.
						err = fmt.Errorf("sampler kit lanes need the scheduled engine")
					default:
						err = fmt.Errorf("unknown kit lane kind %d", binding.Kind)
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
							if err := laneStepNotes(&p, uint8(lane)); err != nil {
								return nil, fmt.Errorf("pattern %s lane %s: %w", pattern.Name, name, err)
							}
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
			track := trackRuntime{name: source.Name, mixer: trackMix, voice: acidVoice{voice}, expressive: acidVoice{voice}, patterns: map[string]seq.Pattern{}}
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
		if (source.Kind == "piano" || keyboard.ID(source.Kind) != 0) && programs[source.Kind] == nil {
			var voice keyvoice.Voice
			var err error
			if keyboard.ID(source.Kind) != 0 {
				spec, e := project.CompileKeysSpec(source)
				if e != nil {
					return nil, e
				}
				voice, err = keyboard.NewCalibrated(sampleRate, &spec)
			} else {
				voice, err = piano.New(sampleRate)
				if err == nil {
					voice = trimmedVoice{voice, keyvoice.PianoLevelTrim}
				}
				if err == nil {
					sustain, e := project.CompilePianoSustain(source)
					if e != nil {
						return nil, e
					}
					err = voice.SetSustain(sustain)
				}
			}
			if err != nil {
				return nil, fmt.Errorf("track %s: %w", source.Name, err)
			}
			track := trackRuntime{name: source.Name, mixer: trackMix, voice: &pianoVoice{Instrument: voice}, patterns: map[string]seq.Pattern{}}
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
				if compiled[0].Pattern.Expression != nil {
					return nil, fmt.Errorf("track %s: piano voices do not support per-note expression", source.Name)
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
			sustain, err := project.CompileModalSustain(source)
			if err != nil {
				return nil, fmt.Errorf("track %s: %w", source.Name, err)
			}
			if err := voice.SetSustain(sustain); err != nil {
				return nil, fmt.Errorf("track %s: %w", source.Name, err)
			}
			track := trackRuntime{name: source.Name, mixer: trackMix, voice: modalVoice{voice}, patterns: map[string]seq.Pattern{}}
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
			voice, err := graph.NewVoice(kernelProgram, sampleRate)
			if err != nil {
				return nil, err
			}
			track.voice, track.expressive = customVoice{voice}, customVoice{voice}
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
	tick := p.MeterMap().BarStart(int64(bar))
	i := sort.Search(len(schedule), func(i int) bool { return tick < schedule[i].EndTick })
	if i == len(schedule) {
		return nil
	}
	return findScene(score, p.Scenes[schedule[i].Scene].ID)
}

func planSceneTransitions(tracks []trackRuntime, next *notation.Scene, boundaryTick int64, sampleAt func(int64) int64, stopIsAction bool) {
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
			track.transition = sceneTransitionFor(track.active, &target, uint8(ti), track.currentSlot, track.patternSlots[binding.Pattern], track.startTick, boundaryTick, sampleAt)
			break
		}
	}
}

// sceneTransitionFor decides whether the note that slides out of the playing
// pattern at a scene boundary glides into the next pattern's first note, and
// when it is released otherwise. It counts steps on each pattern's own grid
// from the playing pattern's start, as the engine does (normalSlideRelease and
// switchWouldSlide in kernel/engine/pattern.go): counting on the fixed
// sixteenth grid named a note that never played for any other grid, so a slide
// across a scene switch was dropped on this path and kept by the engine.
func sceneTransitionFor(source, target *seq.Pattern, track, sourceSlot, targetSlot uint8, startTick, boundaryTick int64, sampleAt func(int64) int64) sceneTransition {
	grid := source.GridTicks()
	if boundaryTick < grid || (boundaryTick-startTick)%grid != 0 {
		return sceneTransition{}
	}
	lastStep := (boundaryTick-startTick%grid)/grid - 1
	local := lastStep - startTick/grid
	if local < 0 {
		return sceneTransition{}
	}
	index := uint8(local % int64(source.Len))
	step, err := seq.UnpackStep(source.Steps[index])
	if err != nil || !step.Gate || !step.Slide || !seq.ProbabilityHit(step.Probability, source.Seed, track, sourceSlot, local/int64(source.Len), index) {
		return sceneTransition{}
	}
	swing := (grid*int64(source.SwingPermille) + 500) / 1000
	stepStart, stepEnd := lastStep*grid+startTick%grid, boundaryTick
	if lastStep&1 != 0 {
		stepStart += swing
	}
	if (lastStep+1)&1 != 0 {
		stepEnd += swing
	}
	onset := seq.RatchetTick(stepStart, stepEnd, step.Ratchet, step.Ratchet-1)
	gateTicks := (stepEnd - onset) * int64(source.GatePercent) / 100
	if gateTicks < 30 {
		gateTicks = 30
	}
	offTick := onset + gateTicks
	sourceNoteID := lastStep*8 + int64(step.Ratchet)
	targetStep := boundaryTick / target.GridTicks()
	targetIndex := uint8(targetStep % int64(target.Len))
	next, err := seq.UnpackStep(target.Steps[targetIndex])
	carry := err == nil && target.Chords[targetIndex].Count == 0 && next.Gate && !next.Tie && seq.ProbabilityHit(next.Probability, target.Seed, track, targetSlot, targetStep/int64(target.Len), targetIndex)
	// The target note starts late by the target pattern's swing when the bar
	// line falls on an odd step of its grid (1/2t, 1/3, 1/1), as in the
	// engine's switchOnsetSample.
	targetTick := boundaryTick
	if targetStep&1 != 0 {
		targetTick += (target.GridTicks()*int64(target.SwingPermille) + 500) / 1000
	}
	return sceneTransition{
		valid: true, carry: carry, sourceNoteID: sourceNoteID,
		boundarySample: sampleAt(boundaryTick), targetSample: sampleAt(targetTick),
		release: seq.Event{Kind: seq.NoteOff, NoteID: sourceNoteID, Tick: offTick, Sample: sampleAt(offTick), Track: track},
	}
}

// tapRelease reports the release of the sounding pattern note at the scene
// boundary, before its identity is cleared.
func (t *trackRuntime) tapRelease() {
	if t.noteTap != nil && t.activeGen != 0 && t.activeNoteID != 0 {
		t.noteTap(t.tapIndex, seq.Event{Kind: seq.NoteOff, NoteID: t.activeNoteID, Tick: t.tapTick})
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
			if binding.Pattern != "keep" {
				tracks[ti].chain = nil
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
				tracks[ti].tapRelease()
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
						tracks[ti].tapRelease()
						tracks[ti].voice.NoteOff()
						tracks[ti].activeGen = 0
					}
				}
				if tracks[ti].active != nil && tracks[ti].activeGen == tracks[ti].generation {
					tracks[ti].pendingStartTick = tracks[ti].startTick
					tracks[ti].pending = tracks[ti].current
					tracks[ti].pendingGen = tracks[ti].generation
					tracks[ti].pendingSlot = tracks[ti].currentSlot
					tracks[ti].hasPendingGate = true
				}
				tracks[ti].startTick = 0
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
	lanes := busState.lanes
	if lanes != nil {
		lanes.begin()
		defer lanes.finish()
	}
	cache := busState.masterCache
	if cache != nil {
		if err := cache.beginBlock(frames, busState.cacheReplay); err != nil {
			return err
		}
		if cache.disabled {
			cache = nil
		}
	}
	for frame := 0; frame < frames; frame++ {
		sample := start + int64(frame)
		if len(tracks) > 0 {
			if err := tracks[0].automation.advanceAutomation(sample); err != nil {
				return err
			}
		}
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
				} else if event.event.Kind == seq.NoteOn {
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
			} else if event.event.Kind == seq.NoteExpression {
				if track.activeGen == event.generation && track.activeNoteID != 0 && track.active != nil {
					track.applyExpression(track.active.ExpressionAt(int(event.event.StepIndex)))
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
					} else if event.event.NoteCount > 1 && isSamplerVoice(track.voice) {
						// Sampler voices are polyphonic: a chord starts every note, as the engine does.
						notes := event.event.Notes[:event.event.NoteCount]
						if pack, ok := track.voice.(*packVoice); ok {
							pack.NoteChord(notes, event.event.Velocity, event.event.Slide)
						} else {
							for _, note := range notes {
								track.voice.NoteOn(note, event.event.Velocity, event.event.Accent, event.event.Slide)
							}
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
				if track.active != nil {
					track.applyExpression(track.active.ExpressionAt(int(event.event.StepIndex)))
				}
				track.hasPendingGate = false
				track.slideFrom, track.slideAt = 0, 0
			}
			eventIndex++
		}
		if lanes != nil && frame >= lanes.end {
			// The tracks' own signals are fixed until the next event or
			// automation point, so lanes may synthesize them up to there.
			end := frames
			if eventIndex < len(events) {
				if next := events[eventIndex].event.Sample - start; next > int64(frame) && next < int64(end) {
					end = int(next)
				}
			}
			if len(tracks) > 0 {
				if next, ok := tracks[0].automation.nextAutomation(); ok && next-start > int64(frame) && next-start < int64(end) {
					end = int(next - start)
				}
			}
			lanes.prepare(tracks, frame, end)
		}
		var dry mix.Dry
		var sendAL, sendAR, sendBL, sendBR, sideL, sideR float32
		for ti := range tracks {
			if tracks[ti].parameters != nil {
				tracks[ti].parameters.advance(&tracks[ti])
			}
			var l, r float32
			var err error
			if lanes != nil && lanes.prepared {
				l, r, err = lanes.sample(ti, frame)
			} else {
				l, r, err = tracks[ti].nextRaw()
			}
			if err != nil {
				return err
			}
			if tracks[ti].sendA > 0 && !tracks[ti].muted {
				if tracks[ti].sendAPre {
					sendAL += float32(l * tracks[ti].sendA * tracks[ti].sendPreGain)
					sendAR += float32(r * tracks[ti].sendA * tracks[ti].sendPreGain)
				} else {
					sendAL += float32(l * tracks[ti].mixer.Left * tracks[ti].sendA)
					sendAR += float32(r * tracks[ti].mixer.Right * tracks[ti].sendA)
				}
			}
			if tracks[ti].sendB > 0 && !tracks[ti].muted {
				if tracks[ti].sendBPre {
					sendBL += float32(l * tracks[ti].sendB * tracks[ti].sendPreGain)
					sendBR += float32(r * tracks[ti].sendB * tracks[ti].sendPreGain)
				} else {
					sendBL += float32(l * tracks[ti].mixer.Left * tracks[ti].sendB)
					sendBR += float32(r * tracks[ti].mixer.Right * tracks[ti].sendB)
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
		if cache != nil {
			if busState.cacheReplay {
				left, right = cache.block[2*frame], cache.block[2*frame+1]
			} else {
				cache.block = append(cache.block, left, right)
			}
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
			if busState.masterLimiter != nil {
				report.noteMasterChainReduction(sample, busState.masterLimiter.LimiterGainReductionDB())
			}
			left *= busState.outputGain
			right *= busState.outputGain
		}
		// Export DC correction: after the master chain and every gain stage.
		if masterBiasL != 0 || masterBiasR != 0 {
			left += masterBiasL
			right += masterBiasR
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
	if cache != nil && !busState.cacheReplay {
		cache.endBlock()
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

// nextRaw renders one frame of the track's own signal: its voice and its
// drive insert or latency alignment, before the mixer. It touches only the
// track's own state, so tracks may render on different goroutines.
func (track *trackRuntime) nextRaw() (l, r float32, err error) {
	if track.drums != nil {
		l, r = track.drums.NextStereo()
		if track.drums.Fault() {
			return 0, 0, fmt.Errorf("drum DSP fault on %s", track.name)
		}
	} else if voice, ok := track.voice.(*pianoVoice); ok {
		l, r = voice.NextStereo()
	} else if track.prepared != nil {
		l, r = track.prepared.NextStereo()
	} else if track.legacyPoly != nil {
		l, r = track.legacyPoly.NextStereo()
	} else if track.poly != nil {
		l = track.poly.Next()
		r = l
	} else {
		if voice, ok := track.voice.(*packVoice); ok {
			l, r = voice.NextStereo()
			if voice.fault != nil {
				return 0, 0, fmt.Errorf("sampler %s: %w", track.name, voice.fault)
			}
		} else {
			mono := track.voice.Next()
			l, r = mono, mono
		}
	}
	if track.insert != nil {
		l, r = track.insert.Process(l, r)
		if track.insert.Fault() {
			return 0, 0, fmt.Errorf("drive DSP fault on %s", track.name)
		}
	} else if track.align != nil {
		l, r = track.align.Process(l, r)
	}
	return l, r, nil
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
//
// Version 1 is 24 bytes: the score tempo, the bars, and the tail. A song whose
// scenes change tempo writes version 2, which adds one (bar, tempo) pair for
// each step after those fields. A song with a single tempo still writes the
// exact version 1 bytes.
func writeMetadata(w io.Writer, tempoMilli, bars, tailFrames uint32, changes []project.TempoChange) error {
	chunk := make([]byte, metadataBytes(len(changes)))
	copy(chunk[:4], "cica")
	binary.LittleEndian.PutUint32(chunk[4:8], uint32(len(chunk)-8))
	binary.LittleEndian.PutUint32(chunk[8:12], 1)
	if len(changes) > 0 {
		binary.LittleEndian.PutUint32(chunk[8:12], 2)
	}
	binary.LittleEndian.PutUint32(chunk[12:16], tempoMilli)
	binary.LittleEndian.PutUint32(chunk[16:20], bars)
	binary.LittleEndian.PutUint32(chunk[20:24], tailFrames)
	for i, change := range changes {
		binary.LittleEndian.PutUint32(chunk[24+8*i:], uint32(change.Bar))
		binary.LittleEndian.PutUint32(chunk[28+8*i:], uint32(change.BPMMilli))
	}
	n, err := w.Write(chunk)
	if err != nil {
		return err
	}
	if n != len(chunk) {
		return io.ErrShortWrite
	}
	return nil
}

// metadataBytes is the size of the whole cica chunk, header included.
func metadataBytes(changes int) int { return 24 + 8*changes }

func expressionEventPriority(kind seq.EventKind) int {
	if kind == seq.NoteOff {
		return 0
	}
	if kind == seq.NoteOn {
		return 1
	}
	return 2
}

func patternBoundToTrack(score *notation.Score, pattern, track string) bool {
	for _, source := range score.Tracks {
		if source.Name == track {
			for _, ref := range source.Chain {
				if ref.Text == pattern {
					return true
				}
			}
		}
	}
	for _, scene := range score.Scenes {
		for _, binding := range scene.Bindings {
			if binding.Track == track && binding.Pattern == pattern {
				return true
			}
		}
	}
	return false
}

func hasPackSampler(score *notation.Score) bool {
	for _, sampler := range score.Samplers {
		if sampler.Pack != "" {
			return true
		}
	}
	return false
}

// hasKitSampler reports whether an authored kit binds a lane to a sampler. The
// direct WAV mixer has no pack voices, so such a score renders through the
// scheduled engine even when the sampler baseline is requested.
func hasKitSampler(score *notation.Score) bool {
	declared := map[string]bool{}
	for _, sampler := range score.Samplers {
		declared[sampler.Name] = true
	}
	for _, kit := range score.Kits {
		for _, binding := range kit.Bindings {
			if name, _, ok := strings.Cut(binding.Target, "."); ok && declared[name] {
				return true
			}
		}
	}
	return false
}

// isSamplerVoice reports whether a direct-mixer voice is a polyphonic sampler
// (an asset sampler or a pack instrument).
func isSamplerVoice(voice monoVoice) bool {
	switch voice.(type) {
	case *preparedVoice, *packVoice:
		return true
	}
	return false
}

// movesInstrumentParam reports whether a scene setting or automation lane
// moves an authored instrument's own parameter. Only the engine drives those.
func movesInstrumentParam(score *notation.Score) bool {
	// A preset track plays its target instrument.
	if resolved, _ := notation.ResolvePresets(score); resolved != nil {
		score = resolved
	}
	params := map[string]map[string]bool{} // instrument -> declared parameters
	for _, inst := range score.Instruments {
		params[inst.Name] = map[string]bool{}
		for _, param := range inst.Params {
			params[inst.Name][param.Name] = true
		}
	}
	kinds := map[string]string{}
	for _, track := range score.Tracks {
		kinds[track.Name] = track.Kind
	}
	moves := func(path string) bool {
		owner, setting, ok := strings.Cut(path, ".")
		kind, track := kinds[owner]
		return ok && track && params[kind][setting]
	}
	for _, lane := range score.Automation {
		if moves(lane.Path) {
			return true
		}
	}
	for _, scene := range score.Scenes {
		for _, setting := range scene.Settings {
			if moves(setting.Path) {
				return true
			}
		}
	}
	return false
}
