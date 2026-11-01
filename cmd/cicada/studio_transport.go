package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/host/midi"
	"m31labs.dev/cicada/internal/audiobackend"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/musictime"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/sdk/director"
)

type studioTransport struct {
	takeInputOpener studioTakeInputOpener
	// healthTicks starts the health frame schedule. Tests replace it with a
	// manual clock; nil selects a wall-clock ticker.
	healthTicks       func(period time.Duration) (ticks <-chan time.Time, stop func())
	path              string
	audioBackend      string
	mu                sync.Mutex
	pollMu            sync.Mutex
	audioConfigMu     sync.Mutex // serialize configuration while native permission waits
	stream            *liveplay.Player
	preview           *liveplay.Player
	previewTimer      *time.Timer
	previewGeneration uint64
	previewOutputOnly bool
	previewLoop       bool
	previewSourceHash [32]byte
	soundPreviewOwner string
	pendingSoundLease string
	soundLeases       [32]soundPreviewLease
	previewOps        []edits.PreviewOp
	audio             studioAudioDevice
	audioOptions      studioAudioOptions
	monitorWrite      bool
	preparedCapture   bool
	lastInput         studioAudioSnapshot
	sampleRate        int
	cancel            context.CancelFunc
	last              [32]byte
	livePlan          *livePlan
	offers            []studioOffer        // Published scores not yet reported landed, oldest first.
	publications      uint64               // Last publication ID handed out; never reused.
	prepareHook       func(liveplay.Score) // Test seam: runs after poll compiles a replacement, before it takes mu.
	arena             *liveplay.PatchArena
	patchRevision     uint64
	playing           bool
	browserPlaying    bool
	browserSampleRate int
	pending           bool
	pendingSong       string
	pendingSongID     uint64
	snapshotSeq       uint64
	midiTimeline      midi.Timeline
	midiFrameWall     int64
	scene             string
	stoppedTracks     map[string]bool
	activeSlots       map[string]string
	landed            int64
	errText           string
	history           *studioHistory
	audioNull         bool
	latestMeter       atomic.Pointer[studioMeterSnapshot]
	meterSequence     atomic.Uint64
	telemetry         *liveplay.Publisher
	pendingCapture    *capture.Calibration
	listening         *studioListening

	audioSettingsPath    string
	audioSettingsErr     error
	audioSettingsWarning string
}

type studioMeterSnapshot struct {
	sequence uint64
	frame    liveplay.MeterFrame
	loudness liveplay.LoudnessSnapshot
}

// snapshotFrames carries the latest binary telemetry frames, base64-encoded.
type snapshotFrames struct {
	Transport string `json:"transport,omitempty"`
	Meters    string `json:"meters,omitempty"`
	Health    string `json:"health,omitempty"`
}

type transportSnapshot struct {
	MIDIClockEpoch      uint64                       `json:"midiClockEpoch"`
	FrameAgeNanos       *int64                       `json:"frameAgeNanos,omitempty"`
	NextTempo           *musictime.ObservedTempoStep `json:"nextTempo,omitempty"`
	Frames              *snapshotFrames              `json:"frames,omitempty"`
	Type                string                       `json:"type"`
	Sequence            uint64                       `json:"sequence"`
	ActiveBackend       string                       `json:"activeBackend"`
	BrowserPlaying      bool                         `json:"browserPlaying,omitempty"`
	Playing             bool                         `json:"playing"`
	Bar                 int64                        `json:"bar"`
	Step                int64                        `json:"step"`
	Pending             bool                         `json:"pending"`
	PendingScene        string                       `json:"pendingScene,omitempty"`
	PendingSong         string                       `json:"pendingSong,omitempty"`
	PendingSlots        map[string]string            `json:"pendingSlots,omitempty"`
	PendingSlotQuantize map[string]uint32            `json:"pendingSlotQuantize,omitempty"`
	PendingQuantize     uint32                       `json:"pendingQuantize,omitempty"`
	StoppedTracks       []string                     `json:"stoppedTracks,omitempty"`
	ActiveSlots         map[string]string            `json:"activeSlots,omitempty"`
	Scene               string                       `json:"scene,omitempty"`
	Landed              int64                        `json:"landedBar,omitempty"`
	Error               string                       `json:"error"`
	Listening           *studioListening             `json:"listening,omitempty"`
	ListeningStatus     string                       `json:"listeningStatus"`
	Streaming           *studioStreamHealth          `json:"streaming,omitempty"`
}

type studioStreamHealth struct {
	ArenaBytes    int64  `json:"arenaBytes"`
	MissingFrames uint64 `json:"missingFrames"`
	ReadErrors    uint64 `json:"readErrors"`
	Recoveries    uint64 `json:"recoveries"`
	Error         string `json:"error,omitempty"`
}

func newStudioTransport(path string) *studioTransport {
	return &studioTransport{telemetry: liveplay.NewPublisher(), path: path, audioBackend: string(audiobackend.DefaultFor("studio")), audioOptions: defaultStudioAudioOptions()}
}

func (t *studioTransport) selectedAudioBackend() string {
	if t.audioNull {
		return string(audiobackend.Null)
	}
	if t.audioBackend != "" {
		return t.audioBackend
	}
	return string(audiobackend.DefaultFor("studio"))
}

func validStudioQuantize(value cmd.Quantize) bool {
	switch value {
	case 0, 1, 2, 5, 6, 8, cmd.QuantizePhrase:
		return true
	default:
		return false
	}
}

func studioQuantizeLabel(value cmd.Quantize) string {
	switch value {
	case 0:
		return "now"
	case 1:
		return "the next beat"
	case 2, 5:
		return "the next bar"
	case 6:
		return "the next 2-bar boundary"
	case 8:
		return "the next 4-bar boundary"
	case cmd.QuantizePhrase:
		return "the next phrase"
	default:
		return "the requested boundary"
	}
}

