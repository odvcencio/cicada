// Package liveplay streams the native engine to an audio device and lands
// validated score replacements on exact musical bar boundaries.
package liveplay

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
)

const blockFrames = 256

// Human-readable note names are prepared once, outside the audio reader.
var liveNoteNames = func() [128]string {
	var names [128]string
	for i := range names {
		names[i] = strconv.Itoa(i)
	}
	return names
}()

type Score struct {
	Engine          *engine.Engine
	SampleRate      int
	BPMMilli        int64
	Name            string
	Revision        string             // Committed source revision supplied by Studio.
	SceneParameters [][]ParameterValue // Ordered committed settings at scene launch.
	SceneIDs        []string           // engine scene indices in source order
	Tracks          []TrackSlots       // engine track and slot indices in source order
	Song            []SongEntry        // song entries with one-based start bars
	Parameters      []ParameterValue
	HasReturnA      bool
	HasReturnB      bool
	HasSFX          bool
	Range           *PlaybackRange // optional listening range prepared with a score edit
	trackNames      *trackNameSnapshot
	publication     uint64 // Assigned by Offer, ordered with patch publications.
}

type trackNameSnapshot struct {
	ids           [engine.MaxTracks]string
	incarnations  [engine.MaxTracks]uint64 // Assigned before publication when this score plays.
	kinds         [engine.MaxTracks]string
	poly          [engine.MaxTracks]bool
	count         uint8
	committed     [engine.MaxTracks + 1][kernel.ParamCount]atomic.Uint64 // Render writes; controls read.
	defaults      engine.ParameterState                                  // Render-owned saved values, separate from scene values.
	previewParams [engine.MaxTracks][kernel.ParamCount]bool
	previewValues [engine.MaxTracks]engine.PreviewParamValidator
}

// PreviewTarget retains the immutable playing-track metadata and incarnation
// resolved by a control caller, even if publication happens after activation.
type PreviewTarget struct {
	player *Player
	names  *trackNameSnapshot
	track  uint8
}

type ParameterValue struct {
	Track uint8
	ID    kernel.ParamID
	Value float32
}

// MeterValue is one meter. Peak and RMS are the mono values. Track meters also
// fill PeakL, PeakR, RMSL and RMSR; returns and buses leave those at zero.
type MeterValue struct {
	Peak, RMS                float32
	PeakL, PeakR, RMSL, RMSR float32
}

type MeterFrame struct {
	Tick                           int64
	TrackCount                     uint8
	TrackIDs                       [engine.MaxTracks]string
	Tracks                         [engine.MaxTracks]MeterValue
	HasReturnA, HasReturnB, HasSFX bool
	ReturnA, ReturnB               MeterValue
	Music, SFX, MasterPre          MeterValue
	MasterPeak, MasterRMS          float32
	CompGR, LimiterGR              float32
	stereo                         bool // the engine sent per-channel track quantities
}

// PreLimiterOver reports a block whose pre-limiter peak exceeded -0.3 dBFS.
func (f MeterFrame) PreLimiterOver() bool {
	return 20*math.Log10(float64(f.MasterPre.Peak)) > -0.3
}

type parameterOverride struct {
	value   float32
	version uint64
	active  bool
}

// A track incarnation owns its render bookkeeping across control snapshot copies.
// Only retirement is read by control callers; the audio reader owns the rest.
type overrideState struct {
	appliedVersions   [kernel.ParamCount]uint64
	clearedVersions   [kernel.ParamCount]uint64
	cancelledVersions [kernel.ParamCount]uint64
	retiredVersions   [kernel.ParamCount]atomic.Uint64
}

type trackOverrides struct {
	values [kernel.ParamCount]parameterOverride
	state  *overrideState
}

type overrideTrack struct {
	id          string
	incarnation uint64
}

type liveOverrides struct {
	tracks map[overrideTrack]*trackOverrides // Empty ID/token is global.
}

type overrideClear struct {
	track   overrideTrack
	id      kernel.ParamID
	version uint64
}

type SongEntry struct {
	Scene    string
	StartBar uint32
}

type TrackSlots struct {
	ID      string
	Kind    string
	Pitched bool // custom graph instrument; Kind retains its source name
	Slots   [16]string
}

type noteInput struct {
	ID                           string
	Track                        string
	Note                         uint8
	Velocity                     uint8
	On                           bool
	NoteID                       uint16
	KernelID                     uint16
	HasControls                  bool
	Expression                   bool
	Group                        string
	BindGroup                    bool
	PitchCents, Pressure, Timbre float32
}

type noteBatch struct {
	inputs  [256]noteInput
	count   uint16
	presses uint16
	epoch   uint64
}

type sceneLaunch struct {
	id         uint64
	name       string
	targetTick int64
	quantize   cmd.Quantize
	// submitted flips once, in place, when the audio goroutine hands the launch to the engine.
	// Every other field is immutable after the record is published.
	submitted atomic.Bool
}

type slotLaunch struct {
	request    SlotRequest
	id         uint64
	TargetTick int64
	quantize   cmd.Quantize
	submitted  bool
}

type Event struct {
	Revision string
	Bar      int64 // one-based bar that has just begun
	Name     string
	Kind     string // edit, scene, scene-error, preview-error, or a live input event
	Track    string // set for one-track slot events
}

type SlotRequest struct {
	Track, Pattern string
}

type StartRequest struct {
	ID    uint64
	Index int
	Scene string
}

type slotLaunchBatch struct{ requests [engine.MaxTracks]slotLaunch }

// Position is the most recently rendered musical location. Step is one-based
// within a 16-step bar; the audio device may still be playing buffered frames.
type Position struct {
	Bar  int64 `json:"bar"`
	Step int64 `json:"step"`
}

// Player implements io.Reader for interleaved stereo float32 little-endian PCM.
// Read is owned by the audio device; Offer may be called from a file watcher.
type Player struct {
	retirement              *engineRetirement
	retirementFault         atomic.Bool
	current                 Score
	offerMu                 sync.Mutex // control-only ownership transfer and teardown
	admissionClosed         bool
	closed                  chan struct{}
	closedOnce              sync.Once
	previous                *engine.Engine
	clock                   seq.Clock
	nextClockEngine         *engine.Engine
	nextClockBar            int64
	nextClockTempo          int64
	rate                    int
	sample                  int64
	bar                     int64
	nextBarSample           int64
	fadeTotal               int
	fadeRemaining           int
	offers                  chan Score
	patches                 chan *Patch
	deferredPatch           *Patch // Render-owned head batch carried to the next block.
	reconstructed           engine.ParameterState
	blockParameters         engine.ParameterState
	initialParameters       bool
	transportQueued         bool       // OpSeek/OpPlay queued for the block about to render; render-owned.
	publicationMu           sync.Mutex // Orders patches/offers with Close, never Read.
	publication             uint64
	offerPublication        atomic.Uint64
	landedRevision          atomic.Uint64
	launches                atomic.Pointer[sceneLaunch]
	launchSequence          atomic.Uint64
	starts                  chan StartRequest
	startMu                 sync.Mutex // control calls only; the audio reader never locks
	startSequence           atomic.Uint64
	pendingStart            atomic.Uint64
	slotLaunches            atomic.Pointer[slotLaunchBatch]
	notes                   atomic.Pointer[noteBatch]
	noteEpoch               atomic.Uint64 // odd: release overload; batches carry their admission epoch
	heldNotes               [128]noteInput
	heldOrder               [128]uint64
	noteFinal               [engine.MaxTracks][128]cmd.Command
	noteTouched             [engine.MaxTracks][128]bool
	noteOrder               uint64
	nextLiveIdentity        uint16
	ownedFinal              [256]ownedNoteCommands
	ownedFinalCount         int
	liveGroups              [256]liveControlGroup
	liveGroupLimit          int
	events                  chan Event
	left, right             [blockFrames]float32
	oldL, oldR              [blockFrames]float32
	pcm                     [blockFrames * 8]byte
	buffered                int
	read                    int
	fault                   error
	jumpFadeRemaining       int
	position                atomic.Uint64
	tempoMilli              atomic.Int64
	scene                   atomic.Pointer[string]
	sceneSequence           uint64
	sceneEngine             *engine.Engine
	overrides               atomic.Pointer[liveOverrides]
	trackNames              atomic.Pointer[trackNameSnapshot]
	overrideSequence        atomic.Uint64
	trackIncarnation        uint64 // Render-owned identity generation.
	overrideClears          chan overrideClear
	overrideHook            func(snapshotLoaded bool)
	trackCount              atomic.Uint32
	appliedOverrideSnapshot *liveOverrides
	appliedOverrideEngine   *engine.Engine
	meters                  chan MeterFrame
	meterScratch            MeterFrame
	meterTick               int64
	meterStarted            bool
	loudness                *liveLoudness
	publishMu               sync.Mutex
	publisher               *Publisher
	hostPaused              atomic.Bool
	cpuLoad                 float64       // render-owned exponential average
	cpuLoadBits             atomic.Uint64 // float64 bits of cpuLoad for readers
	telemetry               telemetryState
	rangeRequest            atomic.Pointer[playbackRangeRequest]
	rangeState              atomic.Pointer[PlaybackRange]
	playbackRange           *PlaybackRange // audio reader owns the active range
	rangeFinished           atomic.Bool
	rangeCycles             atomic.Uint64
	streamEngine            atomic.Pointer[engine.Engine]
}

