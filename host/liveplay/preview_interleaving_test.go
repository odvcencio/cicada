package liveplay

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"testing"
	"testing/synctest"
	"time"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
)

func TestPreviewCancelPublishedAfterRenderSnapshotWaitsForNextBlock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p, err := New(meterScore(t, "preview", -6), 48_000)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		loaded, drain, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		p.overrideHook = func(snapshotLoaded bool) {
			if snapshotLoaded {
				close(loaded)
				<-drain
			}
		}
		go func() {
			var pcm [blockFrames * 8]byte
			_, err := p.Read(pcm[:])
			done <- err
		}()
		<-loaded
		version, err := p.SetPreview(0, kernel.ParamMixGain, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.CancelPreview(version); err != nil {
			t.Fatal(err)
		}
		close(drain)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		p.overrideHook = nil
		if len(p.overrideClears) != 1 {
			t.Error("render consumed a cancellation published after its snapshot")
		}
		renderPreview(t, p, blockFrames)
		if _, active := p.OverrideValue(0, kernel.ParamMixGain); active {
			t.Error("cancellation was lost against an older render snapshot")
		}
		if err := p.Offer(meterScore(t, "saved", -3)); err != nil {
			t.Fatal(err)
		}
		renderPreview(t, p, 120_064)
		assertPreviewGain(t, p, -3)
	})
}

func TestPreviewCloseReleasesEveryBlockedCancellationProducer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p, err := New(meterScore(t, "preview", -6), 48_000)
		if err != nil {
			t.Fatal(err)
		}
		version, err := p.SetPreview(0, kernel.ParamMixGain, 0)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < cap(p.overrideClears); i++ {
			if err := p.CancelPreview(version); err != nil {
				t.Fatal(err)
			}
		}
		const producers = 8
		done := make(chan struct{}, producers)
		for i := 0; i < producers; i++ {
			go func() { p.CancelPreviewWait(version); done <- struct{}{} }()
		}
		synctest.Wait() // All eight producers are blocked on the full queue.
		if len(done) != 0 {
			t.Fatal("cancellation did not wait for queue space")
		}
		p.Close()
		p.Close() // Shutdown is idempotent.
		synctest.Wait()
		for i := 0; i < producers; i++ {
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("Close retained a blocked cancellation producer")
				// Release the old implementation's producers so a failing test
				// still leaves no goroutines behind.
				for len(done) < producers-i {
					<-p.overrideClears
					synctest.Wait()
				}
				return
			}
		}
		p.CancelPreviewWait(version) // Calls made after Close also terminate.
	})
}

type previewRenderOperation struct {
	kind   string
	client int
	value  float32
}

func (op previewRenderOperation) String() string {
	return fmt.Sprintf("%s(client=%d,value=%g)", op.kind, op.client, op.value)
}

type previewRenderClient struct {
	open      bool
	gesture   int
	committed bool
}

// Gesture identities and the cancellation ledger belong to the reference
// model; they are independent of Player's version tokens and queue contents.
type previewRenderCounts struct {
	operations, checks, structuralOffers, supersededPatches int
	additions, removals                                     int
	automaticBoundaries, automaticPatched                   int
}

type previewRenderReference struct {
	otherDefaults, otherCommitted [16]float32
	publication, offerPublication int
	tracks, offerTracks           int
	clients                       [3]previewRenderClient
	sequence                      int
	gesture                       int
	values                        map[int]float32
	retired                       map[int]bool
	queue                         []int
	committed                     float32
	offer                         float32
	hasOffer                      bool
	start                         bool
	paused                        bool
	closed                        bool
	patches                       []previewRenderPatch
	defaults                      float32
	seek                          int
	scene                         int
	launch                        bool
	// blocks is the index of the next render block within the song entry's
	// first bar, or -1 when a seek or launch makes the position unmodelled.
	blocks int
}

type previewRenderPatch struct {
	publication int
	tracks      int
	value       float32
	commands    int
}

type previewRenderBoundary struct {
	gesture      int
	count        int
	committed    float32
	activates    bool
	sceneChanged bool
	phases       chan bool
	permit       chan struct{}
	done         chan error
}

type previewRenderProducer struct {
	gesture int
	queues  bool
	done    chan struct{}
}