func (t *studioTransport) snapshot() transportSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.snapshotSeq++
	// Event delivery is bounded; recover scene truth even if a UI event was dropped.
	if t.stream != nil {
		if scene := t.stream.CurrentScene(); scene != "" && scene != t.scene {
			t.scene, t.activeSlots = scene, nil
		}
	}
	state := transportSnapshot{Type: "cicada/transport", Sequence: t.snapshotSeq, MIDIClockEpoch: t.midiTimeline.Epoch(), Playing: t.playing, Bar: 1, Step: 1, Pending: t.pending, Scene: t.scene, Landed: t.landed, Error: t.errText}
	if t.stream != nil {
		stats := t.stream.StreamStats()
		if stats.ArenaBytes > 0 {
			state.Streaming = &studioStreamHealth{ArenaBytes: stats.ArenaBytes, MissingFrames: stats.MissingFrames, ReadErrors: stats.ReadErrors, Recoveries: stats.Recoveries}
			if err := t.stream.StreamError(); err != nil {
				state.Streaming.Error = err.Error()
			}
		}
	}
	state.ListeningStatus = "Full song"
	if t.listening != nil && t.stream != nil {
		listening := *t.listening
		if r := t.stream.PlaybackRange(); r != nil {
			listening.StartTick, listening.EndTick, listening.Loop = r.StartTick, r.EndTick, r.Loop
		}
		listening.Completed, listening.Cycles = t.stream.RangeFinished(), t.stream.RangeCycles()
		state.Listening = &listening
		state.Playing = t.playing && !listening.Completed
		state.ListeningStatus = listening.label()
	}
	if t.browserPlaying && t.browserSampleRate > 0 {
		state.ActiveBackend = fmt.Sprintf("browser · AudioWorklet · %s", studioAudioRateLabel(t.browserSampleRate))
		state.BrowserPlaying = true
	} else if t.playing && t.sampleRate > 0 {
		backend := studioAudioBackendLabel(audiobackend.Name(t.selectedAudioBackend()))
		state.ActiveBackend = fmt.Sprintf("%s · %s", backend, studioAudioRateLabel(t.sampleRate))
	}
	if len(t.activeSlots) != 0 {
		state.ActiveSlots = make(map[string]string, len(t.activeSlots))
		for track, pattern := range t.activeSlots {
			state.ActiveSlots[track] = pattern
		}
	}
	if t.stream != nil {
		if id := t.stream.PendingSongEntryID(); id != 0 && id == t.pendingSongID {
			state.PendingSong = t.pendingSong
		}
		state.PendingScene = t.stream.PendingScene()
		state.PendingQuantize = t.stream.PendingSceneQuantize()
		for _, request := range t.stream.PendingPatterns() {
			if request.Track != "" {
				if state.PendingSlots == nil {
					state.PendingSlots = make(map[string]string)
				}
				state.PendingSlots[request.Track] = request.Pattern
			}
		}
		state.PendingSlotQuantize = t.stream.PendingPatternQuantizes()
		position := t.stream.Position()
		state.Bar, state.Step = position.Bar, position.Step
	}
	for track, stopped := range t.stoppedTracks {
		if stopped {
			state.StoppedTracks = append(state.StoppedTracks, track)
		}
	}
	if t.telemetry != nil {
		latest, next := t.telemetry.LatestClock()
		if frame, err := liveplay.DecodeTransportFrame(latest[liveplay.FrameTransport]); err == nil {
			now := time.Now().UnixNano()
			age := int64(-1) // A wall-clock discontinuity cannot be extrapolated.
			if frame.WallNanos > 0 && frame.WallNanos <= now {
				age = now - frame.WallNanos
			}
			state.FrameAgeNanos = &age
			tick := (int64(frame.Bar)-1)*3840 + (int64(frame.Beat)-1)*960 + int64(frame.Tick)
			if next.Valid(tick, int64(frame.TempoMilli)) {
				state.NextTempo = &next
			}
			if t.stream != nil {
				t.observeMIDIFrameLocked(frame, next, now, midi.HostTimeNanos())
				state.MIDIClockEpoch = t.midiTimeline.Epoch()
			}
		}
		frames := snapshotFrames{
			Transport: base64.StdEncoding.EncodeToString(latest[liveplay.FrameTransport]),
			Meters:    base64.StdEncoding.EncodeToString(latest[liveplay.FrameMeters]),
			Health:    base64.StdEncoding.EncodeToString(latest[liveplay.FrameHealth]),
		}
		if frames != (snapshotFrames{}) {
			state.Frames = &frames
		}
	}
	return state
}

func (t *studioTransport) start() error { return t.startFrom(-1, "", nil, [32]byte{}) }

func (t *studioTransport) setBrowserAudioStatus(playing bool, sampleRate int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if playing {
		t.stopPreviewLocked()
	}
	t.browserPlaying = playing
	if playing {
		t.browserSampleRate = sampleRate
	} else {
		t.browserSampleRate = 0
	}
}

func studioAudioRateLabel(sampleRate int) string {
	if sampleRate%1000 == 0 {
		return fmt.Sprintf("%dk", sampleRate/1000)
	}
	return fmt.Sprintf("%.1fk", float64(sampleRate)/1000)
}

func (t *studioTransport) startFrom(index int, scene string, prepared *liveplay.Score, preparedHash [32]byte) error {
	return t.startPrepared(index, scene, prepared, preparedHash, nil)
}