// New consumes the initial engine, closing it if player admission fails.
func New(initial Score, rate int) (created *Player, admissionError error) {
	defer func() {
		if admissionError != nil && initial.Engine != nil {
			_ = initial.Engine.Close()
		}
	}()
	if len(initial.Tracks) > engine.MaxTracks {
		return nil, fmt.Errorf("live score exceeds %d tracks", engine.MaxTracks)
	}
	if initial.Engine == nil || initial.SampleRate != rate {
		return nil, fmt.Errorf("live score has no engine at %d Hz", rate)
	}
	if initial.Range != nil {
		if err := initial.Range.Validate(); err != nil {
			return nil, err
		}
	}
	clock, err := seq.NewClock(rate, initial.BPMMilli)
	if err != nil {
		return nil, err
	}
	clock.BPMMilli = initial.Engine.TempoAtTick(0)
	masterLoudness, err := newLiveLoudness(rate)
	if err != nil {
		return nil, err
	}
	if !initial.Engine.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
		masterLoudness.close()
		return nil, fmt.Errorf("live engine rejected play")
	}
	initial.Engine.SetStereoMeters(true)
	primeTick := int64(0)
	if initial.Range != nil {
		primeTick = initial.Range.StartTick
	}
	if err := primeStreamScore(initial, primeTick); err != nil {
		initial.Engine.Close()
		masterLoudness.close()
		return nil, err
	}
	initial.trackNames = makeTrackNames(initial)
	p := &Player{
		current: initial, clock: clock, rate: rate,
		nextBarSample: clock.SampleAtTick(seq.TicksPerBar),
		offers:        make(chan Score, 1), starts: make(chan StartRequest, 1), events: make(chan Event, 32),
		meters: make(chan MeterFrame, 1), loudness: masterLoudness,
		overrideClears:    make(chan overrideClear, 64),
		patches:           make(chan *Patch, 32),
		closed:            make(chan struct{}),
		initialParameters: true, transportQueued: true,
	}
	p.retirement = newEngineRetirement(nil)
	p.retirement.register(initial.Engine)
	p.streamEngine.Store(initial.Engine)
	p.overrides.Store(&liveOverrides{})
	p.trackCount.Store(uint32(initial.Engine.TrackCount()))
	p.assignTrackIncarnations(initial.trackNames)
	p.trackNames.Store(initial.trackNames)
	p.position.Store(1<<8 | 1)
	p.tempoMilli.Store(clock.BPMMilli)
	if len(initial.Song) != 0 {
		p.scene.Store(&p.current.Song[0].Scene)
	} else if len(initial.SceneIDs) != 0 {
		p.scene.Store(&p.current.SceneIDs[0])
	}
	if initial.Range != nil {
		_ = p.PlayRange(*initial.Range) // validated before creating the player
	}
	return p, nil
}

// ResetLoudness schedules a reset on the meter goroutine. It never waits for
// the audio reader or touches meter state owned by that goroutine.
func (p *Player) ResetLoudness() {
	if p.loudness != nil {
		p.loudness.resetMeter()
	}
}

// Loudness returns the latest lock-free snapshot from the off-thread meter.
func (p *Player) Loudness() LoudnessSnapshot {
	if p.loudness != nil {
		return p.loudness.metrics()
	}
	return LoudnessSnapshot{}
}

// Close permanently ends rendering, releases cancellation producers and stops
// the meter worker. Call it after the audio device has stopped reading this player.
func (p *Player) Close() {
	p.closedOnce.Do(func() {
		p.publicationMu.Lock()
		defer p.publicationMu.Unlock()
		close(p.closed)
		if p.deferredPatch != nil {
			p.deferredPatch.recycle()
			p.deferredPatch = nil
		}
		for {
			select {
			case patch := <-p.patches:
				patch.recycle()
			default:
				return
			}
		}
	})
	p.offerMu.Lock()
	defer p.offerMu.Unlock()
	if p.admissionClosed {
		return
	}
	p.admissionClosed = true
	if p.loudness != nil {
		p.loudness.close()
	}
	p.streamEngine.Store(nil)
	p.retirement.shutdown()
	p.previous = nil
	for {
		select {
		case <-p.offers:
		default:
			return
		}
	}
}

