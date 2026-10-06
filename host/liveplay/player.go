// Package liveplay streams the native engine to an audio device and lands
// validated score replacements on exact musical bar boundaries.
package liveplay

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strconv"
	"sync"
	"sync/atomic"

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
	Engine     *engine.Engine
	SampleRate int
	BPMMilli   int64
	Name       string
	SceneIDs   []string     // engine scene indices in source order
	Tracks     []TrackSlots // engine track and slot indices in source order
	Song       []SongEntry  // song entries with one-based start bars
	Parameters []ParameterValue
	HasReturnA bool
	HasReturnB bool
	HasSFX     bool
	trackNames *trackNameSnapshot
}

type trackNameSnapshot struct {
	ids   [16]string
	kinds [16]string
	poly  [16]bool
	count uint8
}

type ParameterValue struct {
	Track uint8
	ID    kernel.ParamID
	Value float32
}

type MeterValue struct{ Peak, RMS float32 }

type MeterFrame struct {
	Tick                           int64
	TrackCount                     uint8
	TrackIDs                       [16]string
	Tracks                         [16]MeterValue
	HasReturnA, HasReturnB, HasSFX bool
	ReturnA, ReturnB               MeterValue
	Music, SFX, MasterPre          MeterValue
	MasterPeak, MasterRMS          float32
	CompGR, LimiterGR              float32
}

type parameterOverride struct {
	value   float32
	version uint64
	active  bool
	trackID string
}

type liveOverrides struct {
	values [17][kernel.ParamCount]parameterOverride
}

type SongEntry struct {
	Scene    string
	StartBar uint32
}

type TrackSlots struct {
	ID    string
	Kind  string
	Slots [16]string
}

type noteInput struct {
	ID       string
	Track    string
	Note     uint8
	Velocity uint8
	On       bool
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
	Bar   int64 // one-based bar that has just begun
	Name  string
	Kind  string // edit, scene, or scene-error
	Track string // set for one-track slot events
}

type SlotRequest struct {
	Track, Pattern string
}

type StartRequest struct {
	ID    uint64
	Index int
	Scene string
}

type slotLaunchBatch struct{ requests [16]slotLaunch }

// Position is the most recently rendered musical location. Step is one-based
// within a 16-step bar; the audio device may still be playing buffered frames.
type Position struct {
	Bar  int64 `json:"bar"`
	Step int64 `json:"step"`
}

// Player implements io.Reader for interleaved stereo float32 little-endian PCM.
// Read is owned by the audio device; Offer may be called from a file watcher.
type Player struct {
	current           Score
	previous          *engine.Engine
	clock             seq.Clock
	rate              int
	sample            int64
	bar               int64
	nextBarSample     int64
	fadeTotal         int
	fadeRemaining     int
	offers            chan Score
	launches          atomic.Pointer[sceneLaunch]
	launchSequence    atomic.Uint64
	starts            chan StartRequest
	startMu           sync.Mutex // control calls only; the audio reader never locks
	startSequence     atomic.Uint64
	pendingStart      atomic.Uint64
	slotLaunches      atomic.Pointer[slotLaunchBatch]
	notes             atomic.Pointer[noteBatch]
	noteEpoch         atomic.Uint64 // odd: release overload; batches carry their admission epoch
	heldNotes         [128]noteInput
	heldOrder         [128]uint64
	noteFinal         [16][128]cmd.Command
	noteTouched       [16][128]bool
	noteOrder         uint64
	events            chan Event
	left, right       [blockFrames]float32
	oldL, oldR        [blockFrames]float32
	pcm               [blockFrames * 8]byte
	buffered          int
	read              int
	fault             error
	jumpFadeRemaining int
	position          atomic.Uint64
	tempoMilli        atomic.Int64
	scene             atomic.Pointer[string]
	sceneSequence     uint64
	sceneEngine       *engine.Engine
	overrides         atomic.Pointer[liveOverrides]
	trackNames        atomic.Pointer[trackNameSnapshot]
	overrideSequence  atomic.Uint64
	trackCount        atomic.Uint32
	appliedVersions   [17][kernel.ParamCount]uint64
	clearedVersions   [17][kernel.ParamCount]uint64
	meters            chan MeterFrame
	meterScratch      MeterFrame
	meterTick         int64
	meterStarted      bool
	loudness          *liveLoudness
}

