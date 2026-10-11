package main

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
	"testing/synctest"
	"time"

	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
)

func TestPreviewPausedThenStoppedReleasesSocketTeardown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p, err := liveplay.New(studioPreviewScore(t, -6), 48_000)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		s := &studio{transport: &studioTransport{stream: p, telemetry: liveplay.NewPublisher(), sampleRate: 48_000}}
		session := make(livePreviewSession)
		if err := s.handleLiveMessage([]byte(`{"type":"preview-set","entity":"track:bass","param":"mix.gain","value":0}`), session); err != nil {
			t.Fatal(err)
		}
		version := session[livePreviewKey{entity: "track:bass", param: kernel.ParamMixGain}].version
		for p.CancelPreview(version) == nil {
		}
		s.transport.pause()
		done := make(chan struct{})
		go func() { session.close(); close(done) }()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("paused teardown discarded a cancellation from a full queue")
		default:
		}
		s.transport.stop()
		synctest.Wait()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("pause then stop retained the socket reader and its player/engine")
			// Let an unfixed teardown finish without leaking this test's reader.
			renderStudioPreview(t, p, 256)
			<-done
		}
		if s.transport.stream != nil || len(session) != 0 {
			t.Fatal("stopped socket teardown retained its player")
		}
	})
}

func TestPreviewTeardownKeepsCommittedVersionsAndNewerGestures(t *testing.T) {
	for _, scenario := range []string{"committed", "newer-client", "new-gesture-after-commit"} {
		t.Run(scenario, func(t *testing.T) {
			p, err := liveplay.New(studioPreviewScore(t, -6), 48_000)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			s := &studio{transport: &studioTransport{stream: p}}
			a, b := make(livePreviewSession), make(livePreviewSession)
			message := func(session livePreviewSession, data string) {
				t.Helper()
				if err := s.handleLiveMessage([]byte(data), session); err != nil {
					t.Fatal(err)
				}
			}
			message(a, `{"type":"preview-set","entity":"track:bass","param":"mix.gain","value":0}`)
			if scenario != "newer-client" {
				message(a, `{"type":"preview-end","entity":"track:bass","param":"mix.gain","commit":true}`)
			}
			if scenario == "newer-client" {
				message(b, `{"type":"preview-set","entity":"track:bass","param":"mix.gain","value":-9}`)
			}
			if scenario == "new-gesture-after-commit" {
				message(a, `{"type":"preview-set","entity":"track:bass","param":"mix.gain","value":-9}`)
			}
			a.close()
			renderStudioPreview(t, p, 24_064)
			switch scenario {
			case "committed":
				assertStudioPreviewGain(t, p, 0)
				if value, active := p.OverrideValue(0, kernel.ParamMixGain); !active || value != 0 {
					t.Fatal("disconnect canceled a committed preview before its save activated")
				}
				if err := p.Offer(studioPreviewScore(t, 0)); err != nil {
					t.Fatal(err)
				}
				renderStudioPreview(t, p, 120_064)
				assertStudioPreviewGain(t, p, 0)
				if _, active := p.OverrideValue(0, kernel.ParamMixGain); active {
					t.Fatal("committed preview did not retire after matching activation")
				}
			case "newer-client":
				assertStudioPreviewGain(t, p, -9)
				if value, active := p.OverrideValue(0, kernel.ParamMixGain); !active || value != -9 {
					t.Fatal("older socket teardown canceled a newer gesture")
				}
				b.close()
				renderStudioPreview(t, p, 24_064)
				assertStudioPreviewGain(t, p, -6)
			case "new-gesture-after-commit":
				assertStudioPreviewGain(t, p, -6)
				if _, active := p.OverrideValue(0, kernel.ParamMixGain); active {
					t.Fatal("a previous commit exempted the next uncommitted gesture from cleanup")
				}
			}
		})
	}
}