// StreamStats exposes immutable arena and atomic reader counters to host UIs.
func (p *Player) StreamStats() engine.StreamingStats {
	if e := p.streamEngine.Load(); e != nil {
		return e.StreamStats()
	}
	return engine.StreamingStats{}
}
func (p *Player) StreamError() error {
	if e := p.streamEngine.Load(); e != nil {
		return e.StreamError()
	}
	return nil
}
func primeStreamScore(score Score, tick int64) error {
	if score.Engine.StreamStats().ArenaBytes == 0 {
		return nil
	}
	commands := [2]cmd.Command{{Op: cmd.OpSeek, Track: 0xff, Arg0: uint32(tick / seq.TicksPerBar), Arg1: uint32(tick % seq.TicksPerBar)}, {Op: cmd.OpPlay, Track: 0xff}}
	if !score.Engine.PushBatch(commands[:]) {
		return fmt.Errorf("streamed score rejected preparation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := score.Engine.PrepareRender(ctx, min(blockFrames, 4096))
	return err
}

// Offer replaces any edit that has not landed yet with the newest valid score.
// Offer consumes score ownership, including failed admission or replacement.
func (p *Player) Offer(score Score) error {
	p.offerMu.Lock()
	defer p.offerMu.Unlock()
	accepted := false
	defer func() {
		if !accepted && score.Engine != nil {
			p.retirement.releaseUnused(score.Engine)
		}
	}()
	if p.admissionClosed {
		return fmt.Errorf("live player is closed")
	}
	if p.retirementFault.Load() {
		return errRetirementInvariant
	}
	if score.Engine != nil && p.retirement.find(score.Engine) != nil {
		accepted = true // ownership is already held by this player
		return fmt.Errorf("live score engine is already owned by this player")
	}
	if len(score.Tracks) > engine.MaxTracks {
		return fmt.Errorf("live score exceeds %d tracks", engine.MaxTracks)
	}
	if score.Engine == nil || score.SampleRate != p.rate {
		return fmt.Errorf("live score has no engine at %d Hz", p.rate)
	}
	if _, err := seq.NewClock(p.rate, score.BPMMilli); err != nil {
		return err
	}
	if score.Range != nil {
		if err := score.Range.Validate(); err != nil {
			return err
		}
	}
	// Every engine the player installs arrives through here (or New), so this
	// is the one place that turns on per-channel meters.
	score.Engine.SetStereoMeters(true)
	primeTick := p.Position().Bar * seq.TicksPerBar
	if score.Range != nil {
		primeTick = score.Range.StartTick
	}
	if err := primeStreamScore(score, primeTick); err != nil {
		score.Engine.Close()
		return err
	}
	score.trackNames = makeTrackNames(score)
	if !p.retirement.register(score.Engine) {
		return errRetirementBusy
	}
	// Patch publications and replacements share this order; Close excludes both.
	p.publicationMu.Lock()
	defer p.publicationMu.Unlock()
	select {
	case <-p.closed:
		return cmd.Error("player is closed")
	default:
	}
	p.publication++
	score.publication = p.publication
	p.offerPublication.Store(score.publication)
	for {
		select {
		case p.offers <- score:
			accepted = true
			return nil
		default:
			select {
			case discarded := <-p.offers:
				if discarded.Engine != nil {
					p.retirement.releaseUnused(discarded.Engine)
				}
			default:
			}
		}
	}
}

func (p *Player) Events() <-chan Event { return p.events }

func (p *Player) Meters() <-chan MeterFrame { return p.meters }

func makeTrackNames(score Score) *trackNameSnapshot {
	snapshot := &trackNameSnapshot{count: uint8(min(len(score.Tracks), engine.MaxTracks))}
	for _, parameter := range score.Parameters {
		snapshot.storeCommitted(parameter.Track, parameter.ID, parameter.Value)
		snapshot.defaults.Set(parameter.Track, parameter.ID, parameter.Value)
	}
	for index := 0; index < int(snapshot.count); index++ {
		snapshot.ids[index] = score.Tracks[index].ID
		snapshot.kinds[index] = score.Tracks[index].Kind
		if score.Engine != nil && score.Engine.TrackVoiceKind(index) == engine.VoiceKeys {
			snapshot.kinds[index] = "keys"
		} else if snapshot.kinds[index] == "piano" && score.Engine != nil && score.Engine.TrackVoiceKind(index) != engine.VoicePiano {
			snapshot.kinds[index] = "instrument"
		}
		if score.Tracks[index].Pitched && snapshot.kinds[index] != "keys" {
			snapshot.kinds[index] = "graph"
		}
		if score.Engine != nil {
			snapshot.poly[index] = score.Engine.TrackPolyphonic(index)
		}
	}
	if score.Engine != nil {
		for index := 0; index < min(score.Engine.TrackCount(), engine.MaxTracks); index++ {
			snapshot.previewValues[index] = score.Engine.PreviewParamValidator(index)
			family := "instrument"
			switch score.Engine.TrackVoiceKind(index) {
			case engine.VoiceAcid:
				family = "acid"
			case engine.VoiceDrums:
				family = "drums"
			case engine.VoicePiano:
				family = "piano"
			case engine.VoiceKeys:
				family = score.Engine.TrackKeysFamily(index).String()
			case engine.VoiceGuitar:
				family = "guitar"
			case engine.VoiceModal:
				family = "modal"
			}
			for _, spec := range kernel.Params {
				if descriptor, ok := paramdefs.Lookup(spec.Name); ok && spec.Live && spec.Scope == "track" {
					snapshot.previewParams[index][spec.ID] = paramdefs.HasVoice(descriptor, family)
				}
			}
		}
	}
	return snapshot
}

// TrackIndex resolves a stable score track ID against the engine that Read owns.
func (p *Player) TrackIndex(id string) (uint8, bool) {
	return p.trackNames.Load().trackIndex(id)
}

func (snapshot *trackNameSnapshot) trackIndex(id string) (uint8, bool) {
	if snapshot == nil {
		return 0, false
	}
	for index := 0; index < int(snapshot.count); index++ {
		if snapshot.ids[index] == id {
			return uint8(index), true
		}
	}
	return 0, false
}

func (snapshot *trackNameSnapshot) overrideTrack(track uint8) overrideTrack {
	if track == 0xff {
		return overrideTrack{}
	}
	return overrideTrack{id: snapshot.ids[track], incarnation: snapshot.incarnations[track]}
}

func (snapshot *trackNameSnapshot) containsTrack(identity overrideTrack) bool {
	index, ok := snapshot.trackIndex(identity.id)
	return ok && snapshot.incarnations[index] == identity.incarnation
}

// Assign only when a prepared score becomes the playing plan. The snapshot
// has not been published yet; control callers see its tokens as immutable.
// Reorders and value changes keep a token. Removal followed by re-add does not.
func (p *Player) assignTrackIncarnations(next *trackNameSnapshot) {
	if next == nil {
		return
	}
	previous := p.trackNames.Load()
	for index := 0; index < int(next.count); index++ {
		if old, ok := previous.trackIndex(next.ids[index]); ok {
			next.incarnations[index] = previous.incarnations[old]
		} else {
			p.trackIncarnation++
			next.incarnations[index] = p.trackIncarnation
		}
	}
}

// CommittedValue reads a parameter from the playing score's immutable snapshot.
// Offered scores become visible only when the render thread lands them.
func (p *Player) CommittedValue(track uint8, id kernel.ParamID) (float32, bool) {
	snapshot := p.trackNames.Load()
	if snapshot == nil {
		return 0, false
	}
	return snapshot.committedValue(track, id)
}

func (snapshot *trackNameSnapshot) storeCommitted(track uint8, id kernel.ParamID, value float32) {
	if track == 0xff {
		track = engine.ParameterGlobalSlot
	}
	if track <= engine.ParameterGlobalSlot && id < kernel.ParamCount {
		snapshot.committed[track][id].Store(1<<32 | uint64(math.Float32bits(value)))
	}
}

func (snapshot *trackNameSnapshot) committedValue(track uint8, id kernel.ParamID) (float32, bool) {
	if snapshot == nil || id >= kernel.ParamCount {
		return 0, false
	}
	if track == 0xff {
		track = engine.ParameterGlobalSlot
	}
	if track > engine.ParameterGlobalSlot {
		return 0, false
	}
	bits := snapshot.committed[track][id].Load()
	return math.Float32frombits(uint32(bits)), bits>>32 != 0
}

// OverrideValue returns the latest override that the render thread has not
// retired. Both the control snapshot and retirement versions are safe to read.
func (p *Player) OverrideValue(track uint8, id kernel.ParamID) (float32, bool) {
	if id >= kernel.ParamCount {
		return 0, false
	}
	snapshot := p.overrides.Load()
	if snapshot == nil {
		return 0, false
	}
	identity := overrideTrack{}
	if track != 0xff {
		names := p.trackNames.Load()
		if names == nil || track >= names.count {
			return 0, false
		}
		identity = names.overrideTrack(track)
	}
	overrides := snapshot.tracks[identity]
	if overrides == nil {
		return 0, false
	}
	override := overrides.values[id]
	return override.value, override.active && override.version != overrides.state.retiredVersions[id].Load()
}

// Note queues a live note for an acid, authored instrument, or drum track. Requests are
// resolved and applied by Read, so the control path never touches engine state.
func (p *Player) Note(track string, note, velocity int, on bool) error {
	return p.NoteID(track, note, velocity, on, fmt.Sprintf("legacy:%s:%d", track, note))
}

// NoteWithID preserves the numeric identity of an expressive note.
func (p *Player) NoteWithID(track string, note, velocity int, on bool, noteID uint16) error {
	return p.NoteOwnerID(track, note, velocity, on, fmt.Sprintf("mpe:%s:%d", track, noteID), noteID)
}

// NoteID binds a note to an immutable input owner.
func (p *Player) NoteID(track string, note, velocity int, on bool, id string) error {
	return p.NoteOwnerID(track, note, velocity, on, id, 0)
}

// NoteOwnerID retains both session ownership and the expressive command identity.
func (p *Player) NoteOwnerID(track string, note, velocity int, on bool, id string, noteID uint16) error {
	pressEpoch := p.noteEpoch.Load()
	if id == "" || len(id) > 1024 {
		return fmt.Errorf("note owner ID is required and must be bounded")
	}
	if noteID == 0xffff {
		return fmt.Errorf("note identity 65535 is reserved")
	}
	if track == "" {
		return fmt.Errorf("track is required")
	}
	if note < 0 || note > 127 {
		return fmt.Errorf("note must be in MIDI range 0–127")
	}
	if velocity < 0 || velocity > 127 {
		return fmt.Errorf("velocity must be in MIDI range 0–127")
	}
	snapshot := p.trackNames.Load()
	if snapshot == nil {
		return fmt.Errorf("track %q is not in the playing score", track)
	}
	for index := 0; index < int(snapshot.count); index++ {
		if snapshot.ids[index] != track {
			continue
		}
		kind := snapshot.kinds[index]
		if kind == "drums" {
			if _, ok := GMDrumLane(note); !ok {
				return fmt.Errorf("MIDI drum note %d is not in the General MIDI map", note)
			}
		} else if kind == "piano" || kind == "keys" {
			if note < 21 || note > 108 {
				return fmt.Errorf("%s note must be in MIDI range 21–108", kind)
			}
		} else if kind != "acid" && kind != "graph" && kind != "poly" {
			return fmt.Errorf("track %q does not accept live notes", track)
		}
		input := noteInput{ID: id, Track: track, Note: uint8(note), Velocity: uint8(velocity), On: on, NoteID: noteID}
		return p.queueOwnedNote(input, pressEpoch)
	}
	return fmt.Errorf("track %q is not in the playing score", track)
}

// NoteExpression queues a complete expression snapshot for one note. Values
// remain float32 throughout the host and command path, without MIDI 1.0's
// seven-bit controller reduction. The audio reader owns voice mutation.
func (p *Player) NoteExpression(track string, noteID uint16, pitchCents, pressure, timbre float32) error {
	return p.NoteExpressionOwner(track, noteID, fmt.Sprintf("mpe:%s:%d", track, noteID), pitchCents, pressure, timbre)
}

// NoteExpressionOwner preserves the input session namespace as well as its
// numeric note ID. Two clients may use the same ID without sharing controls.
func (p *Player) NoteExpressionOwner(track string, noteID uint16, owner string, pitchCents, pressure, timbre float32) error {
	if owner == "" || len(owner) > 1024 {
		return fmt.Errorf("invalid expression owner")
	}
	if noteID == 0 || noteID == 0xffff {
		return fmt.Errorf("expression needs a note identity from 1 to 65534")
	}
	if math.IsNaN(float64(pitchCents)) || math.IsInf(float64(pitchCents), 0) || pitchCents < -9600 || pitchCents > 9600 ||
		math.IsNaN(float64(pressure)) || math.IsInf(float64(pressure), 0) || pressure < 0 || pressure > 1 ||
		math.IsNaN(float64(timbre)) || math.IsInf(float64(timbre), 0) || timbre < 0 || timbre > 1 {
		return fmt.Errorf("expression needs finite pitch cents in -9600 to 9600 and pressure/timbre in 0 to 1")
	}
	snapshot := p.trackNames.Load()
	_, kind, found := findTrack(snapshot, track)
	if found && kind == "keys" {
		return fmt.Errorf("CICADA-UNSUPPORTED: modeled keyboards do not support per-note expression")
	}
	if !found || kind != "acid" && kind != "graph" && kind != "poly" {
		return fmt.Errorf("track %q is not a pitched track in the playing score", track)
	}
	return p.queueOwnedNote(noteInput{ID: owner, Track: track, NoteID: noteID, Expression: true, PitchCents: pitchCents, Pressure: pressure, Timbre: timbre}, p.noteEpoch.Load())
}

func (p *Player) queueOwnedNote(input noteInput, pressEpoch uint64) error {
	for {
		epoch := p.noteEpoch.Load()
		if input.On && (epoch&1 != 0 || epoch != pressEpoch) {
			return fmt.Errorf("live note release overload interrupted this press")
		}
		if epoch&1 != 0 {
			return nil // The barrier already owns this release.
		}
		old := p.notes.Load()
		next := &noteBatch{epoch: epoch}
		if old != nil && old.epoch == epoch {
			*next = *old
		}
		if next.count == uint16(len(next.inputs)) || input.On && next.presses >= 128 {
			if !input.On && !input.Expression && input.Group == "" {
				// Odd epochs release every native live owner and discard presses.
				// Reader-side epoch checks also reject a producer paused before CAS.
				if p.noteEpoch.CompareAndSwap(epoch, epoch+1) {
					return nil
				}
				continue
			}
			return fmt.Errorf("live note queue is full")
		}
		next.inputs[next.count] = input
		next.count++
		if input.On {
			next.presses++
		}
		if p.notes.CompareAndSwap(old, next) {
			if input.On && p.noteEpoch.Load() != pressEpoch {
				return fmt.Errorf("live note release overload interrupted this press")
			}
			return nil
		}
	}
}

// GMDrumLane returns Cicada's lane index for the supported General MIDI
// percussion notes. Other pitches are not drum triggers.
func GMDrumLane(note int) (uint16, bool) {
	switch note {
	case 36:
		return 0, true // bd
	case 37:
		return 5, true // rs
	case 38:
		return 1, true // sd
	case 39:
		return 4, true // cp
	case 41, 43:
		return 6, true // lt
	case 42:
		return 2, true // ch
	case 45, 47:
		return 7, true // mt
	case 46:
		return 3, true // oh
	case 48, 50:
		return 8, true // ht
	case 49, 57:
		return 10, true // cy
	case 56:
		return 9, true // cb
	default:
		return 0, false
	}
}

// SetParam accepts concurrent control calls. A snapshot CAS makes the latest
// value for each parameter win; Read never takes a mutex.
func (p *Player) SetParam(track uint8, id kernel.ParamID, value float32) error {
	_, err := p.SetPreview(track, id, value)
	return err
}

// SetPreview publishes an override using SetParam's snapshot CAS and returns
// its exact version, so a later cancellation cannot retire a newer gesture.
func (p *Player) SetPreview(track uint8, id kernel.ParamID, value float32) (uint64, error) {
	return p.setPreview(p.trackNames.Load(), track, id, value)
}

// SetTrackPreview resolves and validates an entity in one playing-score
// snapshot. Publication keeps its ID and incarnation across activation.
func (p *Player) SetTrackPreview(trackID string, id kernel.ParamID, value float32) (uint64, error) {
	target, err := p.ResolvePreviewTrack(trackID)
	if err != nil {
		return 0, err
	}
	return p.SetResolvedPreview(target, id, value)
}

// ResolvePreviewTrack captures a target from the playing score, independently
// of offered scores and without reading the engine on the control thread.
func (p *Player) ResolvePreviewTrack(trackID string) (PreviewTarget, error) {
	names := p.trackNames.Load()
	track, ok := names.trackIndex(trackID)
	if !ok {
		return PreviewTarget{}, fmt.Errorf("unknown preview track %q", trackID)
	}
	return PreviewTarget{player: p, names: names, track: track}, nil
}

// MonitorAmpTargets reads a bound track's current render targets. Call only on
// the audio thread immediately after this Player's Read; concurrent control
// goroutines must not use it. The original track incarnation must still play.
func (p *Player) MonitorAmpTargets(target PreviewTarget) (engine.AmpTargets, bool) {
	if target.player != p || target.names == nil || target.track >= target.names.count {
		return engine.AmpTargets{}, false
	}
	identity := target.names.overrideTrack(target.track)
	names := p.current.trackNames
	if names == nil {
		return engine.AmpTargets{}, false
	}
	index, ok := names.trackIndex(identity.id)
	if !ok || names.incarnations[index] != identity.incarnation {
		return engine.AmpTargets{}, false
	}
	return p.current.Engine.TrackAmpTargets(int(index))
}

// SetResolvedPreview publishes only to the player that resolved the target.
// Read retires stale incarnations without applying them to a replacement track.
func (p *Player) SetResolvedPreview(target PreviewTarget, id kernel.ParamID, value float32) (uint64, error) {
	if target.player != p || target.names == nil {
		return 0, fmt.Errorf("preview target belongs to a different player")
	}
	return p.setPreview(target.names, target.track, id, value)
}

func (p *Player) setPreview(names *trackNameSnapshot, track uint8, id kernel.ParamID, value float32) (uint64, error) {
	spec, ok := kernel.Param(id)
	if !ok || !spec.Live {
		return 0, fmt.Errorf("parameter is unknown or not live")
	}
	identity := overrideTrack{}
	var validator *engine.PreviewParamValidator
	if spec.Scope == "global" {
		if track != 0xff {
			return 0, fmt.Errorf("global parameter requires track 255")
		}
	} else if spec.Scope != "track" || names == nil || track >= names.count || names.ids[track] == "" {
		return 0, fmt.Errorf("track parameter index is out of range")
	} else {
		if !names.previewParams[track][id] {
			return 0, fmt.Errorf("parameter %q is unsupported by the track in the playing score", spec.Name)
		}
		identity = names.overrideTrack(track)
		validator = &names.previewValues[track]
	}
	if err := validatePreviewValue(spec, validator, value); err != nil {
		return 0, fmt.Errorf("parameter %q is rejected by the playing voice: %w", spec.Name, err)
	}
	version := p.overrideSequence.Add(1)
	for {
		old := p.overrides.Load()
		next := &liveOverrides{tracks: make(map[overrideTrack]*trackOverrides)}
		playing := p.trackNames.Load()
		if old != nil {
			for identity, overrides := range old.tracks {
				// Discard absent identities only after audio-thread retirement.
				// This keeps repeated topology changes from growing the map.
				if identity.id != "" && !playing.containsTrack(identity) && overrides.retired() {
					continue
				}
				next.tracks[identity] = overrides
			}
		}
		overrides := new(trackOverrides)
		if previous := next.tracks[identity]; previous != nil {
			*overrides = *previous
		} else {
			overrides.state = new(overrideState)
		}
		overrides.values[id] = parameterOverride{value: value, version: version, active: true}
		next.tracks[identity] = overrides
		if p.overrides.CompareAndSwap(old, next) {
			return version, nil
		}
	}
}

func (overrides *trackOverrides) retired() bool {
	for id, override := range overrides.values {
		if override.active && override.version != overrides.state.retiredVersions[id].Load() {
			return false
		}
	}
	return true
}

// Admission and restoration use the same checks. Errors are static values so
// rejecting a restoration cannot allocate on the render thread.
func validatePreviewValue(spec kernel.ParamSpec, validator *engine.PreviewParamValidator, value float32) error {
	if validator == nil {
		var global engine.PreviewParamValidator
		validator = &global
	}
	return validator.Validate(spec.ID, value)
}

// CancelPreview requests retirement of exactly one override version. Read
// restores the playing score's committed value and publishes the retirement.
// A superseded or already retired version is harmless.
func (p *Player) CancelPreview(version uint64) error {
	select {
	case <-p.closed:
		return nil // Nothing plays after permanent shutdown.
	default:
	}
	request, _, ok := p.previewClear(version)
	if !ok {
		return nil
	}
	select {
	case <-p.closed:
		return nil
	case p.overrideClears <- request:
		return nil
	default:
		return fmt.Errorf("preview cancellation queue is full")
	}
}

// CancelPreviewWait queues retirement reliably, waiting for Read to make room.
// While paused it waits until rendering resumes or Close permanently ends it.
// Call it only off the render thread. It does not wait for retirement itself
// and must not use a disconnected socket's canceled context.
func (p *Player) CancelPreviewWait(version uint64) {
	select {
	case <-p.closed:
		return
	default:
	}
	if request, _, ok := p.previewClear(version); ok {
		select {
		case p.overrideClears <- request:
		case <-p.closed:
		}
	}
}

func (p *Player) previewClear(version uint64) (overrideClear, parameterOverride, bool) {
	snapshot := p.overrides.Load()
	if version == 0 || snapshot == nil {
		return overrideClear{}, parameterOverride{}, false
	}
	for identity, overrides := range snapshot.tracks {
		for id, override := range overrides.values {
			if !override.active || override.version != version || overrides.state.retiredVersions[id].Load() == version {
				continue
			}
			return overrideClear{track: identity, id: kernel.ParamID(id), version: version}, override, true
		}
	}
	return overrideClear{}, parameterOverride{}, false
}

func (p *Player) SetMute(track uint8, on bool) error {
	value := float32(0)
	if on {
		value = 1
	}
	return p.SetParam(track, kernel.ParamMixMute, value)
}

func (p *Player) SetSolo(track uint8, on bool) error {
	value := float32(0)
	if on {
		value = 1
	}
	return p.SetParam(track, kernel.ParamMixSolo, value)
}

func (p *Player) applyOverrides(target *engine.Engine, score Score, newEngine bool) {
	if newEngine {
		p.assignTrackIncarnations(score.trackNames)
	}
	// Count first so the snapshot includes the override publication for every
	// request we drain. Requests arriving later wait for the next boundary,
	// rather than being rejected against an older snapshot and lost.
	if p.overrideHook != nil {
		p.overrideHook(false)
	}
	remaining := len(p.overrideClears)
	hadClears := remaining != 0
	snapshot := p.overrides.Load()
	if snapshot == nil {
		return
	}
	if p.overrideHook != nil {
		p.overrideHook(true)
	}
	// Drain only the requests present at this boundary. Retain a matching
	// cancellation until its restoration command fits in the engine queue.
	for ; remaining > 0; remaining-- {
		request := <-p.overrideClears
		overrides := snapshot.tracks[request.track]
		if overrides != nil {
			override := overrides.values[request.id]
			if override.active && override.version == request.version {
				overrides.state.cancelledVersions[request.id] = request.version
			}
		}
	}
	// Snapshot caching never bypasses the cancellation queue or its admission
	// boundary hooks. Failed restoration/pushes invalidate it for retry.
	if !newEngine && !hadClears && p.appliedOverrideSnapshot == snapshot && p.appliedOverrideEngine == target {
		return
	}
	complete := true
	for identity, overrides := range snapshot.tracks {
		state := overrides.state
		if newEngine {
			clear(state.appliedVersions[:])
		}
		for id, override := range overrides.values {
			if !override.active || override.version == state.clearedVersions[id] {
				continue
			}
			cancelled := state.cancelledVersions[id] == override.version
			if cancelled && newEngine {
				state.retire(kernel.ParamID(id), override.version)
				continue // The new engine already has its committed value.
			}
			track := uint8(0xff)
			if identity.id != "" {
				var present bool
				track, present = score.trackNames.trackIndex(identity.id)
				if !present || score.trackNames.incarnations[track] != identity.incarnation {
					state.retire(kernel.ParamID(id), override.version)
					continue // A captured target cannot cross a remove/re-add.
				}
			}
			// A later score may replace the voice or drum recipe that admitted
			// this preview. Retire it before publishing an invalid command.
			if track != 0xff && (score.trackNames == nil || !score.trackNames.previewParams[track][id] || score.trackNames.previewValues[track].Validate(kernel.ParamID(id), override.value) != nil) {
				state.retire(kernel.ParamID(id), override.version)
				continue
			}
			if cancelled {
				value, ok := score.trackNames.committedValue(track, kernel.ParamID(id))
				var validator *engine.PreviewParamValidator
				if track != 0xff {
					validator = &score.trackNames.previewValues[track]
				}
				if !ok || validatePreviewValue(kernel.Params[id], validator, value) != nil {
					state.retire(kernel.ParamID(id), override.version)
					p.emit(Event{Kind: "preview-error", Name: kernel.Params[id].Name, Track: identity.id})
					continue
				}
				if target.Push(cmd.Command{Op: cmd.OpSetParam, Track: track, Index: uint16(id), Arg0: math.Float32bits(value)}) {
					state.retire(kernel.ParamID(id), override.version)
				} else {
					complete = false
				}
				continue
			}
			if value, ok := score.trackNames.committedValue(track, kernel.ParamID(id)); newEngine && ok && math.Float32bits(value) == math.Float32bits(override.value) {
				state.retire(kernel.ParamID(id), override.version)
				continue
			}
			if !newEngine && state.appliedVersions[id] == override.version {
				continue
			}
			command := cmd.Command{Op: cmd.OpSetParam, Track: track, Index: uint16(id), Arg0: math.Float32bits(override.value)}
			if target.Push(command) {
				state.appliedVersions[id] = override.version
			} else {
				complete = false
			}
		}
	}
	if complete {
		p.appliedOverrideSnapshot, p.appliedOverrideEngine = snapshot, target
	} else {
		p.appliedOverrideSnapshot = nil
	}
}

func parameterMatches(parameters []ParameterValue, track uint8, id kernel.ParamID, value float32) bool {
	committed, ok := parameterValue(parameters, track, id)
	return ok && math.Float32bits(committed) == math.Float32bits(value)
}

func parameterValue(parameters []ParameterValue, track uint8, id kernel.ParamID) (float32, bool) {
	for _, parameter := range parameters {
		if parameter.Track == track && parameter.ID == id {
			return parameter.Value, true
		}
	}
	return 0, false
}

func (p *Player) resetMeters() {
	p.meterScratch = MeterFrame{}
	p.meterTick = 0
	p.meterStarted = false
}

func (p *Player) collectMeter(message cmd.Message) {
	if message.Kind != cmd.Meter {
		return
	}
	if !p.meterStarted || p.meterTick != message.Tick {
		p.meterScratch = MeterFrame{Tick: message.Tick, HasReturnA: p.current.HasReturnA, HasReturnB: p.current.HasReturnB, HasSFX: p.current.HasSFX}
		p.meterTick, p.meterStarted = message.Tick, true
		p.meterScratch.TrackCount = uint8(min(len(p.current.Tracks), engine.MaxTracks))
		for i := 0; i < int(p.meterScratch.TrackCount); i++ {
			p.meterScratch.TrackIDs[i] = p.current.Tracks[i].ID
		}
	}
	value := math.Float32frombits(message.B)
	switch {
	case message.Track < engine.MaxTracks && int(message.Track) < int(p.meterScratch.TrackCount):
		track := &p.meterScratch.Tracks[message.Track]
		assignMeter(track, message.A, value)
		p.meterScratch.stereo = p.meterScratch.stereo || message.A >= 2 && message.A <= 5
		switch message.A {
		case 2:
			track.PeakL = value
		case 3:
			track.PeakR = value
		case 4:
			track.RMSL = value
		case 5:
			track.RMSR = value
		}
	case message.Track == 0xf0:
		assignMeter(&p.meterScratch.ReturnA, message.A, value)
	case message.Track == 0xf1:
		assignMeter(&p.meterScratch.ReturnB, message.A, value)
	case message.Track == 0xf2:
		assignMeter(&p.meterScratch.Music, message.A, value)
	case message.Track == 0xf3:
		assignMeter(&p.meterScratch.SFX, message.A, value)
	case message.Track == 0xfd:
		assignMeter(&p.meterScratch.MasterPre, message.A, value)
	case message.Track == 0xff && message.A == 0:
		p.meterScratch.MasterPeak = value
	case message.Track == 0xff && message.A == 1:
		p.meterScratch.MasterRMS = value
		p.publishMeter(p.meterScratch)
		p.meterStarted = false
	case message.Track == 0xfc && message.A == 2:
		p.meterScratch.CompGR = value
	case message.Track == 0xfb && message.A == 2:
		p.meterScratch.LimiterGR = value
	}
}

func assignMeter(meter *MeterValue, quantity uint16, value float32) {
	if quantity == 0 {
		meter.Peak = value
	} else if quantity == 1 {
		meter.RMS = value
	}
}

func (p *Player) publishMeter(frame MeterFrame) {
	if !frame.stereo {
		// The engine drops per-channel quantities when its message ring is too full for
		// the whole emission. Report both channels at the mono level.
		for i := 0; i < int(frame.TrackCount); i++ {
			track := &frame.Tracks[i]
			track.PeakL, track.PeakR, track.RMSL, track.RMSR = track.Peak, track.Peak, track.RMS, track.RMS
		}
	}
	p.telemetry.storeMeters(&frame)
	select {
	case p.meters <- frame:
	default:
		select {
		case <-p.meters:
		default:
		}
		select {
		case p.meters <- frame:
		default:
		}
	}
}

// LaunchScene keeps the newest requested scene. The audio reader resolves its
// name against the score active at the landing bar, so edits cannot stale an index.
func (p *Player) LaunchScene(name string) error {
	return p.LaunchSceneQuantized(name, cmd.Quantize(2))
}

// LaunchSceneQuantized schedules the newest scene request at the next
// requested musical boundary. The audio reader resolves the name against the
// score active at that boundary.
func (p *Player) LaunchSceneQuantized(name string, quantize cmd.Quantize) error {
	if name == "" {
		return fmt.Errorf("scene name is required")
	}
	target, err := p.nextQuantizedTick(quantize)
	if err != nil {
		return err
	}
	p.launches.Store(&sceneLaunch{id: p.launchSequence.Add(1), name: name, targetTick: target, quantize: quantize})
	return nil
}

// CancelScene discards a request that has not reached the audio reader.
func (p *Player) CancelScene() {
	p.launches.Store(nil)
}

// StartSongEntry requests an immediate transport jump to a named song block.
// The audio reader resolves it against the score it will render next.
func (p *Player) StartSongEntry(index int, scene string) error {
	_, err := p.QueueSongEntry(index, scene)
	return err
}

// QueueSongEntry returns an ID which lets the control path distinguish a
// queued request from an older one for the same song block.
func (p *Player) QueueSongEntry(index int, scene string) (uint64, error) {
	if index < 0 || scene == "" {
		return 0, fmt.Errorf("song entry index and scene are required")
	}
	p.ClearRange()
	p.startMu.Lock()
	defer p.startMu.Unlock()
	request := StartRequest{ID: p.startSequence.Add(1), Index: index, Scene: scene}
	p.pendingStart.Store(request.ID)
	for {
		select {
		case p.starts <- request:
			return request.ID, nil
		default:
			select {
			case <-p.starts:
			default:
			}
		}
	}
}

// PendingSongEntryID returns the queued song request ID, or zero.
func (p *Player) PendingSongEntryID() uint64 { return p.pendingStart.Load() }

func (p *Player) CancelStart() {
	p.startMu.Lock()
	defer p.startMu.Unlock()
	select {
	case request := <-p.starts:
		p.pendingStart.CompareAndSwap(request.ID, 0)
	default:
	}
}

// SelectPattern queues the latest launch per track in an immutable snapshot.
// The audio reader takes that snapshot at a bar boundary without locking.
func (p *Player) SelectPattern(track, pattern string) error {
	return p.SelectPatternQuantized(track, pattern, cmd.Quantize(2))
}

// SelectPatternQuantized queues the latest slot request per track and lands
// it at the next requested transport boundary.
func (p *Player) SelectPatternQuantized(track, pattern string, quantize cmd.Quantize) error {
	if track == "" || pattern == "" {
		return fmt.Errorf("track and pattern are required")
	}
	target, err := p.nextQuantizedTick(quantize)
	if err != nil {
		return err
	}
	for {
		old := p.slotLaunches.Load()
		next := &slotLaunchBatch{}
		if old != nil {
			*next = *old
		}
		index := -1
		for i, queued := range next.requests {
			if queued.request.Track == track {
				index = i
				break
			}
			if queued.request.Track == "" && index < 0 {
				index = i
			}
		}
		if index < 0 {
			return fmt.Errorf("too many tracks have queued patterns")
		}
		next.requests[index] = slotLaunch{
			request: SlotRequest{Track: track, Pattern: pattern}, id: p.launchSequence.Add(1),
			TargetTick: target, quantize: quantize,
		}
		if p.slotLaunches.CompareAndSwap(old, next) {
			return nil
		}
	}
}

func (p *Player) CancelPatterns() {
	p.slotLaunches.Swap(nil)
}

// PendingScene can be read by a UI thread while Read renders audio.
func (p *Player) PendingScene() string {
	if request := p.launches.Load(); request != nil {
		return request.name
	}
	return ""
}

func (p *Player) PendingSceneQuantize() uint32 {
	if request := p.launches.Load(); request != nil {
		return uint32(request.quantize)
	}
	return 0
}

// PendingPatterns returns a copy of queued pattern launches. An empty Track
// marks an unused entry. Read can consume the queue while the UI takes its copy.
func (p *Player) PendingPatterns() [engine.MaxTracks]SlotRequest {
	if batch := p.slotLaunches.Load(); batch != nil {
		var requests [engine.MaxTracks]SlotRequest
		for index, queued := range batch.requests {
			requests[index] = queued.request
		}
		return requests
	}
	return [engine.MaxTracks]SlotRequest{}
}

func (p *Player) PendingPatternQuantizes() map[string]uint32 {
	batch := p.slotLaunches.Load()
	if batch == nil {
		return nil
	}
	quantizes := make(map[string]uint32)
	for _, queued := range batch.requests {
		if queued.request.Track != "" {
			quantizes[queued.request.Track] = uint32(queued.quantize)
		}
	}
	return quantizes
}

func (p *Player) nextQuantizedTick(quantize cmd.Quantize) (int64, error) {
	quantum := int64(0)
	switch quantize {
	case 1:
		quantum = seq.PPQ
	case 2, 5:
		quantum = seq.TicksPerBar
	case 6:
		quantum = 2 * seq.TicksPerBar
	case 8:
		quantum = 4 * seq.TicksPerBar
	default:
		return 0, fmt.Errorf("quantize must be next beat, next bar, 2 bars, or 4 bars")
	}
	position := p.Position()
	if position.Bar < 1 || position.Step < 1 {
		return 0, fmt.Errorf("transport position is unavailable")
	}
	tick := (position.Bar-1)*seq.TicksPerBar + (position.Step-1)*seq.TicksPerStep
	return nextQuantizedAfter(tick, quantize, quantum)
}

func nextQuantizedAfter(tick int64, quantize cmd.Quantize, quantum int64) (int64, error) {
	target, err := seq.QuantizeTick(tick, quantize, 16)
	if err != nil {
		return 0, err
	}
	if target <= tick {
		target += quantum
	}
	return target, nil
}

// CurrentScene returns the scene observed by the audio reader without blocking it.
func (p *Player) CurrentScene() string {
	if scene := p.scene.Load(); scene != nil {
		return *scene
	}
	return ""
}

// BPMMilli observes the active engine tempo, excluding score offers that have
// not landed yet. Hosts use it to prepare count-in with the engine's clock.
func (p *Player) BPMMilli() int64 { return p.tempoMilli.Load() }

// Position can be read safely by a UI thread while Read renders audio.
func (p *Player) Position() Position {
	packed := p.position.Load()
	return Position{Bar: int64(packed >> 8), Step: int64(packed & 0xff)}
}

func (p *Player) Read(out []byte) (int, error) {
	if len(out) == 0 {
		return 0, nil
	}
	written := 0
	for written < len(out) {
		if p.retirementFault.Load() {
			if written > 0 {
				return written, nil
			}
			return 0, p.fault
		}
		p.beginRequestedRange()
		p.beginRequestedSong()
		if p.read == p.buffered {
			if p.fault != nil {
				if written > 0 {
					return written, nil
				}
				return 0, p.fault
			}
			// Match the device's demand: rendering a full 256-frame block for a
			// 64-frame callback delays live input by the retained 192 frames.
			p.renderBlock((len(out) - written + 7) / 8)
			if p.fault != nil {
				continue
			}
		}
		available := p.buffered - p.read
		// If the driver asks for a partial frame, finish that frame before
		// accepting a jump. A new block must begin on a stereo-frame boundary.
		if remainder := p.read % 8; remainder != 0 && available > 8-remainder {
			available = 8 - remainder
		}
		n := copy(out[written:], p.pcm[p.read:p.read+available])
		written += n
		p.read += n
	}
	return written, nil
}

func (p *Player) beginRequestedSong() {
	if p.read%8 != 0 {
		return
	}
	select {
	case request := <-p.starts:
		p.pendingStart.CompareAndSwap(request.ID, 0)
		var next Score
		hasOffer := false
		select {
		case next = <-p.offers:
			hasOffer = true
		default:
		}
		selected := p.current
		if hasOffer {
			selected = next
		}
		if request.Index >= len(selected.Song) || selected.Song[request.Index].Scene != request.Scene || selected.Song[request.Index].StartBar == 0 {
			if hasOffer {
				select {
				case p.offers <- next:
				default:
					p.retireEngine(next.Engine)
				}
			}
			p.emit(Event{Kind: "song-error", Name: request.Scene})
			return
		}
		start := selected.Song[request.Index].StartBar
		p.read, p.buffered = 0, 0
		// A song-position request supersedes manual launches queued for the
		// old transport position.
		p.launches.Store(nil)
		p.slotLaunches.Swap(nil)
		if p.previous != nil {
			p.retireEngine(p.previous)
		}
		p.previous, p.fadeRemaining = nil, 0
		if hasOffer {
			if p.previous != nil {
				p.retireEngine(p.previous)
			}
			p.previous = p.current.Engine
			p.discardSupersededPatches(next.publication)
			p.rebuildCommitted(next, int64(start-1)*seq.TicksPerBar)
			p.applyOverrides(next.Engine, next, true)
			p.heldNotes = [128]noteInput{}
			p.heldOrder = [128]uint64{}
			p.liveGroups = [256]liveControlGroup{}
			p.liveGroupLimit = 0
			p.current = next
			p.streamEngine.Store(next.Engine)
			p.trackCount.Store(uint32(next.Engine.TrackCount()))
			p.trackNames.Store(next.trackNames)
			p.resetMeters()
			p.fadeTotal, p.fadeRemaining = p.rate/200, p.rate/200
			p.emit(Event{Bar: int64(start), Name: next.Name, Kind: "edit", Revision: next.Revision})
			p.jumpFadeRemaining = 0
		} else {
			p.jumpFadeRemaining = p.rate / 200
		}
		commands := [2]cmd.Command{{Op: cmd.OpSeek, Track: 0xff, Arg0: start - 1}, {Op: cmd.OpPlay, Track: 0xff}}
		if !p.current.Engine.PushBatch(commands[:]) {
			p.fault = fmt.Errorf("live engine rejected song start at bar %d", start)
			return
		}
		p.rebuildCommitted(p.current, int64(start-1)*seq.TicksPerBar)
		p.transportQueued = true
		p.initialParameters = false
		p.bar = int64(start) - 1
		p.clock = seq.Clock{SampleRate: int64(p.rate), BPMMilli: selected.Engine.TempoAtTick(p.bar * seq.TicksPerBar), AnchorSample: p.sample, AnchorTick: p.bar * seq.TicksPerBar}
		p.tempoMilli.Store(p.clock.BPMMilli)
		p.nextBarSample = p.clock.SampleAtTick((p.bar + 1) * seq.TicksPerBar)
		p.position.Store(uint64(start)<<8 | 1)
		p.emit(Event{Bar: int64(start), Name: request.Scene, Kind: "song"})
	default:
	}
}

// followLiveTempo keeps the bar clock on the tempo the engine is actually
// playing. A manual scene launch can change it off the authored chain, and the
// next bar boundary, where an offered edit lands, moves with it. The engine's
// anchor is in its own frames, so the host sample is found from the current
// position of both.
func (p *Player) followLiveTempo() {
	live := p.current.Engine.LiveClock()
	if live.BPMMilli == p.clock.BPMMilli && live.AnchorTick == p.clock.AnchorTick {
		return
	}
	p.clock = seq.Clock{
		SampleRate: int64(p.rate), BPMMilli: live.BPMMilli,
		AnchorSample: live.AnchorSample + p.sample - p.current.Engine.TransportSample(), AnchorTick: live.AnchorTick,
	}
	p.tempoMilli.Store(live.BPMMilli)
	p.nextBarSample = p.clock.SampleAtTick((p.bar + 1) * seq.TicksPerBar)
}

// cpuLoadAlpha weights the newest block in the CPU load average.
const cpuLoadAlpha = 0.1

// noteRenderTime folds one block into the load average: wall time spent in
// Engine.Render over the time that many frames take to play. It allocates
// nothing; time.Now and time.Since use the monotonic clock.
func (p *Player) noteRenderTime(elapsed time.Duration, frames int) {
	load := elapsed.Seconds() / (float64(frames) / float64(p.rate))
	p.cpuLoad += cpuLoadAlpha * (load - p.cpuLoad)
	p.cpuLoadBits.Store(math.Float64bits(p.cpuLoad))
}

// CPULoad returns the exponential average of render time over block time:
// 0.5 means the engine used half of each block's real-time budget.
func (p *Player) CPULoad() float64 { return math.Float64frombits(p.cpuLoadBits.Load()) }

func (p *Player) renderBlock(requestedFrames int) {
	if p.finishPlaybackRange() {
		return
	}
	clear(p.blockParameters[:])
	// A tempo increase found after the fact can leave a bar line behind the
	// playhead; every such line is handled before rendering.
	for p.sample >= p.nextBarSample {
		p.bar++
		swapped := false
		select {
		case next := <-p.offers:
			if p.bar > int64(^uint32(0)) || !next.Engine.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: uint32(p.bar)}) || !next.Engine.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
				p.retireEngine(next.Engine)
				p.fault = fmt.Errorf("live engine rejected bar %d", p.bar)
				return
			}
			if p.previous != nil {
				p.retireEngine(p.previous)
			}
			p.previous = p.current.Engine
			p.discardSupersededPatches(next.publication)
			p.rebuildCommitted(next, p.bar*seq.TicksPerBar)
			p.transportQueued = true
			p.applyOverrides(next.Engine, next, true)
			p.fadeTotal = p.rate / 200 // five milliseconds
			p.fadeRemaining = p.fadeTotal
			p.heldNotes = [128]noteInput{}
			p.heldOrder = [128]uint64{}
			p.liveGroups = [256]liveControlGroup{}
			p.liveGroupLimit = 0
			p.current = next
			p.streamEngine.Store(next.Engine)
			if next.Range != nil {
				p.setPlaybackRange(next.Range)
			}
			p.trackCount.Store(uint32(next.Engine.TrackCount()))
			p.trackNames.Store(next.trackNames)
			p.resetMeters()
			swapped = true
			p.clock = seq.Clock{
				SampleRate: int64(p.rate), BPMMilli: next.Engine.TempoAtTick(p.bar * seq.TicksPerBar),
				AnchorSample: p.sample, AnchorTick: p.bar * seq.TicksPerBar,
			}
			p.tempoMilli.Store(p.clock.BPMMilli)
			if r := next.Range; r != nil && (p.clock.TickAtSample(p.sample) < r.StartTick || p.clock.TickAtSample(p.sample) >= r.EndTick) {
				p.seekListeningRange(r.StartTick)
			}
			select {
			case p.events <- Event{Bar: p.bar + 1, Name: next.Name, Kind: "edit", Revision: next.Revision}:
			default:
			}
		default:
		}
		if swapped {
			p.requeuePendingLaunches()
		}
		// A scene that sets a tempo steps it on this bar. Re-anchor the way the
		// kernel transport does, at the sample where the bar begins.
		if tempo := p.current.Engine.TempoAtTick(p.bar * seq.TicksPerBar); tempo != p.clock.BPMMilli {
			if clock, err := p.clock.Reanchor(p.bar*seq.TicksPerBar, tempo); err == nil {
				p.clock = clock
				p.tempoMilli.Store(tempo)
			}
		}
		p.nextBarSample = p.clock.SampleAtTick((p.bar + 1) * seq.TicksPerBar)
	}
	frames := int(min(int64(min(blockFrames, requestedFrames)), p.nextBarSample-p.sample))
	if r := p.playbackRange; r != nil {
		frames = int(min(int64(frames), p.clock.SampleAtTick(r.EndTick)-p.sample))
	}
	if frames <= 0 {
		p.fault = fmt.Errorf("live transport did not advance at bar %d", p.bar+1)
		return
	}
	tick := p.clock.TickAtSample(p.sample)
	p.position.Store(uint64((tick/seq.TicksPerBar+1)<<8 | (tick%seq.TicksPerBar)/seq.TicksPerStep + 1))
	if p.initialParameters {
		p.rebuildCommitted(p.current, tick)
		p.initialParameters = false
	}
	p.drainPatches()
	p.queueLiveNotes()
	if p.fault != nil {
		return
	}
	p.queueSceneLaunch(frames)
	p.queueSlotLaunches(frames)
	p.notePendingScenes(tick)
	p.applyOverrides(p.current.Engine, p.current, false)
	renderStart := time.Now()
	p.current.Engine.Render(p.left[:frames], p.right[:frames])
	p.noteRenderTime(time.Since(renderStart), frames)
	if index, sequence := p.current.Engine.CurrentScene(); index >= 0 && index < len(p.current.SceneIDs) && (p.sceneEngine != p.current.Engine || p.sceneSequence != sequence) {
		p.noteSceneCommitted(index, tick)
		firstScene := p.sceneEngine == nil
		p.sceneEngine, p.sceneSequence = p.current.Engine, sequence
		p.scene.Store(&p.current.SceneIDs[index])
		request := p.launches.Load()
		manual := request != nil && request.submitted.Load() && p.clock.SampleAtTick(request.targetTick) < p.sample+int64(frames)
		if !firstScene && !manual {
			p.emit(Event{Bar: tick/seq.TicksPerBar + 1, Name: p.current.SceneIDs[index], Kind: "song-scene"})
		}
	}
	p.transportQueued = false
	var message cmd.Message
	for p.current.Engine.Poll(&message) {
		if message.Kind == cmd.Fault {
			if message.A == cmd.FaultPolyLive {
				p.fault = fmt.Errorf("%s", cmd.PolyLiveUnsupported)
			} else {
				p.fault = fmt.Errorf("live engine fault %d", message.A)
			}
			return
		}
		p.collectMeter(message)
	}
	if p.fadeRemaining > 0 {
		fadeFrames := min(frames, p.fadeRemaining)
		p.previous.Render(p.oldL[:fadeFrames], p.oldR[:fadeFrames])
		for i := 0; i < fadeFrames; i++ {
			newGain := float32(p.fadeTotal-p.fadeRemaining+i+1) / float32(p.fadeTotal)
			oldGain := 1 - newGain
			p.left[i] = p.oldL[i]*oldGain + p.left[i]*newGain
			p.right[i] = p.oldR[i]*oldGain + p.right[i]*newGain
		}
		p.fadeRemaining -= fadeFrames
		if p.fadeRemaining == 0 {
			p.retireEngine(p.previous)
			p.previous = nil
		}
	}
	if p.jumpFadeRemaining > 0 {
		fadeFrames := min(frames, p.jumpFadeRemaining)
		for i := 0; i < fadeFrames; i++ {
			gain := float32(p.rate/200-p.jumpFadeRemaining+i+1) / float32(p.rate/200)
			p.left[i] *= gain
			p.right[i] *= gain
		}
		p.jumpFadeRemaining -= fadeFrames
	}
	if r := p.playbackRange; r != nil {
		endSample := p.clock.SampleAtTick(r.EndTick)
		fade := int64(max(1, p.rate/200))
		for i := 0; i < frames; i++ {
			remaining := endSample - p.sample - int64(i) - 1
			if remaining < fade {
				gain := float32(max(int64(0), remaining)) / float32(fade)
				p.left[i] *= gain
				p.right[i] *= gain
			}
		}
	}
	// The engine output is post-limiter. Copy it only after transport fades have
	// been applied, and never wait for the independent meter worker.
	p.loudness.push(p.left[:frames], p.right[:frames])
	for i := 0; i < frames; i++ {
		binary.LittleEndian.PutUint32(p.pcm[i*8:], math.Float32bits(p.left[i]))
		binary.LittleEndian.PutUint32(p.pcm[i*8+4:], math.Float32bits(p.right[i]))
	}
	endSample := p.sample + int64(frames)
	p.completeSceneLaunch(endSample)
	p.completeSlotLaunches(endSample)
	p.sample += int64(frames)
	p.followLiveTempo()
	p.telemetry.storeTransport(p.sample, p.clock.TickAtSample(p.sample), p.clock.BPMMilli, p.current.Engine.Playing(), p.nextTempoStep())
	p.buffered, p.read = frames*8, 0
}