func previewRenderScore(t *testing.T, gain float32) Score {
	return previewRenderScoreTracks(t, gain, 16)
}

func previewRenderScoreTracks(t *testing.T, gain float32, count int) Score {
	return previewRenderScoreScenes(t, gain, count, false)
}

// previewRenderScoreScenes optionally also authors -9 dB in the first scene,
// so song start and seeks to bar one reconstruct a scene setting.
func previewRenderScoreScenes(t *testing.T, gain float32, count int, firstScene bool) Score {
	t.Helper()
	program := graph.Program{}
	program.Nodes[0] = graph.Node{Op: graph.Constant, Value: 375}
	program.Nodes[1] = graph.Node{Op: graph.Sine, A: 0}
	program.Nodes[2] = graph.Node{Op: graph.Constant, Value: .125}
	program.Nodes[3] = graph.Node{Op: graph.Multiply, A: 1, B: 2}
	program.Len, program.Output = 4, 3
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: blockFrames, Tracks: count, MaxVoices: count, BPMMilli: 20_000}
	cfg.Track[0].Kind, cfg.Track[0].Graph = engine.VoiceGraph, program
	cfg.Track[0].GainDB, cfg.Track[0].GainSet = float64(gain), true
	cfg.Track[0].Pan, cfg.Track[0].BusSFX = -1, true
	for i := 1; i < count; i++ {
		cfg.Track[i] = cfg.Track[0]
		cfg.Track[i].Graph.Nodes[2].Value = 0
	}
	cfg.Scenes = []engine.Scene{{}, {Settings: []engine.SceneSetting{{Track: 0, ID: kernel.ParamMixGain, Value: -9}}}, {}}
	if firstScene {
		cfg.Scenes[0] = cfg.Scenes[1]
	}
	cfg.Song = []engine.SongEntry{{Scene: 0, Bars: 1}, {Scene: 1, Bars: 1}, {Scene: 2, Bars: 1}}
	cfg.LoopSong = true
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	tracks := make([]TrackSlots, count)
	for i := range tracks {
		tracks[i].ID = fmt.Sprintf("extra%d", i)
	}
	tracks[0].ID = "bass"
	parameters := make([]ParameterValue, 0, 4*count)
	for track := range tracks {
		parameters = append(parameters, ParameterValue{Track: uint8(track), ID: kernel.ParamMixGain, Value: gain},
			ParameterValue{Track: uint8(track), ID: kernel.ParamMixPan, Value: -1},
			ParameterValue{Track: uint8(track), ID: kernel.ParamMixSendA, Value: 0},
			ParameterValue{Track: uint8(track), ID: kernel.ParamMixSendB, Value: 0})
	}
	sceneParameters := [][]ParameterValue{nil, {{Track: 0, ID: kernel.ParamMixGain, Value: -9}}, nil}
	if firstScene {
		sceneParameters[0] = sceneParameters[1]
	}
	return Score{
		Engine: created, SampleRate: 48_000, BPMMilli: 20_000, Name: "preview",
		Tracks: tracks, HasSFX: true, SceneIDs: []string{"preview", "later", "last"},
		SceneParameters: sceneParameters,
		Song:            []SongEntry{{Scene: "preview", StartBar: 1}, {Scene: "later", StartBar: 2}, {Scene: "last", StartBar: 3}},
		Parameters:      parameters,
	}
}