func (t *studioTransport) startPrepared(index int, scene string, prepared *liveplay.Score, preparedHash [32]byte, listening *studioListening, beforePlay ...func(*liveplay.Player, studioAudioDevice) error) error {
	preparedConsumed := false
	defer func() {
		if prepared != nil && !preparedConsumed && prepared.Engine != nil {
			_ = prepared.Engine.Close()
		}
	}()
	t.pollMu.Lock()
	defer t.pollMu.Unlock()
	sampleRate, err := t.ensureSampleRate()
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopPreviewLocked()
	changeMIDIClock := t.stream == nil || !t.playing || index >= 0 || listening != nil || t.listening != nil
	if t.stream != nil {
		if len(beforePlay) != 0 {
			return fmt.Errorf("stop playback before preparing live amp Write")
		}
		wasListening := t.listening != nil
		if listening != nil {
			offered := *prepared
			offered.Revision = t.recordOfferLocked(nil) // A listening range has no patchable plan.
			if err := t.stream.Offer(offered); err != nil {
				t.forgetOfferLocked(offered.Revision)
				return err
			}
			preparedConsumed = true
			if err := t.stream.PlayRange(listening.playbackRange()); err != nil {
				return err
			}
			t.listening, t.last, t.pending = listening, preparedHash, true
		} else if index >= 0 {
			t.stream.ClearRange()
			t.listening = nil
		} else if t.listening != nil && t.stream.RangeFinished() {
			if err := t.stream.PlayRange(t.listening.playbackRange()); err != nil {
				return err
			}
		}
		audioFailed := false
		if t.audio != nil {
			if err := t.audio.Err(); err != nil {
				t.audio.Pause()
				_ = t.audio.Close()
				t.audio = nil
				audioFailed = true
				t.playing, t.errText = false, err.Error()
			}
		}
		if index >= 0 {
			if prepared != nil && (preparedHash != t.last || wasListening) {
				next := *prepared
				plan := t.planAtFingerprint(preparedHash, sampleRate)
				if plan != nil && listening == nil && !wasListening {
					plan.prepareScore(&next)
				} else {
					plan = nil
				}
				next.Revision = t.recordOfferLocked(plan)
				if err := t.stream.Offer(next); err != nil {
					t.forgetOfferLocked(next.Revision)
					return err
				}
				preparedConsumed = true
				t.last = preparedHash
			}
			requestID, err := t.stream.QueueSongEntry(index, scene)
			if err != nil {
				return err
			}
			t.pendingSong, t.pendingSongID = scene, requestID
		}
		if !t.playing {
			t.stream.ResetLoudness()
		}
		if t.audio == nil && (!t.playing || audioFailed) {
			audio, openErr := openStudioAudio(t.stream, t.audioOptions, t.selectedAudioBackend(), sampleRate)
			if openErr != nil {
				t.playing, t.errText = false, openErr.Error()
				return openErr
			}
			audio.SetMonitor(studioMonitorOptions(t.audioOptions))
			t.audio = audio
		}
		if t.audio != nil {
			if err := t.prepareCaptureLocked(t.audio, t.stream.BPMMilli(), t.stream.CountInBars()); err != nil {
				return err
			}
			if err := t.audio.Play(); err != nil {
				t.audio.Pause()
				_ = t.audio.Close()
				t.audio = nil
				t.playing, t.errText = false, err.Error()
				return err
			}
		}
		t.playing, t.errText = true, ""
		if changeMIDIClock {
			t.resetMIDIClockLocked()
		}
		t.stream.SetTransportPlaying(true)
		t.stream.PublishTelemetry()
		t.observeMIDIClockLocked()
		t.publishHealthLocked()
		return nil
	}
	var fingerprint [32]byte
	var initial liveplay.Score
	if prepared != nil {
		fingerprint, initial = preparedHash, *prepared
		if initial.SampleRate != sampleRate {
			return fmt.Errorf("prepared score is %d Hz but the selected audio endpoints use %d Hz", initial.SampleRate, sampleRate)
		}
	} else {
		var err error
		fingerprint, err = playSourceHash(t.path)
		if err != nil {
			return err
		}
		if t.audio != nil && t.audio.Armed() {
			p, loadErr := loadProject(t.path)
			err = loadErr
			if err == nil {
				initial, err = compileLiveProjectAtRate(t.path, captureBacking(p), sampleRate)
			}
		} else {
			initial, err = compileLiveScoreAtRate(t.path, sampleRate)
		}
		if err != nil {
			return err
		}
	}
	// Plans and scene settings must describe the same committed source as the
	// initial score. Capture backing has its own filtered engine authority.
	var initialPlan *livePlan
	if listening == nil {
		initialPlan = t.planAtFingerprint(fingerprint, sampleRate)
	}
	if initialPlan != nil {
		initialPlan.prepareScore(&initial)
	}
	stream, err := liveplay.New(initial, sampleRate)
	if err != nil {
		if initial.Engine != nil {
			_ = initial.Engine.Close()
		}
		return err
	}
	preparedConsumed = true
	streamAdopted := false
	defer func() {
		if !streamAdopted {
			stream.Close()
		}
	}()
	if listening != nil {
		if err := stream.PlayRange(listening.playbackRange()); err != nil {
			return err
		}
	}
	var requestID uint64
	if index >= 0 {
		requestID, err = stream.QueueSongEntry(index, scene)
		if err != nil {
			return err
		}
	}
	audio := t.audio
	if audio != nil && audio.Armed() {
		audio.SetSource(stream)
	} else {
		audio, err = openStudioAudio(stream, t.audioOptions, t.selectedAudioBackend(), sampleRate)
		if err != nil {
			return err
		}
	}
	audio.SetMonitor(studioMonitorOptions(t.audioOptions))
	bound := false
	defer func() {
		if bound && !streamAdopted {
			if device, ok := audio.(studioAmpFollowDevice); ok {
				_ = device.SetAmpFollower(nil)
			}
			t.monitorWrite = false
		}
	}()
	if len(beforePlay) > 0 {
		if err := beforePlay[0](stream, audio); err != nil {
			return err
		}
		bound = true
	}
	if audio != nil {
		countInBars := 1
		if initial.CountInBars != nil {
			countInBars = *initial.CountInBars
		}
		if err := t.prepareCaptureLocked(audio, initial.BPMMilli, countInBars); err != nil {
			return err
		}
		if err := audio.Play(); err != nil {
			audio.Pause()
			_ = audio.Close()
			return err
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.stream, t.audio, t.sampleRate, t.last, t.cancel = stream, audio, sampleRate, fingerprint, cancel
	t.livePlan, t.offers, t.arena, t.patchRevision = initialPlan, nil, liveplay.NewPatchArena(16), 0
	streamAdopted = true
	t.playing, t.errText = true, ""
	t.listening = listening
	if index >= 0 {
		t.pendingSong, t.pendingSongID = scene, requestID
	} else {
		t.scene = stream.CurrentScene()
	}
	stream.SetPublisher(t.telemetry)
	t.resetMIDIClockLocked()
	stream.PublishTelemetry()
	t.observeMIDIClockLocked()
	t.publishHealthLocked()
	healthTicks, stopHealthTicks := t.startHealthTicks()
	go t.watch(ctx, stream, healthTicks, stopHealthTicks)
	return nil
}

func (t *studioTransport) isPlayingFingerprint(fingerprint [32]byte) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stream != nil && t.last == fingerprint
}

// studioOffer pairs a published score's unique publication token with the
// plan compiled for it. The plan is nil when the offer has no complete plan.
//
// Identity rules. A publication ID is a transport-owned counter, so every Offer
// has its own token even when its content repeats (A, B, A). The content
// fingerprint is separate: it only tells poll whether the files changed. The
// player keeps only the newest offer that has not started, so a landing event
// names exactly one ledger entry; that entry and every older one are done.
type studioOffer struct {
	token string
	plan  *livePlan
}

// maxStudioOffers bounds the ledger; the player keeps one pending score and a
// 32-event buffer, so older offers cannot still report a landing.
const maxStudioOffers = 64

// studioPublicationToken renders a publication ID as the Score.Revision token.
func studioPublicationToken(id uint64) string { return "p" + strconv.FormatUint(id, 10) }

// recordOfferLocked allocates the next publication token and queues it before
// the Offer call, so a landing event cannot arrive ahead of its ledger entry.
// Called with mu held.
func (t *studioTransport) recordOfferLocked(plan *livePlan) string {
	t.publications++
	token := studioPublicationToken(t.publications)
	if len(t.offers) == maxStudioOffers {
		t.offers = append(t.offers[:0], t.offers[1:]...)
	}
	t.offers = append(t.offers, studioOffer{token: token, plan: plan})
	t.pending = true
	return token
}

// forgetOfferLocked removes an offer the player rejected. Called with mu held.
func (t *studioTransport) forgetOfferLocked(token string) {
	for i, offer := range t.offers {
		if offer.token == token {
			t.offers = append(t.offers[:i], t.offers[i+1:]...)
			break
		}
	}
	t.pending = len(t.offers) > 0
}

// playbackFingerprint is the one place that hashes committed playback state:
// the entry file at the transport's own path and every source it names. Poll,
// activation after any commit (entry or secondary session, changed or no-op),
// scene launches and the recording handoff all call it, so they cannot
// disagree about what "unchanged" means. The caller holds pollMu.
func (t *studioTransport) playbackFingerprint() ([]byte, [32]byte, error) {
	var empty [32]byte
	source, err := os.ReadFile(t.path)
	if err != nil {
		return nil, empty, err
	}
	fingerprint, err := playSourceHashBytes(t.path, source)
	return source, fingerprint, err
}

// Called with mu held. A hash mismatch leaves activation unavailable until
// the watcher offers a complete plan; playback preparation keeps its behavior.
func (t *studioTransport) planAtFingerprint(fingerprint [32]byte, rate int) *livePlan {
	if t.audio != nil && t.audio.Armed() {
		return nil
	}
	source, err := os.ReadFile(t.path)
	if err != nil {
		return nil
	}
	hash, err := playSourceHashBytes(t.path, source)
	if err != nil || hash != fingerprint {
		return nil
	}
	plan, _ := buildLivePlan(t.path, source, rate)
	return plan
}

// publishHealthLocked publishes one health frame. Call it with t.mu held.
// CPULoad is the player's render-time average, and 0 before any block.
func (t *studioTransport) publishHealthLocked() {
	var frame liveplay.HealthFrame
	if t.stream != nil {
		frame.CPULoad = float32(t.stream.CPULoad())
	}
	if t.audio != nil {
		snapshot := t.audio.Snapshot()
		frame.Dropouts = uint32(min(snapshot.Dropouts, uint64(^uint32(0))))
		frame.LatencyNanos = int64(snapshot.OutputLatencyMS * float64(time.Millisecond))
	}
	t.telemetry.PublishHealth(frame)
}

// pause halts playback and keeps the stream, so play resumes at the same
// position (D18: Space toggles play and pause).
func (t *studioTransport) pause() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pauseLocked()
}