func (p *Player) queueLiveNotes() {
	p.ownedFinalCount = 0
	p.pruneLiveGroups()
	epoch := p.noteEpoch.Load()
	batch := p.notes.Swap(nil)
	releaseAll := epoch&1 != 0
	if batch != nil && batch.epoch != epoch {
		batch = nil // An in-flight producer from before the barrier cannot replay.
	}
	if batch == nil && !releaseAll {
		return
	}
	snapshot := p.trackNames.Load()
	// The kernel intentionally orders same-tick NoteOff before NoteOn. Reduce
	// this reader's burst to the final gate command per route so a release/panic
	// cannot be reordered ahead of its queued press and reopen the voice.
	final, touched := &p.noteFinal, &p.noteTouched
	for track := range touched {
		clear(touched[track][:])
	}
	if releaseAll {
		p.noteEpoch.CompareAndSwap(epoch, epoch+1)
		for i, held := range p.heldNotes {
			if p.heldOrder[i] == 0 {
				continue
			}
			track, kind, found := findTrack(snapshot, held.Track)
			if !found {
				continue
			}
			lane, command := uint16(0), cmd.Command{Op: cmd.OpNoteOff, Track: track, Index: 0xffff}
			if held.KernelID != 0 {
				command.Index = held.KernelID
				if snapshot.poly[track] || kind == "poly" {
					command.Arg1 = cmd.LiveIdentity
				}
				p.addOwnedFinal(command)
				continue
			}
			if kind == "drums" {
				lane, _ = GMDrumLane(int(held.Note))
				command.Index = lane
			} else if kind == "piano" || kind == "keys" || kind == "poly" {
				lane, command.Index = uint16(held.Note), uint16(held.Note)
			}
			final[track][lane], touched[track][lane] = command, true
		}
		p.heldNotes, p.heldOrder = [128]noteInput{}, [128]uint64{}
		for i := range p.liveGroups[:p.liveGroupLimit] {
			p.liveGroups[i].Owner = "" // Freeze tails until their slots are safe to reuse.
		}
		batch = nil
		p.emit(Event{Name: "live note release overload cleared held inputs and queued presses", Kind: "note-error"})
	}
	for index := 0; batch != nil && index < int(batch.count); index++ {
		input := batch.inputs[index]
		track, kind, found := findTrack(snapshot, input.Track)
		if !found {
			p.emit(Event{Track: input.Track, Name: "track is no longer in the playing score", Kind: "note-error"})
			continue
		}
		poly := snapshot.poly[track] || kind == "poly"
		if input.Group != "" {
			p.queueLiveGroup(track, input)
			continue
		}
		if input.Expression {
			if kind == "keys" {
				p.emit(Event{Track: input.Track, Name: "CICADA-UNSUPPORTED: modeled keyboards do not support per-note expression", Kind: "note-error"})
				continue
			}
			if kind == "acid" || kind == "graph" || kind == "poly" {
				for i := range p.heldNotes {
					held := &p.heldNotes[i]
					if p.heldOrder[i] != 0 && held.ID == input.ID && held.Track == input.Track && held.NoteID == input.NoteID {
						held.PitchCents, held.Pressure, held.Timbre, held.HasControls = input.PitchCents, input.Pressure, input.Timbre, true
						p.addOwnedFinal(liveExpressionCommand(track, held))
						break
					}
				}
			}
			continue
		}
		slot, free, latest := -1, -1, -1
		sameRoute := func(a noteInput) bool {
			if a.Track != input.Track {
				return false
			}
			if poly {
				return a.ID == input.ID
			}
			if kind == "piano" || kind == "keys" || kind == "poly" {
				return a.Note == input.Note
			}
			if kind != "drums" {
				return true
			}
			x, _ := GMDrumLane(int(a.Note))
			y, _ := GMDrumLane(int(input.Note))
			return x == y
		}
		for i := range p.heldNotes {
			if p.heldOrder[i] == 0 {
				if free < 0 {
					free = i
				}
				continue
			}
			held := p.heldNotes[i]
			if held.ID == input.ID {
				slot = i
			}
			if sameRoute(held) && (latest < 0 || p.heldOrder[i] > p.heldOrder[latest]) {
				latest = i
			}
		}
		if input.On {
			if slot >= 0 {
				continue
			} // Duplicate press never changes its owner.
			if free < 0 {
				p.emit(Event{Track: input.Track, Name: "held note capacity exceeded", Kind: "note-error"})
				continue
			}
			p.noteOrder++
			if kind == "acid" || kind == "graph" || kind == "poly" {
				input.KernelID = p.allocateLiveIdentity()
			}
			p.heldNotes[free], p.heldOrder[free] = input, p.noteOrder
		} else {
			if slot < 0 || p.heldNotes[slot].Track != input.Track || p.heldNotes[slot].Note != input.Note {
				continue
			}
			input.KernelID = p.heldNotes[slot].KernelID
			p.heldNotes[slot], p.heldOrder[slot] = noteInput{}, 0
			if slot != latest {
				continue
			} // A late release cannot close the current voice.
			prior := -1
			for i, held := range p.heldNotes {
				if p.heldOrder[i] != 0 && sameRoute(held) && (prior < 0 || p.heldOrder[i] > p.heldOrder[prior]) {
					prior = i
				}
			}
			if prior >= 0 {
				input = p.heldNotes[prior]
			}
		}
		command := cmd.Command{Track: track, Index: input.KernelID}
		if kind == "acid" || kind == "piano" || kind == "keys" || kind == "graph" || kind == "poly" {
			if (kind == "piano" || kind == "keys") && (input.Note < 21 || input.Note > 108) {
				p.emit(Event{Track: input.Track, Name: fmt.Sprintf("%s note must be in MIDI range 21–108", kind), Kind: "note-error"})
				continue
			}
			if input.On {
				command.Op = cmd.OpNoteOn
				command.Arg0 = uint32(input.Note) | uint32(input.Velocity)<<8
			} else {
				command.Op, command.Index = cmd.OpNoteOff, 0xffff
				if kind == "piano" || kind == "keys" || kind == "poly" {
					command.Index = uint16(input.Note)
				} else if input.KernelID != 0 {
					command.Index = input.KernelID
				} else if kind == "piano" || kind == "graph" || kind == "poly" {
					command.Index = uint16(input.Note)
					if kind == "graph" {
						command.Index |= engine.MonoNoteOffPitchFlag
					}
				}
			}
		} else if kind == "drums" {
			lane, ok := GMDrumLane(int(input.Note))
			if !ok {
				p.emit(Event{Track: input.Track, Name: "MIDI note is not in the General MIDI drum map", Kind: "note-error"})
				continue
			}
			command.Index = lane
			if input.On {
				command.Op = cmd.OpNoteOn
				command.Arg0 = uint32(input.Note) | uint32(input.Velocity)<<8
			} else {
				command.Op = cmd.OpNoteOff
			}
		} else {
			p.emit(Event{Track: input.Track, Name: fmt.Sprintf("track %q does not accept live notes", input.Track), Kind: "note-error"})
			continue
		}
		if poly {
			command.Index, command.Arg1 = input.KernelID, cmd.LiveIdentity
		}
		lane := uint16(0)
		if kind == "drums" || kind == "piano" || kind == "keys" || kind == "poly" {
			lane = command.Index
			if input.On && kind != "drums" {
				lane = uint16(input.Note)
			}
		}
		if poly {
			p.addOwnedFinal(command)
		} else {
			final[track][lane], touched[track][lane] = command, true
		}
		if input.On && input.HasControls {
			p.addOwnedFinal(liveExpressionCommand(track, &input))
		}
		eventKind := "note-on"
		if !input.On {
			eventKind = "note-off"
		}
		p.emit(Event{Bar: p.Position().Bar, Name: liveNoteNames[input.Note], Track: input.Track, Kind: eventKind})
	}
	for track := range final {
		for lane := range final[track] {
			if touched[track][lane] && !p.current.Engine.Push(final[track][lane]) {
				p.emit(Event{Track: snapshot.ids[track], Name: "live note command queue is full", Kind: "note-error"})
				p.fault = io.ErrShortBuffer
			}
		}
	}
	p.flushOwnedFinal()
}