func New(initial Score, rate int) (*Player, error) {
	if initial.Engine == nil || initial.SampleRate != rate {
		return nil, fmt.Errorf("live score has no engine at %d Hz", rate)
	}
	clock, err := seq.NewClock(rate, initial.BPMMilli)
	if err != nil {
		return nil, err
	}
	masterLoudness, err := newLiveLoudness(rate)
	if err != nil {
		return nil, err
	}
	if !initial.Engine.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
		masterLoudness.close()
		return nil, fmt.Errorf("live engine rejected play")
	}
	initial.trackNames = makeTrackNames(initial)
	p := &Player{
		current: initial, clock: clock, rate: rate,
		nextBarSample: clock.SampleAtTick(seq.TicksPerBar),
		offers:        make(chan Score, 1), starts: make(chan StartRequest, 1), events: make(chan Event, 32),
		meters: make(chan MeterFrame, 1), loudness: masterLoudness,
	}
	p.overrides.Store(&liveOverrides{})
	p.trackCount.Store(uint32(initial.Engine.TrackCount()))
	p.trackNames.Store(initial.trackNames)
	p.position.Store(1<<8 | 1)
	p.tempoMilli.Store(initial.BPMMilli)
	if len(initial.Song) != 0 {
		p.scene.Store(&p.current.Song[0].Scene)
	} else if len(initial.SceneIDs) != 0 {
		p.scene.Store(&p.current.SceneIDs[0])
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

// Close stops the meter worker. It must be called after the audio device has
// stopped reading this player.
func (p *Player) Close() {
	if p.loudness != nil {
		p.loudness.close()
	}
}

// Offer replaces any edit that has not landed yet with the newest valid score.
func (p *Player) Offer(score Score) error {
	if score.Engine == nil || score.SampleRate != p.rate {
		return fmt.Errorf("live score has no engine at %d Hz", p.rate)
	}
	if _, err := seq.NewClock(p.rate, score.BPMMilli); err != nil {
		return err
	}
	score.trackNames = makeTrackNames(score)
	for {
		select {
		case p.offers <- score:
			return nil
		default:
			select {
			case <-p.offers:
			default:
			}
		}
	}
}

func (p *Player) Events() <-chan Event { return p.events }

func (p *Player) Meters() <-chan MeterFrame { return p.meters }

func makeTrackNames(score Score) *trackNameSnapshot {
	snapshot := &trackNameSnapshot{count: uint8(min(len(score.Tracks), 16))}
	for index := 0; index < int(snapshot.count); index++ {
		snapshot.ids[index] = score.Tracks[index].ID
		snapshot.kinds[index] = score.Tracks[index].Kind
		if snapshot.kinds[index] == "piano" && score.Engine != nil && score.Engine.TrackVoiceKind(index) != engine.VoicePiano {
			snapshot.kinds[index] = "instrument"
		}
		if score.Engine != nil {
			snapshot.poly[index] = score.Engine.TrackPolyphonic(index)
		}
	}
	return snapshot
}

// TrackIndex resolves a stable score track ID against the engine that Read owns.
func (p *Player) TrackIndex(id string) (uint8, bool) {
	snapshot := p.trackNames.Load()
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

// Note queues a live note for an acid, authored instrument, or drum track. Requests are
// resolved and applied by Read, so the control path never touches engine state.
func (p *Player) Note(track string, note, velocity int, on bool) error {
	return p.NoteID(track, note, velocity, on, fmt.Sprintf("legacy:%s:%d", track, note))
}

// NoteID binds a press to an immutable owner. Held state is a fixed array owned
// by Read; the existing command ABI and audio callback allocation budget stay intact.
func (p *Player) NoteID(track string, note, velocity int, on bool, id string) error {
	pressEpoch := p.noteEpoch.Load()
	if id == "" || len(id) > 1024 {
		return fmt.Errorf("note owner ID is required and must be bounded")
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
		if snapshot.poly[index] {
			return fmt.Errorf("%s", cmd.PolyLiveUnsupported)
		}
		if kind == "drums" {
			if _, ok := GMDrumLane(note); !ok {
				return fmt.Errorf("MIDI drum note %d is not in the General MIDI map", note)
			}
		} else if kind == "piano" {
			if note < 21 || note > 108 {
				return fmt.Errorf("piano note must be in MIDI range 21–108")
			}
		} else if kind != "acid" && kind != "graph" && kind != "poly" {
			return fmt.Errorf("track %q does not accept live notes", track)
		}
		input := noteInput{ID: id, Track: track, Note: uint8(note), Velocity: uint8(velocity), On: on}
		return p.queueOwnedNote(input, pressEpoch)
	}
	return fmt.Errorf("track %q is not in the playing score", track)
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
			if !input.On {
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
	spec, ok := kernel.Param(id)
	if !ok || !spec.Live {
		return fmt.Errorf("parameter is unknown or not live")
	}
	slot := int(track)
	trackID := ""
	if spec.Scope == "global" {
		if track != 0xff {
			return fmt.Errorf("global parameter requires track 255")
		}
		slot = 16
	} else if spec.Scope != "track" || track >= 16 || uint32(track) >= p.trackCount.Load() {
		return fmt.Errorf("track parameter index is out of range")
	} else if names := p.trackNames.Load(); names != nil && int(track) < int(names.count) {
		trackID = names.ids[track]
	}
	off := spec.Off && math.IsInf(float64(value), -1)
	if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) && !off || !off && (value < spec.Min || value > spec.Max) {
		return fmt.Errorf("parameter value is out of range")
	}
	if spec.Curve == "toggle" && value != 0 && value != 1 {
		return fmt.Errorf("toggle parameter must be zero or one")
	}
	version := p.overrideSequence.Add(1)
	for {
		old := p.overrides.Load()
		next := new(liveOverrides)
		if old != nil {
			*next = *old
		}
		if trackID != "" {
			for existing := 0; existing < 16; existing++ {
				if existing != slot && next.values[existing][id].trackID == trackID {
					next.values[existing][id].active = false
				}
			}
		}
		next.values[slot][id] = parameterOverride{value: value, version: version, active: true, trackID: trackID}
		if p.overrides.CompareAndSwap(old, next) {
			return nil
		}
	}
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
		clear(p.appliedVersions[:])
	}
	snapshot := p.overrides.Load()
	if snapshot == nil {
		return
	}
	for slot := range snapshot.values {
		for id := range snapshot.values[slot] {
			override := snapshot.values[slot][id]
			if !override.active || override.version == p.clearedVersions[slot][id] {
				continue
			}
			track := uint8(slot)
			if slot == 16 {
				track = 0xff
			} else if override.trackID != "" {
				track = 0xff
				for index := 0; index < len(score.Tracks); index++ {
					if score.Tracks[index].ID == override.trackID {
						track = uint8(index)
						break
					}
				}
				if track == 0xff {
					continue
				}
			} else if slot >= target.TrackCount() {
				continue
			}
			if newEngine && parameterMatches(score.Parameters, track, kernel.ParamID(id), override.value) {
				p.clearedVersions[slot][id] = override.version
				continue
			}
			if !newEngine && p.appliedVersions[slot][id] == override.version {
				continue
			}
			command := cmd.Command{Op: cmd.OpSetParam, Track: track, Index: uint16(id), Arg0: math.Float32bits(override.value)}
			if target.Push(command) {
				p.appliedVersions[slot][id] = override.version
			}
		}
	}
}

func parameterMatches(parameters []ParameterValue, track uint8, id kernel.ParamID, value float32) bool {
	for _, parameter := range parameters {
		if parameter.Track == track && parameter.ID == id {
			return math.Float32bits(parameter.Value) == math.Float32bits(value)
		}
	}
	return false
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
		p.meterScratch.TrackCount = uint8(min(len(p.current.Tracks), 16))
		for i := 0; i < int(p.meterScratch.TrackCount); i++ {
			p.meterScratch.TrackIDs[i] = p.current.Tracks[i].ID
		}
	}
	value := math.Float32frombits(message.B)
	switch {
	case message.Track < 16 && int(message.Track) < int(p.meterScratch.TrackCount):
		assignMeter(&p.meterScratch.Tracks[message.Track], message.A, value)
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
func (p *Player) PendingPatterns() [16]SlotRequest {
	if batch := p.slotLaunches.Load(); batch != nil {
		var requests [16]SlotRequest
		for index, queued := range batch.requests {
			requests[index] = queued.request
		}
		return requests
	}
	return [16]SlotRequest{}
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
		p.beginRequestedSong()
		if p.read == p.buffered {
			if p.fault != nil {
				if written > 0 {
					return written, nil
				}
				return 0, p.fault
			}
			p.renderBlock()
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
		p.previous, p.fadeRemaining = nil, 0
		if hasOffer {
			p.previous = p.current.Engine
			p.applyOverrides(next.Engine, next, true)
			p.heldNotes = [128]noteInput{}
			p.heldOrder = [128]uint64{}
			p.current = next
			p.trackCount.Store(uint32(next.Engine.TrackCount()))
			p.trackNames.Store(next.trackNames)
			p.resetMeters()
			p.fadeTotal, p.fadeRemaining = p.rate/200, p.rate/200
			p.emit(Event{Bar: int64(start), Name: next.Name, Kind: "edit"})
			p.jumpFadeRemaining = 0
		} else {
			p.jumpFadeRemaining = p.rate / 200
		}
		commands := [2]cmd.Command{{Op: cmd.OpSeek, Track: 0xff, Arg0: start - 1}, {Op: cmd.OpPlay, Track: 0xff}}
		if !p.current.Engine.PushBatch(commands[:]) {
			p.fault = fmt.Errorf("live engine rejected song start at bar %d", start)
			return
		}
		p.bar = int64(start) - 1
		p.clock = seq.Clock{SampleRate: int64(p.rate), BPMMilli: selected.BPMMilli, AnchorSample: p.sample, AnchorTick: p.bar * seq.TicksPerBar}
		p.tempoMilli.Store(selected.BPMMilli)
		p.nextBarSample = p.clock.SampleAtTick((p.bar + 1) * seq.TicksPerBar)
		p.position.Store(uint64(start)<<8 | 1)
		p.emit(Event{Bar: int64(start), Name: request.Scene, Kind: "song"})
	default:
	}
}

func (p *Player) renderBlock() {
	if p.sample == p.nextBarSample {
		p.bar++
		swapped := false
		select {
		case next := <-p.offers:
			if p.bar > int64(^uint32(0)) || !next.Engine.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: uint32(p.bar)}) || !next.Engine.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
				p.fault = fmt.Errorf("live engine rejected bar %d", p.bar)
				return
			}
			p.previous = p.current.Engine
			p.applyOverrides(next.Engine, next, true)
			p.fadeTotal = p.rate / 200 // five milliseconds
			p.fadeRemaining = p.fadeTotal
			p.heldNotes = [128]noteInput{}
			p.heldOrder = [128]uint64{}
			p.current = next
			p.trackCount.Store(uint32(next.Engine.TrackCount()))
			p.trackNames.Store(next.trackNames)
			p.resetMeters()
			swapped = true
			p.clock = seq.Clock{
				SampleRate: int64(p.rate), BPMMilli: next.BPMMilli,
				AnchorSample: p.sample, AnchorTick: p.bar * seq.TicksPerBar,
			}
			p.tempoMilli.Store(next.BPMMilli)
			select {
			case p.events <- Event{Bar: p.bar + 1, Name: next.Name, Kind: "edit"}:
			default:
			}
		default:
		}
		if swapped {
			p.requeuePendingLaunches()
		}
		p.nextBarSample = p.clock.SampleAtTick((p.bar + 1) * seq.TicksPerBar)
	}
	frames := int(min(int64(blockFrames), p.nextBarSample-p.sample))
	if frames <= 0 {
		p.fault = fmt.Errorf("live transport did not advance at bar %d", p.bar+1)
		return
	}
	tick := p.clock.TickAtSample(p.sample)
	p.position.Store(uint64((tick/seq.TicksPerBar+1)<<8 | (tick%seq.TicksPerBar)/seq.TicksPerStep + 1))
	p.queueLiveNotes()
	if p.fault != nil {
		return
	}
	p.queueSceneLaunch()
	p.queueSlotLaunches()
	p.applyOverrides(p.current.Engine, p.current, false)
	p.current.Engine.Render(p.left[:frames], p.right[:frames])
	if index, sequence := p.current.Engine.CurrentScene(); index >= 0 && index < len(p.current.SceneIDs) && (p.sceneEngine != p.current.Engine || p.sceneSequence != sequence) {
		firstScene := p.sceneEngine == nil
		p.sceneEngine, p.sceneSequence = p.current.Engine, sequence
		p.scene.Store(&p.current.SceneIDs[index])
		request := p.launches.Load()
		manual := request != nil && request.submitted.Load() && request.targetTick < p.clock.TickAtSample(p.sample+int64(frames))
		if !firstScene && !manual {
			p.emit(Event{Bar: tick/seq.TicksPerBar + 1, Name: p.current.SceneIDs[index], Kind: "song-scene"})
		}
	}
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
	// The engine output is post-limiter. Copy it only after transport fades have
	// been applied, and never wait for the independent meter worker.
	p.loudness.push(p.left[:frames], p.right[:frames])
	for i := 0; i < frames; i++ {
		binary.LittleEndian.PutUint32(p.pcm[i*8:], math.Float32bits(p.left[i]))
		binary.LittleEndian.PutUint32(p.pcm[i*8+4:], math.Float32bits(p.right[i]))
	}
	endTick := p.clock.TickAtSample(p.sample + int64(frames))
	p.completeSceneLaunch(endTick)
	p.completeSlotLaunches(endTick)
	p.sample += int64(frames)
	p.buffered, p.read = frames*8, 0
}