func TestPreviewTeardownWaitsForCancellationQueue(t *testing.T) {
	p, err := liveplay.New(studioPreviewScore(t, -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	noise, err := p.SetPreview(0, kernel.ParamMixGain, -3)
	if err != nil {
		t.Fatal(err)
	}
	for count := 0; ; count++ {
		if err := p.CancelPreview(noise); err != nil {
			if count == 0 {
				t.Fatal(err)
			}
			break
		}
		if count > 1024 {
			t.Fatal("cancellation queue did not fill")
		}
	}
	s := &studio{transport: &studioTransport{stream: p}}
	session := make(livePreviewSession)
	if err := s.handleLiveMessage([]byte(`{"type":"preview-set","entity":"track:bass","param":"mix.gain","value":0}`), session); err != nil {
		t.Fatal(err)
	}
	started, done := make(chan struct{}), make(chan struct{})
	go func() {
		close(started)
		session.close()
		close(done)
	}()
	<-started
	select {
	case <-done:
		t.Fatal("teardown discarded a cancellation when the queue was full")
	case <-time.After(10 * time.Millisecond):
	}
	// The render thread drains the older requests and never waits for teardown.
	renderStudioPreview(t, p, 256)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("teardown did not queue its cancellation after space became available")
	}
	renderStudioPreview(t, p, 24_064)
	assertStudioPreviewGain(t, p, -6)
	if _, active := p.OverrideValue(0, kernel.ParamMixGain); active {
		t.Fatal("teardown lost its cancellation under queue pressure")
	}
}

// Track identities stay fixed while each offered score has its own ordering.
var previewTrackIDs = [...]string{"bass", "lead", "pad", "drums"}

type previewOperation struct {
	kind   string
	client int
	track  int
	value  float32
}

func (op previewOperation) String() string {
	return fmt.Sprintf("%s(client=%d,track=%s,value=%g)", op.kind, op.client, previewTrackIDs[op.track], op.value)
}

type previewReferenceGesture struct {
	version   int
	value     float32
	committed bool
}

type previewReferencePending struct {
	target             liveplay.PreviewTarget
	track, incarnation int
	value              float32
	valid              bool
}

type previewReferenceClient struct {
	open     bool
	gestures [4]previewReferenceGesture
	pending  previewReferencePending
}

type previewReferenceOverride struct {
	incarnation int
	owner       int
	gesture     int
	value       float32
	active      bool
}

type previewReferenceScore struct {
	incarnations [4]int
	order        []int
	gains        [4]float32
}

type previewLifecycleCounts struct {
	checks, sets, cancels, teardowns, activations, reorders, adds, removes, shifts int
	captures, publications, stalePublications, readdedPublications, readds         int
}

func previewLifecycleScore(t *testing.T, score previewReferenceScore) liveplay.Score {
	t.Helper()
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: 256, Tracks: len(score.order), MaxVoices: len(score.order), BPMMilli: 20_000}
	tracks := make([]liveplay.TrackSlots, len(score.order))
	parameters := make([]liveplay.ParameterValue, len(score.order))
	for index, identity := range score.order {
		program := graph.Program{}
		program.Nodes[0] = graph.Node{Op: graph.Constant, Value: float32(375 * (identity + 1))}
		program.Nodes[1] = graph.Node{Op: graph.Sine, A: 0}
		program.Nodes[2] = graph.Node{Op: graph.Constant, Value: .125}
		program.Nodes[3] = graph.Node{Op: graph.Multiply, A: 1, B: 2}
		program.Len, program.Output = 4, 3
		cfg.Track[index].Kind, cfg.Track[index].Graph = engine.VoiceGraph, program
		cfg.Track[index].GainDB, cfg.Track[index].GainSet = float64(score.gains[identity]), true
		cfg.Track[index].Pan, cfg.Track[index].BusSFX = -1, true
		tracks[index].ID = previewTrackIDs[identity]
		parameters[index] = liveplay.ParameterValue{Track: uint8(index), ID: kernel.ParamMixGain, Value: score.gains[identity]}
	}
	// A slow clock keeps offers pending until explicit song-start activation.
	cfg.Scenes = []engine.Scene{{}}
	cfg.Song = []engine.SongEntry{{Scene: 0, Bars: 1}}
	cfg.LoopSong = true
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// This engine has not been handed to a player yet. Emit per-track meters
	// every block so the final frame measures each identity after smoothing.
	if !created.Push(cmd.Command{Op: cmd.OpMeterRate, Track: 0xff, Arg0: 1}) {
		t.Fatal("meter rate publication failed")
	}
	return liveplay.Score{
		Engine: created, SampleRate: 48_000, BPMMilli: 20_000, Name: "preview",
		Tracks: tracks, HasSFX: true,
		Song: []liveplay.SongEntry{{Scene: "preview", StartBar: 1}}, Parameters: parameters,
	}
}