func findTrack(snapshot *trackNameSnapshot, id string) (uint8, string, bool) {
	if snapshot == nil {
		return 0, "", false
	}
	for index := 0; index < int(snapshot.count); index++ {
		if snapshot.ids[index] == id {
			return uint8(index), snapshot.kinds[index], true
		}
	}
	return 0, "", false
}

func (p *Player) queueSceneLaunch(frames int) {
	request := p.launches.Load()
	if request == nil || request.submitted.Load() {
		return
	}
	if len(p.offers) != 0 && p.sample < p.nextBarSample && request.targetTick >= p.clock.TickAtSample(p.nextBarSample) {
		return
	}
	endSample := min(p.sample+int64(frames), p.nextBarSample)
	if request.targetTick > p.clock.TickAtSample(endSample) {
		return
	}
	index := -1
	for sceneIndex, name := range p.current.SceneIDs {
		if name == request.name {
			index = sceneIndex
			break
		}
	}
	if index < 0 || index > int(^uint16(0)) {
		if p.launches.CompareAndSwap(request, nil) {
			p.emit(Event{Bar: request.targetTick/seq.TicksPerBar + 1, Name: request.name, Kind: "scene-error"})
		}
		return
	}
	// Mark the published record in place: no copy, so the audio goroutine allocates nothing and
	// readers never see a record change except through the atomic flag.
	if p.launches.Load() != request || !request.submitted.CompareAndSwap(false, true) {
		return
	}
	if !p.current.Engine.Push(cmd.Command{Op: cmd.OpLaunchScene, Track: 0xff, Index: uint16(index), Tick: request.targetTick}) {
		p.launches.CompareAndSwap(request, nil)
		p.emit(Event{Bar: request.targetTick/seq.TicksPerBar + 1, Name: request.name, Kind: "scene-error"})
	}
}