func (t *studioTransport) pauseLocked() {
	if t.playing {
		t.resetMIDIClockLocked()
	}
	if t.audio != nil {
		audio := t.audio
		audio.Pause()
		t.lastInput = audio.Snapshot()
		// Release duplex hosts after Pause so they no longer hold the input endpoint.
		if audio.StopClosesDevice() {
			if err := audio.Close(); err != nil {
				t.errText = err.Error()
			}
			t.audio = nil
		}
	}
	if t.stream != nil {
		t.stream.CancelScene()
		t.stream.CancelPatterns()
		t.stream.CancelStart()
		// Rendering has halted, so tell telemetry clients the transport is paused.
		t.stream.SetTransportPlaying(false)
		t.stream.PublishTelemetry()
	}
	t.playing = false
	t.pendingSong = ""
	t.pendingSongID = 0
	t.pendingCapture = nil
}

// stop halts playback and returns to bar 1 by discarding the stream (D18).
// The next start builds a new stream from the beginning.
func (t *studioTransport) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopLocked()
}

func (t *studioTransport) stopLocked() {
	if t.monitorWrite {
		if device, ok := t.audio.(studioAmpFollowDevice); ok {
			_ = device.SetAmpFollower(nil)
		}
		t.monitorWrite = false
	}
	t.stopPreviewLocked()
	t.pauseLocked()
	if t.stream != nil {
		// Detach first so the old watcher cannot publish after a restart.
		t.stream.SetPublisher(nil)
		t.telemetry.PublishTransport(liveplay.TransportFrame{
			SampleRate: uint32(t.sampleRate), TempoMilli: uint32(t.stream.BPMMilli()),
			Bar: 1, Beat: 1,
		})
	}
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
	if t.audio != nil && !t.audio.Armed() {
		if err := t.audio.Close(); err != nil {
			t.errText = err.Error()
		}
		t.audio = nil
	}
	if t.audio != nil {
		t.audio.SetSource(nil)
	}
	if t.stream != nil {
		t.stream.Close()
		t.stream = nil
	}
	t.livePlan, t.offers, t.arena = nil, nil, nil
	// A discarded stream has no muted tracks; do not report them as stopped.
	t.stoppedTracks = nil
	t.activeSlots = nil
	t.scene = ""
	t.pending = false
	t.landed = 0
	t.listening = nil
}