func runPreviewRenderLifecycle(t *testing.T, operations []previewRenderOperation, counts *previewRenderCounts) (failure error) {
	t.Helper()
	if counts == nil {
		counts = new(previewRenderCounts)
	}
	synctest.Test(t, func(t *testing.T) {
		p, err := New(previewRenderScore(t, -6), 48_000)
		if err != nil {
			t.Fatal(err)
		}
		model := previewRenderReference{tracks: 16, offerTracks: 16, committed: -6, defaults: -6, blocks: 0, values: make(map[int]float32), retired: make(map[int]bool)}
		for track := range model.otherDefaults {
			model.otherDefaults[track], model.otherCommitted[track] = -6, -6
		}
		var versions = make(map[int]uint64)
		var boundary *previewRenderBoundary
		var producers []previewRenderProducer
		var pcm [blockFrames * 8]byte
		arena := NewPatchArena(32)
		patchRevision := uint64(0)
		for i := range model.clients {
			model.clients[i].open = true
		}
		// Failed runs also join every reader/producer, permitting deterministic
		// counterexample minimization without retaining players or engines.
		defer func() {
			if boundary != nil {
				for {
					boundary.permit <- struct{}{}
					select {
					case <-boundary.done:
						goto readerJoined
					case <-boundary.phases:
					}
				}
			}
		readerJoined:
			p.Close()
			for _, producer := range producers {
				for {
					synctest.Wait()
					select {
					case <-producer.done:
						goto joined
					default:
						<-p.overrideClears // Only needed to clean up a failing old implementation.
					}
				}
			joined:
			}
		}()
		activeGesture := func() int {
			if model.retired[model.gesture] {
				return 0
			}
			return model.gesture
		}
		collectProducers := func() {
			synctest.Wait()
			pending := producers[:0]
			for _, producer := range producers {
				select {
				case <-producer.done:
					if producer.queues && !model.closed {
						model.queue = append(model.queue, producer.gesture)
					}
				default:
					pending = append(pending, producer)
				}
			}
			producers = pending
		}
		applyPatches := func() bool {
			applied := false
			consumed, commands := 0, 0
			// A block applies up to 128 commands, less what the engine queue must
			// keep for launches, transport, notes (renderCommandReserve), queued
			// cancellations, and an active preview with its pending reapply.
			spare := engine.CommandCapacity - renderCommandReserve - len(model.queue)
			if activeGesture() != 0 {
				spare -= 2
			}
			patchBudget := min(2*patchCapacity, spare)
			for _, patch := range model.patches {
				if model.hasOffer && patch.publication > model.offerPublication {
					break
				}
				if commands+patch.commands > patchBudget {
					break
				}
				commands += patch.commands
				consumed++
				if patch.tracks > model.tracks {
					continue
				}
				applied = true
				value := patch.value
				model.committed, model.defaults = value, value
				for track := 1; track < patch.tracks; track++ {
					model.otherDefaults[track], model.otherCommitted[track] = value, value
				}
				if gesture := activeGesture(); gesture != 0 && model.values[gesture] == value {
					model.retired[gesture] = true
				}
			}
			model.patches = model.patches[consumed:]
			return applied
		}
		begin := func() {
			boundary = &previewRenderBoundary{
				committed: model.committed,
				phases:    make(chan bool), permit: make(chan struct{}), done: make(chan error, 1),
			}
			if model.start {
				model.scene = model.seek
				model.otherCommitted = model.otherDefaults
				model.committed = model.defaults
				if model.scene > 0 {
					model.committed = -9
				}
				if model.hasOffer {
					boundary.committed, boundary.activates = model.offer, true
					if model.seek > 0 {
						boundary.committed = -9
					}
					model.hasOffer = false
					if model.offerTracks > model.tracks {
						counts.additions++
					} else if model.offerTracks < model.tracks {
						counts.removals++
					}
					model.tracks = model.offerTracks
					kept := model.patches[:0]
					for _, patch := range model.patches {
						if patch.publication > model.offerPublication {
							kept = append(kept, patch)
						} else {
							counts.supersededPatches++
						}
					}
					model.patches = kept
					model.defaults = model.offer
					for track := range model.otherDefaults {
						model.otherDefaults[track], model.otherCommitted[track] = model.offer, model.offer
					}
				}
				model.start = false
				model.blocks = -1
				if model.seek == 0 {
					model.blocks = 0
				}
			}
			// The song enters scene 1 on the first frame of bar 2, after the
			// render thread has drained commands. A patch that lands in this
			// block runs first, so the scene value wins.
			automatic := model.blocks == previewBarBlocks && model.scene == 0 && !boundary.activates
			if model.blocks >= 0 {
				model.blocks++
			}
			launched := model.launch && !boundary.activates
			boundary.sceneChanged = launched && len(model.patches) == 0
			if launched {
				model.scene, model.committed = 1, -9
			}
			model.launch = false
			captured := boundary
			p.overrideHook = func(snapshotLoaded bool) {
				captured.phases <- snapshotLoaded
				<-captured.permit
			}
			go func() { _, err := p.Read(pcm[:]); captured.done <- err }()
			select {
			case <-captured.phases: // Before the render goroutine counts queued requests.
			case err := <-captured.done:
				failure = fmt.Errorf("admitted patches faulted before render boundary: %v", err)
				boundary, p.overrideHook = nil, nil
				return
			}
			if !captured.activates {
				patched := applyPatches()
				captured.sceneChanged = launched && !patched
				if launched && model.committed == -9 {
					if gesture := activeGesture(); gesture != 0 && model.values[gesture] == -9 {
						model.retired[gesture] = true
					}
				}
				if automatic {
					counts.automaticBoundaries++
					if patched {
						counts.automaticPatched++
					}
					captured.sceneChanged, model.scene, model.committed = true, 1, -9
					if gesture := activeGesture(); gesture != 0 && model.values[gesture] == -9 {
						model.retired[gesture] = true
					}
				}
				captured.committed = model.committed
			}
			collectProducers()
			captured.gesture, captured.count = activeGesture(), len(model.queue)
			captured.permit <- struct{}{}
			<-captured.phases // The immutable snapshot has now been loaded.
		}
		applyReference := func(gesture, count int, committed float32, newEngine bool) {
			for _, canceled := range model.queue[:count] {
				if canceled == gesture {
					model.retired[gesture] = true
				}
			}
			model.queue = model.queue[count:]
			if newEngine && gesture != 0 && model.values[gesture] == committed {
				model.retired[gesture] = true
			}
		}
		drain := func() error {
			captured := boundary
			captured.permit <- struct{}{}
			applyReference(captured.gesture, captured.count, captured.committed, captured.activates)
			if captured.activates {
				model.committed = captured.committed
			}
			select {
			case err := <-captured.done:
				boundary, p.overrideHook = nil, nil
				if err != nil {
					return err
				}
				collectProducers()
				if captured.sceneChanged && captured.committed == -9 {
					if gesture := activeGesture(); gesture != 0 && model.values[gesture] == -9 {
						model.retired[gesture] = true
					}
				}
			case <-captured.phases:
				// Activation has a second boundary before the first PCM block.
				// Stop it before counting, publish any woken producers, then load
				// its snapshot. Client operations can interleave here too.
				collectProducers()
				applyPatches()
				captured.gesture, captured.count = activeGesture(), len(model.queue)
				captured.committed, captured.activates = model.committed, false
				captured.permit <- struct{}{}
				<-captured.phases
			}
			if len(p.overrideClears) != len(model.queue) {
				return fmt.Errorf("cancel lost: queued %d, reference ledger %d (boundary drained %d)", len(p.overrideClears), len(model.queue), captured.count)
			}
			wantGesture := activeGesture()
			value, active := p.OverrideValue(0, kernel.ParamMixGain)
			if active != (wantGesture != 0) || active && value != model.values[wantGesture] {
				return fmt.Errorf("override %g/%v, reference gesture %d value %g", value, active, wantGesture, model.values[wantGesture])
			}
			if committed, ok := p.CommittedValue(0, kernel.ParamMixGain); !ok || committed != model.committed {
				return fmt.Errorf("committed %g/%v, reference %g", committed, ok, model.committed)
			}
			// Invariant (n): all modeled controls, including reconstruction bases,
			// agree with the latest publication that has actually landed.
			names := p.trackNames.Load()
			for track := 0; track < model.tracks; track++ {
				base, committed := model.otherDefaults[track], model.otherCommitted[track]
				if track == 0 {
					base, committed = model.defaults, model.committed
				}
				for _, control := range []struct {
					id              kernel.ParamID
					base, committed float32
				}{
					{kernel.ParamMixGain, base, committed}, {kernel.ParamMixPan, -1, -1},
					{kernel.ParamMixSendA, 0, 0}, {kernel.ParamMixSendB, 0, 0},
				} {
					bits := names.defaults[track][control.id]
					if bits>>32 == 0 || math.Float32frombits(uint32(bits)) != control.base {
						return fmt.Errorf("track %d reconstruction control %d differs from newest landed publication: got %g, want %g", track, control.id, math.Float32frombits(uint32(bits)), control.base)
					}
					if value, ok := names.committedValue(uint8(track), control.id); !ok || value != control.committed {
						return fmt.Errorf("track %d committed control %d got %g/%v, want %g", track, control.id, value, ok, control.committed)
					}
					counts.checks++
				}
			}
			return nil
		}
		settle := func() error {
			for boundary != nil {
				if err := drain(); err != nil {
					return err
				}
			}
			if model.paused || model.closed {
				return nil
			}
			// Eight explicitly scheduled blocks settle gain smoothing and any
			// pending commit/activation or next-block cancellation. Never sleep.
			for settled := 0; settled < 8; {
				begin()
				if failure != nil {
					return failure
				}
				for boundary != nil {
					if err := drain(); err != nil {
						return err
					}
				}
				if len(model.patches) == 0 || model.hasOffer && model.patches[0].publication > model.offerPublication {
					settled++
				} else {
					settled = 0
				}
			}
			want := model.committed
			if gesture := activeGesture(); gesture != 0 {
				ownerOpen := false
				for _, client := range model.clients {
					ownerOpen = ownerOpen || client.open && !client.committed && client.gesture == gesture
				}
				if !ownerOpen {
					return fmt.Errorf("uncommitted gesture %d retained after its owner closed", gesture)
				}
				want = model.values[gesture]
			}
			peak := float64(0)
			for frame := 0; frame < blockFrames; frame++ {
				peak = max(peak, math.Abs(float64(math.Float32frombits(binary.LittleEndian.Uint32(pcm[frame*8:])))))
			}
			if wantPeak := .125 * math.Pow(10, float64(want)/20); math.Abs(peak-wantPeak) > .002 {
				return fmt.Errorf("audible peak %g, want %g (%g dB)", peak, wantPeak, want)
			}
			return nil
		}
		for step, op := range operations {
			if model.closed {
				continue
			}
			counts.operations++
			client := &model.clients[op.client]
			switch op.kind {
			case "set", "newer-gesture":
				version, err := p.SetPreview(0, kernel.ParamMixGain, op.value)
				if err != nil {
					failure = err
					return
				}
				model.sequence++
				model.gesture = model.sequence
				model.values[model.gesture], versions[model.gesture] = op.value, version
				*client = previewRenderClient{open: true, gesture: model.gesture}
			case "cancel":
				if client.open && client.gesture != 0 {
					queues := activeGesture() == client.gesture
					if err := p.CancelPreview(versions[client.gesture]); err == nil {
						if queues {
							model.queue = append(model.queue, client.gesture)
						}
						client.gesture = 0
					} else if len(model.queue) != cap(p.overrideClears) {
						failure = err
					}
				}
			case "commit":
				// Preserve the existing model's completed save/activation handoff.
				// Pending committed previews during pause/disconnect are covered by
				// the socket lifecycle tests; this model interleaves their cancels.
				if client.open && client.gesture != 0 && !model.paused {
					client.committed = true
					model.offer, model.hasOffer, model.start = model.values[client.gesture], true, true
					model.seek = 0
					model.publication++
					model.offerPublication, model.offerTracks = model.publication, model.tracks
					failure = p.Offer(previewRenderScoreTracks(t, model.offer, model.offerTracks))
					if failure == nil {
						failure = p.StartSongEntry(0, "preview")
					}
					if failure == nil {
						failure = settle()
					}
				}
			case "close", "drop":
				if client.open && !client.committed && client.gesture != 0 {
					gesture := client.gesture
					producer := previewRenderProducer{gesture: gesture, queues: activeGesture() == gesture, done: make(chan struct{})}
					go func() { p.CancelPreviewWait(versions[gesture]); close(producer.done) }()
					producers = append(producers, producer)
				}
				client.open, client.gesture = false, 0
				collectProducers()
			case "patch":
				patchRevision++
				batch := arena.Begin(patchRevision)
				if batch != nil {
					target, err := p.ResolvePreviewTrack("bass")
					if err != nil {
						failure = err
						batch.Release()
						break
					}
					batch.SetResolvedParam(target, kernel.ParamMixGain, op.value)
					failure = p.Patch(batch)
					if failure == nil {
						model.publication++
						model.patches = append(model.patches, previewRenderPatch{publication: model.publication, value: op.value, commands: 1})
					}
				}
			case "burst":
				for n := 0; n < 9; n++ {
					patchRevision++
					batch := arena.Begin(patchRevision)
					if batch == nil {
						break
					}
					for track := uint8(0); track < p.trackNames.Load().count; track++ {
						value := float32(-1)
						batch.SetParam(track, kernel.ParamMixPan, value)
						batch.SetParam(track, kernel.ParamMixSendA, 0)
						batch.SetParam(track, kernel.ParamMixSendB, 0)
						batch.SetParam(track, kernel.ParamMixGain, op.value)
					}
					if err := p.Patch(batch); err == nil {
						model.publication++
						model.patches = append(model.patches, previewRenderPatch{publication: model.publication, tracks: int(p.trackNames.Load().count), value: op.value, commands: int(p.trackNames.Load().count) * 4})
					} else {
						break
					}
				}
			case "seek-forward", "seek-backward", "seek-middle":
				model.seek = map[string]int{"seek-forward": 2, "seek-backward": 0, "seek-middle": 1}[op.kind]
				model.start = true
				failure = p.StartSongEntry(model.seek, []string{"preview", "later", "last"}[model.seek])
			case "scene":
				for boundary != nil {
					failure = drain()
					if failure != nil {
						break
					}
				}
				model.scene, model.launch = 1, true
				if !p.current.Engine.Push(cmd.Command{Op: cmd.OpLaunchScene, Track: 0xff, Index: 1}) {
					failure = fmt.Errorf("scene command rejected")
				}
			case "offer", "offer-add", "offer-remove":
				if op.kind != "offer" {
					counts.structuralOffers++
				}
				value := op.value
				if value == model.committed {
					value = -15
					if value == model.committed {
						value = 0
					}
				}
				model.offer, model.hasOffer = value, true
				model.publication++
				model.offerPublication, model.offerTracks = model.publication, model.tracks
				if op.kind == "offer-add" {
					model.offerTracks = 16
				}
				if op.kind == "offer-remove" {
					model.offerTracks = 15
				}
				failure = p.Offer(previewRenderScoreTracks(t, value, model.offerTracks))
			case "activate":
				model.start, model.seek = true, 0
				failure = p.StartSongEntry(0, "preview")
			case "render-begin":
				if boundary == nil && !model.paused {
					begin()
				}
			case "render-drain-N":
				if boundary != nil {
					failure = drain()
				}
			case "pause", "resume", "Close":
				for boundary != nil {
					failure = drain() // Audio pause/stop first joins the in-flight block.
					if failure != nil {
						break
					}
				}
				if op.kind == "Close" {
					p.Close()
					model.closed = true // No engine plays after permanent shutdown.
					collectProducers()
					for _, producer := range producers {
						select {
						case <-producer.done:
						case <-time.After(time.Second):
							failure = fmt.Errorf("Close retained a cancellation producer")
						}
					}
				} else {
					model.paused = op.kind == "pause"
					p.SetTransportPlaying(!model.paused)
				}
			case "pressure":
				if gesture := activeGesture(); gesture != 0 {
					for len(model.queue) < cap(p.overrideClears) {
						if err := p.CancelPreview(versions[gesture]); err != nil {
							failure = err
							break
						}
						model.queue = append(model.queue, gesture)
					}
				}
			case "settle":
				failure = settle()
			case "approach", "approach-boundary":
				// Render real blocks until the next one is the last block of bar
				// one (approach) or the first block of bar two (approach-boundary).
				for boundary != nil && failure == nil {
					failure = drain()
				}
				if failure == nil && (model.paused || model.blocks < 0 || model.scene != 0 || model.hasOffer || model.start) {
					failure = fmt.Errorf("automatic boundary is unmodelled: blocks %d scene %d", model.blocks, model.scene)
				}
				target := previewBarBlocks - 1
				if op.kind == "approach-boundary" {
					target = previewBarBlocks
				}
				for failure == nil && model.blocks < target {
					if begin(); failure == nil && boundary != nil {
						failure = drain()
					}
				}
			default:
				failure = fmt.Errorf("unknown operation %q", op.kind)
			}
			if failure != nil {
				failure = fmt.Errorf("step %d %v: %w", step, op, failure)
				return
			}
		}
	})
	return failure
}