func (p *Player) requeuePendingLaunches() {
	tick := p.clock.TickAtSample(p.sample)
	for {
		old := p.launches.Load()
		if old == nil {
			break
		}
		// A requeued launch may get a new target tick, so it is a new immutable record. This runs
		// when a replacement score lands, not every block, and allocates once per pending launch.
		next := sceneLaunch{id: old.id, name: old.name, targetTick: old.targetTick, quantize: old.quantize}
		if next.targetTick <= tick {
			quantum, ok := quantizeQuantum(next.quantize)
			if !ok {
				p.launches.CompareAndSwap(old, nil)
				break
			}
			var err error
			next.targetTick, err = nextQuantizedAfter(tick, next.quantize, quantum)
			if err != nil {
				p.launches.CompareAndSwap(old, nil)
				break
			}
		}
		if p.launches.CompareAndSwap(old, &next) {
			break
		}
	}
	for {
		old := p.slotLaunches.Load()
		if old == nil {
			return
		}
		next := *old
		changed := false
		for index, request := range next.requests {
			if request.request.Track == "" {
				continue
			}
			request.submitted = false
			if request.TargetTick <= tick {
				quantum, ok := quantizeQuantum(request.quantize)
				if !ok {
					next.requests[index] = slotLaunch{}
					changed = true
					continue
				}
				target, err := nextQuantizedAfter(tick, request.quantize, quantum)
				if err != nil {
					next.requests[index] = slotLaunch{}
					changed = true
					continue
				}
				request.TargetTick = target
			}
			next.requests[index] = request
			changed = true
		}
		if !changed || p.slotLaunches.CompareAndSwap(old, &next) {
			return
		}
	}
}