// returnToStart moves to bar 1. A playing transport keeps playing from the
// start; a stopped one stays stopped (D18).
func (t *studioTransport) returnToStart() error {
	t.mu.Lock()
	wasPlaying := t.playing
	t.mu.Unlock()
	t.stop()
	if wasPlaying {
		return t.start()
	}
	return nil
}

func (t *studioTransport) launchScene(name string, quantizes ...cmd.Quantize) error {
	quantize := cmd.Quantize(2)
	if len(quantizes) != 0 {
		quantize = quantizes[0]
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.playing || t.stream == nil {
		return fmt.Errorf("press Play before launching a scene")
	}
	if err := t.stream.LaunchSceneQuantized(name, quantize); err != nil {
		return err
	}
	t.errText = ""
	if t.history != nil {
		position := t.stream.Position()
		t.history.record("queued", fmt.Sprintf("Scene %s queued for %s", name, studioQuantizeLabel(quantize)), position.Bar, position.Step, "")
	}
	return nil
}

func (t *studioTransport) launchPattern(track, pattern string, quantizes ...cmd.Quantize) error {
	quantize := cmd.Quantize(2)
	if len(quantizes) != 0 {
		quantize = quantizes[0]
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.playing || t.stream == nil {
		return fmt.Errorf("press Play before launching a pattern")
	}
	if err := t.stream.SelectPatternQuantized(track, pattern, quantize); err != nil {
		return err
	}
	t.errText = ""
	if t.history != nil {
		position := t.stream.Position()
		t.history.record("queued", fmt.Sprintf("%s queued on %s for %s", pattern, track, studioQuantizeLabel(quantize)), position.Bar, position.Step, "")
	}
	return nil
}

func (t *studioTransport) stopTrack(track string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.playing || t.stream == nil {
		return fmt.Errorf("press Play before stopping a track")
	}
	index, ok := t.stream.TrackIndex(track)
	if !ok {
		return fmt.Errorf("unknown track %q", track)
	}
	if err := t.stream.SetMute(index, true); err != nil {
		return err
	}
	if t.stoppedTracks == nil {
		t.stoppedTracks = make(map[string]bool)
	}
	t.stoppedTracks[track] = true
	return nil
}

func (t *studioTransport) resumeStoppedTracks(only string) {
	for track := range t.stoppedTracks {
		if only != "" && track != only {
			continue
		}
		if index, ok := t.stream.TrackIndex(track); ok {
			_ = t.stream.SetMute(index, false)
		}
		delete(t.stoppedTracks, track)
	}
}

func (t *studioTransport) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel != nil {
		t.cancel()
	}
	if t.audio != nil {
		t.audio.Pause()
		_ = t.audio.Close()
		t.audio = nil
	}
	if t.stream != nil {
		t.stream.Close()
	}
	t.playing = false
}

func (t *studioTransport) resetLoudness() {
	t.mu.Lock()
	stream := t.stream
	t.mu.Unlock()
	if stream != nil {
		stream.ResetLoudness()
	}
}

func (t *studioTransport) loudness() liveplay.LoudnessSnapshot {
	t.mu.Lock()
	stream := t.stream
	t.mu.Unlock()
	if stream != nil {
		return stream.Loudness()
	}
	return liveplay.LoudnessSnapshot{}
}

// healthFramePeriod is the interval between published health frames (twice a
// second).
const healthFramePeriod = 500 * time.Millisecond

func (t *studioTransport) startHealthTicks() (<-chan time.Time, func()) {
	if t.healthTicks != nil {
		return t.healthTicks(healthFramePeriod)
	}
	ticker := time.NewTicker(healthFramePeriod)
	return ticker.C, ticker.Stop
}

func (t *studioTransport) watch(ctx context.Context, stream *liveplay.Player, healthFrames <-chan time.Time, stopHealthFrames func()) {
	ticker := time.NewTicker(100 * time.Millisecond)
	health := time.NewTicker(time.Second)
	telemetryTicker := time.NewTicker(40 * time.Millisecond)
	defer ticker.Stop()
	defer health.Stop()
	defer telemetryTicker.Stop()
	defer stopHealthFrames()
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-stream.Events():
			t.markLanded(event)
		case frame := <-stream.Meters():
			t.publishMeter(frame, stream.Loudness())
		case <-ticker.C:
			t.poll()
		case <-telemetryTicker.C:
			t.mu.Lock()
			if t.stream == stream && t.playing && stream.RangeFinished() && !t.captureRunningLocked() {
				t.pauseLocked()
			}
			t.mu.Unlock()
			stream.PublishTelemetry()
			t.mu.Lock()
			if t.stream == stream {
				t.observeMIDIClockLocked()
			}
			t.mu.Unlock()
		case <-healthFrames:
			t.mu.Lock()
			t.publishHealthLocked()
			t.mu.Unlock()
		case <-health.C:
			t.mu.Lock()
			if t.audio != nil {
				if err := t.audio.Err(); err != nil {
					t.audio.Pause()
					t.errText, t.playing = err.Error(), false
				}
			}
			t.mu.Unlock()
		}
	}
}

