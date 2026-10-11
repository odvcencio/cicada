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
type previewRenderReference struct {
	clients   [3]previewRenderClient
	sequence  int
	gesture   int
	values    map[int]float32
	retired   map[int]bool
	queue     []int
	committed float32
	offer     float32
	hasOffer  bool
	start     bool
	paused    bool
	closed    bool
	patches   []float32
}

type previewRenderBoundary struct {
	gesture   int
	count     int
	committed float32
	activates bool
	phases    chan bool
	permit    chan struct{}
	done      chan error
}

type previewRenderProducer struct {
	gesture int
	queues  bool
	done    chan struct{}
}

func previewRenderScore(t *testing.T, gain float32) Score {
	t.Helper()
	program := graph.Program{}
	program.Nodes[0] = graph.Node{Op: graph.Constant, Value: 375}
	program.Nodes[1] = graph.Node{Op: graph.Sine, A: 0}
	program.Nodes[2] = graph.Node{Op: graph.Constant, Value: .125}
	program.Nodes[3] = graph.Node{Op: graph.Multiply, A: 1, B: 2}
	program.Len, program.Output = 4, 3
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: 1, BPMMilli: 20_000}
	cfg.Track[0].Kind, cfg.Track[0].Graph = engine.VoiceGraph, program
	cfg.Track[0].GainDB, cfg.Track[0].GainSet = float64(gain), true
	cfg.Track[0].Pan, cfg.Track[0].BusSFX = -1, true
	cfg.Scenes, cfg.Song, cfg.LoopSong = []engine.Scene{{}}, []engine.SongEntry{{Scene: 0, Bars: 1}}, true
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return Score{
		Engine: created, SampleRate: 48_000, BPMMilli: 20_000, Name: "preview",
		Tracks: []TrackSlots{{ID: "bass"}}, HasSFX: true,
		Song:       []SongEntry{{Scene: "preview", StartBar: 1}},
		Parameters: []ParameterValue{{Track: 0, ID: kernel.ParamMixGain, Value: gain}},
	}
}

func runPreviewRenderLifecycle(t *testing.T, operations []previewRenderOperation) (failure error) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		p, err := New(previewRenderScore(t, -6), 48_000)
		if err != nil {
			t.Fatal(err)
		}
		model := previewRenderReference{committed: -6, values: make(map[int]float32), retired: make(map[int]bool)}
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
		applyPatches := func() {
			for _, value := range model.patches {
				model.committed = value
				if gesture := activeGesture(); gesture != 0 && model.values[gesture] == value {
					model.retired[gesture] = true
				}
			}
			model.patches = nil
		}
		begin := func() {
			boundary = &previewRenderBoundary{
				committed: model.committed,
				phases:    make(chan bool), permit: make(chan struct{}), done: make(chan error, 1),
			}
			if model.start {
				if model.hasOffer {
					boundary.committed, boundary.activates = model.offer, true
					model.hasOffer = false
				}
				model.start = false
			}
			captured := boundary
			p.overrideHook = func(snapshotLoaded bool) {
				captured.phases <- snapshotLoaded
				<-captured.permit
			}
			go func() { _, err := p.Read(pcm[:]); captured.done <- err }()
			<-captured.phases // Before the render goroutine counts queued requests.
			if !captured.activates {
				applyPatches()
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
			for block := 0; block < 8; block++ {
				begin()
				for boundary != nil {
					if err := drain(); err != nil {
						return err
					}
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
					failure = p.Offer(previewRenderScore(t, model.offer))
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
						model.patches = append(model.patches, op.value)
					}
				}
			case "offer":
				value := op.value
				if value == model.committed {
					value = -15
					if value == model.committed {
						value = 0
					}
				}
				model.offer, model.hasOffer = value, true
				failure = p.Offer(previewRenderScore(t, value))
			case "activate":
				model.start = true
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
			if runPreviewRenderLifecycle(t, candidate) != nil {
				minimal, changed = candidate, true
				break
			}
		}
	}
	return minimal
}

func TestPreviewRenderLifecycleGeneratedStateMachine(t *testing.T) {
	const seeds, steps = 2048, 36
	kinds := [...]string{"set", "newer-gesture", "cancel", "commit", "close", "drop", "offer", "activate", "render-begin", "render-drain-N", "pause", "resume", "Close", "pressure", "settle", "patch"}
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
		operations = append(operations, previewRenderOperation{kind: "resume"}, previewRenderOperation{kind: "settle"}, previewRenderOperation{kind: "Close"})
		if err := runPreviewRenderLifecycle(t, operations); err != nil {
			t.Fatalf("seed %d: %v; minimized counterexample: %v", seed, err, minimizePreviewRenderLifecycle(t, operations))
		}
	}
	t.Logf("%d seeds, %d random operations with controlled snapshot/drain boundaries, pause/resume and shutdown", seeds, seeds*steps)
}

func TestPreviewRenderLifecycleInterleavingRegressions(t *testing.T) {
	for name, operations := range map[string][]previewRenderOperation{
		"late-cancel": {
			{kind: "render-begin"}, {kind: "set", value: 0}, {kind: "cancel"}, {kind: "render-drain-N"},
			{kind: "offer", value: -3}, {kind: "activate"}, {kind: "settle"},
		},
		"paused-shutdown": {
			{kind: "set", value: 0}, {kind: "pressure"}, {kind: "pause"}, {kind: "close"}, {kind: "Close"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := runPreviewRenderLifecycle(t, operations); err != nil {
				t.Fatalf("%v; minimized counterexample: %v", err, minimizePreviewRenderLifecycle(t, operations))
			}
		})
	}
}