func quantizeQuantum(quantize cmd.Quantize) (int64, bool) {
	switch quantize {
	case 1:
		return seq.PPQ, true
	case 2, 5:
		return seq.TicksPerBar, true
	case 6:
		return 2 * seq.TicksPerBar, true
	case 8:
		return 4 * seq.TicksPerBar, true
	default:
		return 0, false
	}
}

func (p *Player) queueSlotLaunches(frames int) {
	type scheduledSlot struct {
		request slotLaunch
		track   int
		slot    int
	}
	for {
		old := p.slotLaunches.Load()
		if old == nil {
			return
		}
		endSample := min(p.sample+int64(frames), p.nextBarSample)
		horizon := p.clock.TickAtSample(endSample)
		next := *old
		changed := false
		var failed []slotLaunch
		var scheduled []scheduledSlot
		for index, request := range old.requests {
			if request.request.Track == "" || request.submitted || request.TargetTick > horizon {
				continue
			}
			if len(p.offers) != 0 && p.sample < p.nextBarSample && request.TargetTick >= p.clock.TickAtSample(p.nextBarSample) {
				continue
			}
			track := -1
			for candidate, value := range p.current.Tracks {
				if value.ID == request.request.Track {
					track = candidate
					break
				}
			}
			slot := -1
			if track >= 0 && track < engine.MaxTracks {
				for candidate, name := range p.current.Tracks[track].Slots {
					if name == request.request.Pattern {
						slot = candidate
						break
					}
				}
			}
			if track < 0 || track >= engine.MaxTracks || slot < 0 {
				failed = append(failed, request)
				next.requests[index] = slotLaunch{}
				changed = true
				continue
			}
			next.requests[index].submitted = true
			request.submitted = true
			scheduled = append(scheduled, scheduledSlot{request: request, track: track, slot: slot})
			changed = true
		}
		if !changed {
			return
		}
		if !p.slotLaunches.CompareAndSwap(old, &next) {
			continue
		}
		for _, request := range failed {
			p.emit(Event{Bar: request.TargetTick/seq.TicksPerBar + 1, Name: request.request.Pattern, Track: request.request.Track, Kind: "slot-error"})
		}
		for _, queued := range scheduled {
			// A submitted request is marked before pushing so a concurrent reader
			// cannot duplicate it. The Engine owns the command queue on this path.
			request := queued.request
			if !p.current.Engine.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: uint8(queued.track), Index: uint16(queued.slot), Tick: request.TargetTick}) {
				p.emit(Event{Bar: request.TargetTick/seq.TicksPerBar + 1, Name: request.request.Pattern, Track: request.request.Track, Kind: "slot-error"})
				p.removeSlotRequest(request.id)
			}
		}
		return
	}
}