func (t *studioTransport) publishMeter(frame liveplay.MeterFrame, loudnessSnapshots ...liveplay.LoudnessSnapshot) {
	sequence := t.meterSequence.Add(1)
	snapshot := studioMeterSnapshot{sequence: sequence, frame: frame}
	if len(loudnessSnapshots) != 0 {
		snapshot.loudness = loudnessSnapshots[0]
	} else {
		snapshot.loudness = t.loudness()
	}
	t.latestMeter.Store(&snapshot)
}

func (t *studioTransport) markLanded(event liveplay.Event) {
	t.mu.Lock()
	if event.Kind == "note-on" || event.Kind == "note-off" {
		t.mu.Unlock()
		return
	}
	switch event.Kind {
	case "scene":
		t.resumeStoppedTracks("")
		t.scene, t.landed = event.Name, event.Bar
		t.activeSlots = nil
	case "song-scene":
		t.scene, t.landed = event.Name, event.Bar
		t.activeSlots = nil
	case "scene-error":
		t.errText = fmt.Sprintf("scene %q is no longer in the playing score", event.Name)
	case "song":
		t.scene, t.landed = event.Name, event.Bar
		t.activeSlots = nil
	case "song-error":
		t.errText = fmt.Sprintf("song block %q is no longer in the playing score", event.Name)
	case "slot":
		t.resumeStoppedTracks(event.Track)
		t.landed = event.Bar
		if t.activeSlots == nil {
			t.activeSlots = make(map[string]string)
		}
		t.activeSlots[event.Track] = event.Name
	case "slot-error":
		t.errText = fmt.Sprintf("pattern %q is no longer on track %q in the playing score", event.Name, event.Track)
	case "note-error":
		t.errText = event.Name
	case "preview-error":
		t.errText = fmt.Sprintf("Preview %s on track %s ended, but its saved value could not be restored", event.Name, event.Track)
	case "patch-error":
		t.livePlan = nil
		t.last = [32]byte{} // Let poll reconstruct the committed score.
		t.errText = event.Name
	case "edit":
		t.landed = event.Bar
		// An edit event names its offer by the playback fingerprint token. Only
		// that offer's plan becomes the live plan, and offers still queued
		// behind it keep the transport pending. An unknown token (an offer the
		// player already superseded) says nothing about the playing engine.
		for i, offer := range t.offers {
			if offer.token == event.Revision {
				t.livePlan, t.offers = offer.plan, t.offers[i+1:]
				t.pending = len(t.offers) > 0
				break
			}
		}
	default:
		t.pending, t.landed = false, event.Bar
	}
	t.mu.Unlock()
	if t.history != nil {
		if event.Kind == "song" {
			t.history.record("landed", fmt.Sprintf("Song started at %s, bar %d", event.Name, event.Bar), event.Bar, 1, "")
		} else if event.Kind == "song-scene" {
			t.history.record("landed", fmt.Sprintf("Song advanced to %s at bar %d", event.Name, event.Bar), event.Bar, 1, "")
		} else if event.Kind == "song-error" {
			t.history.record("error", fmt.Sprintf("Song block %s was not in the playing score", event.Name), event.Bar, 1, "")
		} else if event.Kind == "slot" {
			t.history.record("landed", fmt.Sprintf("%s launched on %s at bar %d", event.Name, event.Track, event.Bar), event.Bar, 1, "")
		} else if event.Kind == "slot-error" {
			t.history.record("error", fmt.Sprintf("%s was not on %s in the playing score", event.Name, event.Track), event.Bar, 1, "")
		} else if event.Kind == "scene" {
			t.history.record("landed", fmt.Sprintf("Scene %s launched at bar %d", event.Name, event.Bar), event.Bar, 1, "")
		} else if event.Kind == "scene-error" {
			t.history.record("error", fmt.Sprintf("Scene %s was not in the playing score", event.Name), event.Bar, 1, "")
		} else if event.Kind == "note-error" {
			t.history.record("error", event.Name, event.Bar, 1, "")
		} else if event.Kind == "note-on" || event.Kind == "note-off" {
			return
		} else {
			t.history.record("landed", fmt.Sprintf("Edit landed at bar %d", event.Bar), event.Bar, 1, "")
		}
	}
}

func (t *studioTransport) poll() {
	t.pollMu.Lock()
	defer t.pollMu.Unlock()
	source, fingerprint, err := t.playbackFingerprint()
	if err != nil {
		t.setError(err)
		return
	}
	t.mu.Lock()
	if fingerprint == t.last {
		t.mu.Unlock()
		// The files equal the playing score, so a take armed for other content
		// can never be published. Drop its handoff.
		if t.history != nil {
			t.history.consumeRecordedTake("")
		}
		return
	}
	stream := t.stream
	sampleRate := t.sampleRate
	var listening *studioListening
	if t.listening != nil {
		copy := *t.listening
		listening = &copy
	}
	t.mu.Unlock()
	if stream == nil {
		return
	}
	if sampleRate <= 0 {
		sampleRate = liveSampleRate
	}
	// A listening range is not a patchable plan: only a plain score gets one.
	var plan *livePlan
	var project *project.Project
	listeningTargetError := false
	if listening == nil {
		plan, err = buildLivePlan(t.path, source, sampleRate)
		if err == nil {
			project = plan.project
		}
	} else {
		project, err = compileStudioSource(t.path, source)
		if err == nil {
			project, listening, err = prepareStudioListening(project, *listening)
			listeningTargetError = err != nil
		}
	}
	var next liveplay.Score
	if err == nil {
		next, err = compileStudioListeningScore(t.path, project, listening, sampleRate)
		if err == nil && listening != nil {
			r := listening.playbackRange()
			next.Range = &r
		}
	}
	if err != nil {
		if listeningTargetError {
			t.mu.Lock()
			t.stopLocked()
			t.mu.Unlock()
		}
		t.setError(err)
		return
	}
	if plan != nil {
		plan.prepareScore(&next)
	}
	if t.prepareHook != nil {
		t.prepareHook(next)
	}
	t.mu.Lock()
	if t.stream != stream {
		t.mu.Unlock()
		// Playback stopped or restarted while this replacement was prepared.
		// Offer never saw it, so poll still owns the engine and its workers.
		_ = next.Engine.Close()
		return
	}
	next.Revision = t.recordOfferLocked(plan)
	t.mu.Unlock()
	if err := stream.Offer(next); err != nil {
		t.mu.Lock()
		t.forgetOfferLocked(next.Revision)
		t.mu.Unlock()
		t.setError(err)
		return
	}
	t.mu.Lock()
	if t.stream != stream {
		t.mu.Unlock()
		return
	}
	t.last = fingerprint
	t.listening = listening
	// Clear the transport error when a valid revision lands (UX review finding 2).
	t.errText = ""
	position := stream.Position()
	t.mu.Unlock()
	if t.history != nil && !t.history.consumeRecordedTake(studioFingerprintKey(fingerprint)) {
		t.history.record("queued", "Edit queued for the next bar", position.Bar, position.Step, "")
	}
}