func runPreviewLifecycle(t *testing.T, operations []previewOperation, counts *previewLifecycleCounts) error {
	t.Helper()
	if counts == nil {
		counts = new(previewLifecycleCounts)
	}
	playing := previewReferenceScore{order: []int{0, 1, 2}, gains: [4]float32{-6, -12, -9, -3}, incarnations: [4]int{1, 2, 3, 0}}
	p, err := liveplay.New(previewLifecycleScore(t, playing), 48_000)
	if err != nil {
		return err
	}
	defer p.Close()
	s := &studio{transport: &studioTransport{stream: p}}
	var sessions [3]livePreviewSession
	var clients [3]previewReferenceClient
	var overrides [4]previewReferenceOverride
	sequence := 0
	incarnation := 3
	var offer previewReferenceScore
	hasOffer := false
	for i := range sessions {
		sessions[i] = make(livePreviewSession)
		clients[i].open = true
	}
	present := func(identity int) bool {
		for _, track := range playing.order {
			if track == identity {
				return true
			}
		}
		return false
	}
	activate := func() error {
		if err := p.StartSongEntry(0, "preview"); err != nil {
			return err
		}
		next := offer
		for _, identity := range next.order {
			if present(identity) {
				next.incarnations[identity] = playing.incarnations[identity]
			} else {
				incarnation++
				next.incarnations[identity] = incarnation
			}
		}
		playing, hasOffer = next, false
		for identity := range overrides {
			if !present(identity) || overrides[identity].value == playing.gains[identity] {
				overrides[identity].active = false
			}
		}
		counts.activations++
		return nil
	}
	publish := func(next previewReferenceScore) error {
		if err := p.Offer(previewLifecycleScore(t, next)); err != nil {
			return err
		}
		offer, hasOffer = next, true
		return nil
	}
	cancel := func(client, identity int) {
		gesture := clients[client].gestures[identity]
		override := &overrides[identity]
		if override.active && override.owner == client && override.gesture == gesture.version {
			override.active = false
		}
		clients[client].gestures[identity] = previewReferenceGesture{}
	}
	var pcm [2048 * 8]byte
	for step, op := range operations {
		client := &clients[op.client]
		gesture := &client.gestures[op.track]
		message := ""
		switch op.kind {
		case "set", "newer-gesture":
			if client.pending.valid {
				break
			}
			if !client.open {
				sessions[op.client] = make(livePreviewSession)
				*client = previewReferenceClient{open: true}
			}
			message = fmt.Sprintf(`{"type":"preview-set","entity":"track:%s","param":"mix.gain","value":%g}`, previewTrackIDs[op.track], op.value)
			if present(op.track) {
				sequence++
				*gesture = previewReferenceGesture{version: sequence, value: op.value}
				overrides[op.track] = previewReferenceOverride{owner: op.client, gesture: sequence, value: op.value, active: true, incarnation: playing.incarnations[op.track]}
				counts.sets++
			} else {
				if err := s.handleLiveMessage([]byte(message), sessions[op.client]); err == nil {
					return fmt.Errorf("step %d: preview accepted for absent %s", step, previewTrackIDs[op.track])
				}
				message = ""
			}
		case "capture":
			if client.pending.valid {
				break
			}
			if !client.open {
				sessions[op.client] = make(livePreviewSession)
				*client = previewReferenceClient{open: true}
			}
			if present(op.track) {
				target, err := p.ResolvePreviewTrack(previewTrackIDs[op.track])
				if err != nil {
					return err
				}
				client.pending = previewReferencePending{target: target, track: op.track, value: op.value, incarnation: playing.incarnations[op.track], valid: true}
				counts.captures++
			}
		case "publish":
			if client.open && client.pending.valid {
				pending := client.pending
				client.pending = previewReferencePending{}
				version, err := p.SetResolvedPreview(pending.target, kernel.ParamMixGain, pending.value)
				if err != nil {
					return err
				}
				sequence++
				client.gestures[pending.track] = previewReferenceGesture{version: sequence, value: pending.value}
				key := livePreviewKey{entity: "track:" + previewTrackIDs[pending.track], param: kernel.ParamMixGain}
				sessions[op.client][key] = livePreview{stream: p, version: version}
				counts.publications++
				if present(pending.track) && playing.incarnations[pending.track] == pending.incarnation {
					overrides[pending.track] = previewReferenceOverride{owner: op.client, gesture: sequence, value: pending.value, active: true, incarnation: pending.incarnation}
					counts.sets++
				} else {
					counts.stalePublications++
					if present(pending.track) {
						counts.readdedPublications++
					}
				}
			}
		case "cancel":
			if client.pending.valid {
				break
			}
			if client.open && (present(op.track) || gesture.version != 0) {
				cancel(op.client, op.track)
				message = fmt.Sprintf(`{"type":"preview-end","entity":"track:%s","param":"mix.gain","commit":false}`, previewTrackIDs[op.track])
				counts.cancels++
			}
		case "commit":
			if client.pending.valid {
				break
			}
			if client.open && gesture.version != 0 {
				gesture.committed = true
				message = fmt.Sprintf(`{"type":"preview-end","entity":"track:%s","param":"mix.gain","commit":true}`, previewTrackIDs[op.track])
			}
		case "close", "drop":
			sessions[op.client].close()
			for identity, owned := range client.gestures {
				if !owned.committed {
					cancel(op.client, identity)
				}
			}
			client.open = false
			client.pending = previewReferencePending{}
			counts.teardowns++
		case "offer":
			next := playing
			next.gains[op.track] = op.value
			if next.gains[op.track] == playing.gains[op.track] {
				next.gains[op.track] = -15
			}
			if err := publish(next); err != nil {
				return err
			}
		case "activate":
			if hasOffer {
				if err := activate(); err != nil {
					return err
				}
			}
		case "reorder", "add", "remove", "shift":
			next := playing
			next.order = append([]int(nil), playing.order...)
			switch op.kind {
			case "reorder":
				for left, right := 0, len(next.order)-1; left < right; left, right = left+1, right-1 {
					next.order[left], next.order[right] = next.order[right], next.order[left]
				}
				counts.reorders++
			case "add":
				if present(op.track) {
					break
				}
				if playing.incarnations[op.track] != 0 {
					counts.readds++
				}
				next.order = append([]int{op.track}, next.order...)
				counts.adds++
			case "remove":
				for index, identity := range next.order {
					if identity == op.track && len(next.order) > 1 {
						next.order = append(next.order[:index], next.order[index+1:]...)
						counts.removes++
						break
					}
				}
			case "shift":
				next.order = append(next.order[1:], next.order[0])
				next.gains[op.track] = op.value
				counts.shifts++
			}
			if err := publish(next); err != nil {
				return err
			}
			if err := activate(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown operation %q", op.kind)
		}
		if message != "" {
			if err := s.handleLiveMessage([]byte(message), sessions[op.client]); err != nil {
				return fmt.Errorf("step %d %v: %w", step, op, err)
			}
		}
		if op.kind == "commit" && message != "" && present(op.track) {
			// Save and activate before a subsequent edit. Pending committed
			// gestures on disconnect remain covered by the render scheduler.
			next := playing
			next.gains[op.track] = gesture.value
			if err := publish(next); err != nil {
				return err
			}
			if err := activate(); err != nil {
				return err
			}
		}
		if _, err := p.Read(pcm[:]); err != nil {
			return fmt.Errorf("step %d %v: %w", step, op, err)
		}
		frame := <-p.Meters()
		if frame.TrackCount != uint8(len(playing.order)) {
			return fmt.Errorf("step %d: meter topology has %d tracks, want %d", step, frame.TrackCount, len(playing.order))
		}
		for identity := range previewTrackIDs {
			if !present(identity) {
				if _, ok := p.TrackIndex(previewTrackIDs[identity]); ok {
					return fmt.Errorf("step %d: removed track %s still resolves", step, previewTrackIDs[identity])
				}
			}
		}
		for index, identity := range playing.order {
			name := previewTrackIDs[identity]
			track, ok := p.TrackIndex(name)
			if !ok || track != uint8(index) || frame.TrackIDs[index] != name {
				return fmt.Errorf("step %d: %s index %d/%v, expected %d", step, name, track, ok, index)
			}
			committed := playing.gains[identity]
			if got, ok := p.CommittedValue(track, kernel.ParamMixGain); !ok || got != committed {
				return fmt.Errorf("step %d %v: %s committed %g/%v, want %g", step, op, name, got, ok, committed)
			}
			override := overrides[identity]
			value, active := p.OverrideValue(track, kernel.ParamMixGain)
			if active != override.active || active && value != override.value {
				return fmt.Errorf("step %d %v: %s override %g/%v, model %g/%v", step, op, name, value, active, override.value, override.active)
			}
			want := committed
			if active {
				owner := clients[override.owner]
				if !owner.open || owner.gestures[identity].committed {
					return fmt.Errorf("step %d: %s override has no open uncommitted owner", step, name)
				}
				if override.incarnation != playing.incarnations[identity] {
					return fmt.Errorf("step %d: %s preview crossed incarnations %d -> %d", step, name, override.incarnation, playing.incarnations[identity])
				}
				want = override.value
			}
			wantPeak := .125 * math.Pow(10, float64(want)/20)
			if got := float64(frame.Tracks[index].Peak); math.Abs(got-wantPeak) > .002 {
				return fmt.Errorf("step %d %v: %s audible peak %g, want %g (%g dB)", step, op, name, got, wantPeak, want)
			}
			counts.checks++
		}
	}
	return nil
}

func minimizePreviewLifecycle(t *testing.T, operations []previewOperation) []previewOperation {
	t.Helper()
	minimal := append([]previewOperation(nil), operations...)
	for changed := true; changed; {
		changed = false
		for i := range minimal {
			candidate := append([]previewOperation(nil), minimal[:i]...)
			candidate = append(candidate, minimal[i+1:]...)
			if runPreviewLifecycle(t, candidate, nil) != nil {
				minimal, changed = candidate, true
				break
			}
		}
	}
	return minimal
}

func TestPreviewLifecycleGeneratedStateMachine(t *testing.T) {
	const seeds, steps = 2048, 36
	kinds := [...]string{"set", "cancel", "commit", "close", "drop", "offer", "activate", "newer-gesture", "reorder", "add", "remove", "shift", "capture", "publish", "remove-add"}
	values := [...]float32{-12, -9, -6, -3, 0}
	var counts previewLifecycleCounts
	operationsCount := 0
	for seed := int64(0); seed < seeds; seed++ {
		random := rand.New(rand.NewSource(seed))
		operations := make([]previewOperation, 0, steps+3)
		lastGestureClient := -1
		for step := 0; step < steps; step++ {
			op := previewOperation{kind: kinds[random.Intn(len(kinds))], client: random.Intn(3), track: random.Intn(4), value: values[random.Intn(len(values))]}
			if op.kind == "newer-gesture" && lastGestureClient >= 0 {
				op.client = (lastGestureClient + 1 + random.Intn(2)) % 3
			}
			if op.kind == "set" || op.kind == "newer-gesture" {
				lastGestureClient = op.client
			}
			if op.kind == "remove-add" {
				op.kind = "remove"
				operations = append(operations, op)
				op.kind = "add"
			}
			operations = append(operations, op)
		}
		if seed < 64 {
			// Force a suspended socket publication around two score activations,
			// alongside a newer gesture on the replacement track from another client.
			operations = append(operations,
				previewOperation{kind: "close", client: 0}, previewOperation{kind: "close", client: 1},
				previewOperation{kind: "add", track: 1}, previewOperation{kind: "add", track: 0},
				previewOperation{kind: "set", client: 0, track: 0, value: 0},
				previewOperation{kind: "capture", client: 0, track: 0, value: 0},
				previewOperation{kind: "remove", track: 0}, previewOperation{kind: "add", track: 0},
				previewOperation{kind: "set", client: 1, track: 0, value: -3},
				previewOperation{kind: "publish", client: 0},
			)
		}
		for client := 0; client < 3; client++ {
			operations = append(operations, previewOperation{kind: "close", client: client})
		}
		operationsCount += len(operations)
		if err := runPreviewLifecycle(t, operations, &counts); err != nil {
			minimal := minimizePreviewLifecycle(t, operations)
			t.Fatalf("seed %d: %v; minimized counterexample: %v", seed, err, minimal)
		}
	}
	if counts.readdedPublications < 64 || counts.readds == 0 {
		t.Fatal("no delayed publication crossed a remove/re-add boundary")
	}
	t.Logf("captures %d, delayed publications %d, stale publications %d (re-added targets %d), re-added incarnations %d; 64 forced capture/remove/re-add/newer-gesture/publish sequences", counts.captures, counts.publications, counts.stalePublications, counts.readdedPublications, counts.readds)
	t.Logf("%d seeds (0..%d), %d random choices, %d executed operations including expanded remove/add pairs, forced interleavings and final closures; %d per-track lifecycle cases; sets %d, cancels %d, teardowns %d, activations %d, reorders %d, additions %d, removals %d, index shifts %d", seeds, seeds-1, seeds*steps, operationsCount, counts.checks, counts.sets, counts.cancels, counts.teardowns, counts.activations, counts.reorders, counts.adds, counts.removes, counts.shifts)
}