func (p *Player) removeSlotRequest(id uint64) {
	for {
		old := p.slotLaunches.Load()
		if old == nil {
			return
		}
		next := *old
		changed := false
		for index, request := range next.requests {
			if request.id == id {
				next.requests[index] = slotLaunch{}
				changed = true
				break
			}
		}
		if !changed || p.slotLaunches.CompareAndSwap(old, &next) {
			return
		}
	}
}

func (p *Player) completeSceneLaunch(endSample int64) {
	request := p.launches.Load()
	if request == nil || !request.submitted.Load() || p.clock.SampleAtTick(request.targetTick) >= endSample {
		return
	}
	if p.launches.CompareAndSwap(request, nil) {
		p.emit(Event{Bar: request.targetTick/seq.TicksPerBar + 1, Name: request.name, Kind: "scene"})
	}
}

func (p *Player) completeSlotLaunches(endSample int64) {
	for {
		old := p.slotLaunches.Load()
		if old == nil {
			return
		}
		next := *old
		var landed []slotLaunch
		changed := false
		for index, request := range old.requests {
			if request.request.Track != "" && request.submitted && p.clock.SampleAtTick(request.TargetTick) < endSample {
				landed = append(landed, request)
				next.requests[index] = slotLaunch{}
				changed = true
			}
		}
		if !changed {
			return
		}
		if !p.slotLaunches.CompareAndSwap(old, &next) {
			continue
		}
		for _, request := range landed {
			p.emit(Event{Bar: request.TargetTick/seq.TicksPerBar + 1, Name: request.request.Pattern, Track: request.request.Track, Kind: "slot"})
		}
		return
	}
}

func (p *Player) emit(event Event) {
	select {
	case p.events <- event:
	default:
	}
}

var _ io.Reader = (*Player)(nil)