func minimizePreviewRenderLifecycle(t *testing.T, operations []previewRenderOperation) []previewRenderOperation {
	t.Helper()
	minimal := append([]previewRenderOperation(nil), operations...)
	for changed := true; changed; {
		changed = false
		for i := range minimal {
			candidate := append([]previewRenderOperation(nil), minimal[:i]...)
			candidate = append(candidate, minimal[i+1:]...)
			if runPreviewRenderLifecycle(t, candidate, nil) != nil {
				minimal, changed = candidate, true
				break
			}
		}
	}
	return minimal
}

func TestPreviewRenderLifecycleGeneratedStateMachine(t *testing.T) {
	const seeds, steps = 2048, 36
	var counts previewRenderCounts
	kinds := [...]string{"set", "newer-gesture", "cancel", "commit", "close", "drop", "offer", "activate", "render-begin", "render-drain-N", "pause", "resume", "Close", "pressure", "settle", "patch", "seek-forward", "seek-backward", "scene", "burst"}
	values := [...]float32{-12, -9, -6, -3, 0}
	for seed := int64(0); seed < seeds; seed++ {
		random := rand.New(rand.NewSource(seed))
		operations := make([]previewRenderOperation, 0, steps+6)
		lastClient := -1
		for step := 0; step < steps; step++ {
			op := previewRenderOperation{kind: kinds[random.Intn(len(kinds))], client: random.Intn(3), value: values[random.Intn(len(values))]}
			if op.kind == "newer-gesture" && lastClient >= 0 {
				op.client = (lastClient + 1 + random.Intn(2)) % 3
			}
			if op.kind == "set" || op.kind == "newer-gesture" {
				lastClient = op.client
			}
			if op.kind == "Close" && step < steps*2/3 {
				op.kind = "render-begin" // Exercise interleavings before permanent shutdown.
			}
			operations = append(operations, op)
		}
		for client := 0; client < 3; client++ {
			operations = append(operations, previewRenderOperation{kind: "close", client: client})
		}
		operations = append(operations, previewRenderOperation{kind: "resume"}, previewRenderOperation{kind: "settle"})
		// Keep the M2/M3 random lifecycle stream stable and generate additional
		// publication schedules after its owners have finished their gestures.
		kind, secondKind := "offer-add", "offer-remove"
		if seed%2 == 0 {
			kind, secondKind = secondKind, kind
		}
		operations = append(operations,
			previewRenderOperation{kind: "pause"}, previewRenderOperation{kind: "burst", value: values[seed%5]},
			previewRenderOperation{kind: kind, value: values[(seed+1)%5]},
			previewRenderOperation{kind: "patch", value: values[(seed+2)%5]},
			previewRenderOperation{kind: "activate"}, previewRenderOperation{kind: "resume"}, previewRenderOperation{kind: "settle"},
			previewRenderOperation{kind: "burst", value: values[(seed+3)%5]},
			previewRenderOperation{kind: "render-begin"}, previewRenderOperation{kind: "render-drain-N"},
			previewRenderOperation{kind: secondKind, value: values[(seed+4)%5]}, previewRenderOperation{kind: "activate"},
			previewRenderOperation{kind: "settle"}, previewRenderOperation{kind: "seek-forward"}, previewRenderOperation{kind: "settle"},
			previewRenderOperation{kind: "seek-backward"}, previewRenderOperation{kind: "settle"})
		// Seeking into the scene that sets gain: a patch drained in the same
		// block follows the reconstruction, whether it was published before or
		// after the seek request, with or without a pending score.
		operations = append(operations,
			previewRenderOperation{kind: "patch", value: values[(seed+1)%5]}, previewRenderOperation{kind: "seek-middle"}, previewRenderOperation{kind: "settle"},
			previewRenderOperation{kind: "seek-backward"}, previewRenderOperation{kind: "settle"},
			previewRenderOperation{kind: "seek-middle"}, previewRenderOperation{kind: "patch", value: values[(seed+3)%5]}, previewRenderOperation{kind: "settle"},
			previewRenderOperation{kind: "seek-backward"}, previewRenderOperation{kind: "settle"},
			previewRenderOperation{kind: "offer", value: values[(seed+2)%5]}, previewRenderOperation{kind: "patch", value: values[(seed+4)%5]},
			previewRenderOperation{kind: "seek-middle"}, previewRenderOperation{kind: "settle"},
			previewRenderOperation{kind: "seek-backward"}, previewRenderOperation{kind: "settle"})
		// A 1-in-16 sample also crosses the automatic bar-two boundary with a
		// patch before it, in it, or after it, and with or without a preview.
		if seed%16 == 0 {
			variant := (seed / 16) % 3
			if (seed/48)%2 == 0 {
				operations = append(operations, previewRenderOperation{kind: "set", client: 0, value: values[(seed/96)%5]})
			}
			patch := previewRenderOperation{kind: "patch", value: values[(seed+2)%5]}
			switch variant {
			case 0:
				operations = append(operations, previewRenderOperation{kind: "approach"}, patch, previewRenderOperation{kind: "settle"})
			case 1:
				operations = append(operations, previewRenderOperation{kind: "approach-boundary"}, patch, previewRenderOperation{kind: "settle"})
			default:
				operations = append(operations, previewRenderOperation{kind: "approach-boundary"},
					previewRenderOperation{kind: "render-begin"}, previewRenderOperation{kind: "render-drain-N"}, patch, previewRenderOperation{kind: "settle"})
			}
			operations = append(operations, previewRenderOperation{kind: "cancel", client: 0}, previewRenderOperation{kind: "settle"})
		}
		operations = append(operations, previewRenderOperation{kind: "Close"})
		if err := runPreviewRenderLifecycle(t, operations, &counts); err != nil {
			t.Fatalf("seed %d: %v; minimized counterexample: %v", seed, err, minimizePreviewRenderLifecycle(t, operations))
		}
	}
	if counts.automaticBoundaries == 0 || counts.automaticPatched == 0 {
		t.Fatalf("no automatic boundary crossed with a patch: %d boundaries, %d patched", counts.automaticBoundaries, counts.automaticPatched)
	}
	if counts.additions == 0 || counts.removals == 0 {
		t.Fatal("publication schedules did not add and remove tracks")
	}
	t.Logf("automatic boundaries %d, with a same-block patch %d", counts.automaticBoundaries, counts.automaticPatched)
	t.Logf("landed additions %d, removals %d", counts.additions, counts.removals)
	t.Logf("executed operations %d, parameter/default checks %d, structural offers %d, superseded batches %d", counts.operations, counts.checks, counts.structuralOffers, counts.supersededPatches)
	t.Logf("%d seeds, %d random operations with controlled snapshot/drain boundaries, pause/resume and shutdown", seeds, seeds*steps)
}