func (p *Player) queueLiveNotes() {
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
			if kind == "drums" {
				lane, _ = GMDrumLane(int(held.Note))
				command.Index = lane
			} else if kind == "piano" || kind == "poly" {
				lane, command.Index = uint16(held.Note), uint16(held.Note)
			}
			final[track][lane], touched[track][lane] = command, true
		}
		p.heldNotes, p.heldOrder = [128]noteInput{}, [128]uint64{}
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
		if snapshot.poly[track] {
			p.emit(Event{Track: input.Track, Name: cmd.PolyLiveUnsupported, Kind: "note-error"})
			continue
		}
		slot, free, latest := -1, -1, -1
		sameRoute := func(a noteInput) bool {
			if a.Track != input.Track {
				return false
			}
			if kind == "piano" || kind == "poly" {
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
			p.heldNotes[free], p.heldOrder[free] = input, p.noteOrder
		} else {
			if slot < 0 || p.heldNotes[slot].Track != input.Track || p.heldNotes[slot].Note != input.Note {
				continue
			}
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
		command := cmd.Command{Track: track}
		if kind == "acid" || kind == "piano" || kind == "graph" || kind == "poly" {
			if kind == "piano" && (input.Note < 21 || input.Note > 108) {
				p.emit(Event{Track: input.Track, Name: "piano note must be in MIDI range 21–108", Kind: "note-error"})
				continue
			}
			if input.On {
				command.Op = cmd.OpNoteOn
				command.Arg0 = uint32(input.Note) | uint32(input.Velocity)<<8
			} else {
				command.Op, command.Index = cmd.OpNoteOff, 0xffff
				if kind == "piano" || kind == "graph" || kind == "poly" {
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
		lane := uint16(0)
		if kind == "drums" || kind == "piano" || kind == "poly" {
			lane = command.Index
			if input.On && kind != "drums" {
				lane = uint16(input.Note)
			}
		}
		final[track][lane], touched[track][lane] = command, true
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

func (p *Player) queueSceneLaunch() {
	request := p.launches.Load()
	if request == nil || request.submitted.Load() {
		return
	}
	if len(p.offers) != 0 && p.sample < p.nextBarSample && request.targetTick >= p.clock.TickAtSample(p.nextBarSample) {
		return
	}
	endSample := min(p.sample+blockFrames, p.nextBarSample)
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

func (p *Player) queueSlotLaunches() {
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
		endSample := min(p.sample+blockFrames, p.nextBarSample)
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
			if track >= 0 && track < 16 {
				for candidate, name := range p.current.Tracks[track].Slots {
					if name == request.request.Pattern {
						slot = candidate
						break
					}
				}
			}
			if track < 0 || track >= 16 || slot < 0 {
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

func (p *Player) completeSceneLaunch(endTick int64) {
	request := p.launches.Load()
	if request == nil || !request.submitted.Load() || request.targetTick >= endTick {
		return
	}
	if p.launches.CompareAndSwap(request, nil) {
		p.emit(Event{Bar: request.targetTick/seq.TicksPerBar + 1, Name: request.name, Kind: "scene"})
	}
}

func (p *Player) completeSlotLaunches(endTick int64) {
	for {
		old := p.slotLaunches.Load()
		if old == nil {
			return
		}
		next := *old
		var landed []slotLaunch
		changed := false
		for index, request := range old.requests {
			if request.request.Track != "" && request.submitted && request.TargetTick < endTick {
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