// activateLocked runs after a successful commit while pollMu still covers the
// file exchange. An offered engine keeps authority until its render event; a
// subsequent commit cannot patch against that engine before it starts playing.
//
// It reads the committed entry and its sources through playbackFingerprint, so
// a secondary-file session activates against the same state poll hashes. It
// returns that fingerprint and whether it could be computed.
func (t *studioTransport) activateLocked() ([32]byte, bool) {
	source, fingerprint, err := t.playbackFingerprint()
	if err != nil {
		return fingerprint, false
	}
	path := t.path
	t.activatePlanLocked(path, source, fingerprint)
	return fingerprint, true
}

func (t *studioTransport) activatePlanLocked(path string, source []byte, fingerprint [32]byte) {
	t.mu.Lock()
	rate := t.sampleRate
	t.mu.Unlock()
	if rate <= 0 {
		rate = liveSampleRate
	}
	next, err := buildLivePlan(path, source, rate)
	if err != nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stream == nil {
		t.livePlan = next
		return
	}
	if t.pending || t.audio != nil && t.audio.Armed() {
		return
	}
	delta := planDiff(t.livePlan, next)
	if delta.structural {
		return
	}
	if t.arena == nil {
		t.arena = liveplay.NewPatchArena(16)
	}
	batch := t.arena.Begin(t.patchRevision + 1)
	if batch == nil {
		return
	}
	for _, change := range delta.params {
		if change.entity == "global" {
			batch.SetParam(0xff, change.id, change.value)
			continue
		}
		target, err := t.stream.ResolvePreviewTrack(strings.TrimPrefix(change.entity, "track:"))
		if err != nil {
			batch.Release()
			return
		}
		batch.SetResolvedParam(target, change.id, change.value)
	}
	targets := make(map[uint8]liveplay.PreviewTarget)
	resolve := func(track uint8) (liveplay.PreviewTarget, bool) {
		if target, ok := targets[track]; ok {
			return target, true
		}
		if int(track) >= len(next.project.Tracks) {
			return liveplay.PreviewTarget{}, false
		}
		target, err := t.stream.ResolvePreviewTrack(next.project.Tracks[track].ID)
		if err != nil {
			return liveplay.PreviewTarget{}, false
		}
		targets[track] = target
		return target, true
	}
	for _, edit := range delta.edits {
		var track uint8
		switch edit := edit.(type) {
		case stepChange:
			track = edit.track
		case lenChange:
			track = edit.track
		case metaChange:
			track = edit.track
		}
		target, ok := resolve(track)
		if !ok {
			batch.Release()
			return
		}
		switch edit := edit.(type) {
		case stepChange:
			batch.Step(target, edit.slot, edit.index, edit.packed)
			if edit.chord != nil {
				batch.Chord(target, edit.slot, edit.index, edit.chord.Notes, edit.chord.Count)
			}
		case lenChange:
			batch.PatternLen(target, edit.slot, edit.length)
		case metaChange:
			batch.PatternMeta(target, edit.slot, edit.grid, edit.swing, edit.transpose)
		}
	}
	for _, change := range delta.bindings {
		target, ok := resolve(change.track)
		if !ok {
			batch.Release()
			return
		}
		batch.SceneBinding(target, change.scene, change.binding)
	}
	if err := t.stream.Patch(batch); err != nil {
		return
	}
	t.patchRevision++
	delta.adopt(next)
	t.last, t.livePlan, t.errText = fingerprint, next, ""
	if t.history != nil {
		// A patched take has no landing event; its handoff ends here.
		t.history.consumeRecordedTake(studioFingerprintKey(fingerprint))
	}
}

func (t *studioTransport) setError(err error) {
	t.mu.Lock()
	t.errText = err.Error()
	t.mu.Unlock()
}

func studioSameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Scheme == "http" && parsed.Host == r.Host
}