func TestPreviewRenderLifecycleInterleavingRegressions(t *testing.T) {
	for name, operations := range map[string][]previewRenderOperation{
		"older-structural":      {{kind: "patch", value: -3}, {kind: "offer-add", value: -12}, {kind: "activate"}, {kind: "settle"}, {kind: "seek-backward"}, {kind: "settle"}},
		"deferred-structural":   {{kind: "burst", value: -3}, {kind: "render-begin"}, {kind: "render-drain-N"}, {kind: "offer-remove", value: -12}, {kind: "patch", value: 0}, {kind: "activate"}, {kind: "settle"}, {kind: "seek-backward"}, {kind: "settle"}},
		"later-waits-for-offer": {{kind: "offer-remove", value: -12}, {kind: "patch", value: -3}, {kind: "settle"}, {kind: "activate"}, {kind: "settle"}},
		"patch-seek":            {{kind: "patch", value: -3}, {kind: "settle"}, {kind: "seek-backward"}, {kind: "settle"}},
		"backward-cancel":       {{kind: "seek-forward"}, {kind: "settle"}, {kind: "seek-backward"}, {kind: "set", value: 0}, {kind: "cancel"}, {kind: "settle"}},
		"paused-burst":          {{kind: "pause"}, {kind: "burst", value: -3}, {kind: "resume"}, {kind: "settle"}},
		"late-cancel": {
			{kind: "render-begin"}, {kind: "set", value: 0}, {kind: "cancel"}, {kind: "render-drain-N"},
			{kind: "offer", value: -3}, {kind: "activate"}, {kind: "settle"},
		},
		"paused-shutdown": {
			{kind: "set", value: 0}, {kind: "pressure"}, {kind: "pause"}, {kind: "close"}, {kind: "Close"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := runPreviewRenderLifecycle(t, operations, nil); err != nil {
				t.Fatalf("%v; minimized counterexample: %v", err, minimizePreviewRenderLifecycle(t, operations))
			}
		})
	}
}