func (s *studio) transportCommand(w http.ResponseWriter, r *http.Request) {
	if !studioSameOrigin(r) {
		studioJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin transport commands are not allowed"})
		return
	}
	var input studioTransportInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&input); err != nil {
		studioJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.captureInstrument {
		studioJSON(w, http.StatusConflict, map[string]string{"error": "stop instrument recording before changing transport"})
		return
	}
	if s.captureID != "" && s.captureRecorder != nil {
		capture := s.captureRecorder.Snapshot()
		// An armed device may still play and pause while preparing a take.
		// Once capture begins, its input clock must keep the original backing
		// and range until the take drains. Stop is also Finish take so the
		// ordinary transport cannot strand a recorder with playback halted.
		if capture.Recording || capture.Completed {
			if input.Action != "stop" {
				studioJSON(w, http.StatusConflict, map[string]string{"error": "finish the take before changing playback; Stop saves the current take"})
				return
			}
			id := s.captureID
			if err := s.finishVocalCapture(id); err != nil {
				s.transport.setError(err)
				status := http.StatusUnprocessableEntity
				if errors.Is(err, errTakeRevision) {
					status = http.StatusConflict
				}
				studioJSON(w, status, map[string]any{"error": err.Error(), "retained": true, "take": id})
				return
			}
			studioJSON(w, http.StatusOK, s.transport.snapshot())
			return
		}
	}
	quantize := cmd.Quantize(2)
	// Preserve the existing API's explicit zero as next-bar timing. An omitted
	// value selects the score preference, including its "now" setting.
	if input.Quantize != nil && *input.Quantize != 0 {
		quantize = cmd.Quantize(*input.Quantize)
	}
	if (input.Action == "launch" || input.Action == "slot") && !validStudioQuantize(quantize) {
		studioJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid launch quantization"})
		return
	}
	switch input.Action {
	case "play":
		if err := s.transport.start(); err != nil {
			s.transport.setError(err)
			studioJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
	case "pause":
		s.transport.pause()
	case "stop":
		s.transport.stop()
	case "fullSong":
		s.transport.stop()
		if err := s.transport.start(); err != nil {
			s.transport.setError(err)
			studioJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
	case "loop", "loopSection", "audition", "patternAudition":
		if status, err := s.startListeningLocked(input); err != nil {
			studioJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
	case "home":
		if err := s.transport.returnToStart(); err != nil {
			s.transport.setError(err)
			studioJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
	case "trackStop":
		if err := s.transport.stopTrack(input.Track); err != nil {
			studioJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
	case "launch", "slot", "playFrom":
		current, err := os.ReadFile(s.path)
		if err == nil {
			if p, parseErr := compileStudioSource(s.path, current); parseErr == nil {
				s.lastGoodSource, s.lastGoodProject = current, p
			}
		}
		if err != nil || s.lastGoodProject == nil {
			studioJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "score is unavailable"})
			return
		}
		if input.Revision == "" || input.Revision != studioRevision(s.lastGoodSource) {
			studioJSON(w, http.StatusConflict, map[string]string{"error": "score changed on disk; reload before launching a scene"})
			return
		}
		project := s.lastGoodProject
		if input.Action != "playFrom" && input.Quantize == nil && project.Live != nil && project.Live.Land != "" {
			value, err := director.Quantize(project.Live.Land, project.Live.PhraseBars)
			if err != nil {
				studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
				return
			}
			quantize = cmd.Quantize(value)
		}
		s.transport.mu.Lock()
		hasStream := s.transport.stream != nil
		s.transport.mu.Unlock()
		if hasStream && input.Action != "playFrom" {
			s.transport.poll()
		}
		if input.Action == "playFrom" {
			latest, readErr := os.ReadFile(s.path)
			if readErr != nil || studioRevision(latest) != input.Revision {
				studioJSON(w, http.StatusConflict, map[string]string{"error": "score changed on disk; reload before starting the song"})
				return
			}
		}
		var launchErr error
		if input.Action == "launch" {
			found := false
			for _, scene := range project.Scenes {
				if scene.ID == input.Scene {
					found = true
					break
				}
			}
			if !found {
				studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": fmt.Sprintf("scene %q is not in the score", input.Scene)})
				return
			}
			launchErr = s.transport.launchScene(input.Scene, quantize)
		} else if input.Action == "slot" {
			found := false
			for _, track := range project.Tracks {
				if track.ID != input.Track {
					continue
				}
				for _, slot := range track.Slots {
					if slot != nil && *slot == input.Pattern {
						found = true
						break
					}
				}
				break
			}
			if !found {
				studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": fmt.Sprintf("pattern %q is not on track %q", input.Pattern, input.Track)})
				return
			}
			launchErr = s.transport.launchPattern(input.Track, input.Pattern, quantize)
		} else {
			if input.Entry < 0 || input.Entry >= len(project.Song) || project.Song[input.Entry].Scene != input.Scene {
				studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "song block is no longer in this position"})
				return
			}
			sampleRate, rateErr := s.transport.ensureSampleRate()
			if rateErr != nil {
				s.transport.setError(rateErr)
				studioJSON(w, http.StatusServiceUnavailable, map[string]string{"error": rateErr.Error()})
				return
			}
			prepared, compileErr := compileLiveProjectAtRate(s.path, project, sampleRate)
			if compileErr != nil {
				studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": compileErr.Error()})
				return
			}
			_, fingerprint, hashErr := s.transport.playbackFingerprint()
			if hashErr != nil {
				_ = prepared.Engine.Close()
				studioJSON(w, http.StatusServiceUnavailable, map[string]string{"error": hashErr.Error()})
				return
			}
			launchErr = s.transport.startFrom(input.Entry, input.Scene, &prepared, fingerprint)
		}
		if launchErr != nil {
			s.transport.setError(launchErr)
			studioJSON(w, http.StatusConflict, map[string]string{"error": launchErr.Error()})
			return
		}
	default:
		studioJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown transport action"})
		return
	}
	studioJSON(w, http.StatusOK, s.transport.snapshot())
}

func (s *studio) transportState(w http.ResponseWriter, _ *http.Request) {
	studioJSON(w, http.StatusOK, s.transport.snapshot())
}

func (s *studio) transportSocket(w http.ResponseWriter, r *http.Request) {
	if !studioSameOrigin(r) {
		http.Error(w, "cross-origin transport stream is not allowed", http.StatusForbidden)
		return
	}
	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer connection.CloseNow()
	ctx := connection.CloseRead(context.Background())
	// Send transport snapshot immediately when the WebSocket connects so stopped transports show their real position.
	writeCtx, cancel := context.WithTimeout(ctx, time.Second)
	err = wsjson.Write(writeCtx, connection, s.transport.snapshot())
	cancel()
	if err != nil {
		return
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		writeCtx, cancel := context.WithTimeout(ctx, time.Second)
		err := wsjson.Write(writeCtx, connection, s.transport.snapshot())
		cancel()
		if err != nil {
			return
		}
	}
}
