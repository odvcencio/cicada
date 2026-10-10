package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"testing"
	"testing/synctest"
	"time"

	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/kernel"
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

type previewOperation struct {
	kind   string
	client int
	value  float32
}

func (op previewOperation) String() string {
	if op.kind == "set" || op.kind == "newer-gesture" || op.kind == "offer" {
		return fmt.Sprintf("%s(client=%d,value=%g)", op.kind, op.client, op.value)
	}
	return fmt.Sprintf("%s(client=%d)", op.kind, op.client)
}

// The model uses gesture identities independent of Player's version tokens.
type previewReferenceClient struct {
	open      bool
	gesture   int
	value     float32
	committed bool
}

type previewReference struct {
	clients   [3]previewReferenceClient
	sequence  int
	owner     int
	gesture   int
	value     float32
	active    bool
	committed float32
	offer     float32
	hasOffer  bool
}

func previewLifecycleScore(t *testing.T, gain float32) liveplay.Score {
	t.Helper()
	program := graph.Program{}
	program.Nodes[0] = graph.Node{Op: graph.Constant, Value: 375}
	program.Nodes[1] = graph.Node{Op: graph.Sine, A: 0}
	program.Nodes[2] = graph.Node{Op: graph.Constant, Value: .125}
	program.Nodes[3] = graph.Node{Op: graph.Multiply, A: 1, B: 2}
	program.Len, program.Output = 4, 3
	// A slow clock keeps offers pending between generated operations. Explicit
	// song starts exercise the real render-thread offer activation path.
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: 256, Tracks: 1, MaxVoices: 1, BPMMilli: 20_000}
	cfg.Track[0].Kind, cfg.Track[0].Graph = engine.VoiceGraph, program
	cfg.Track[0].GainDB, cfg.Track[0].GainSet = float64(gain), true
	cfg.Track[0].Pan, cfg.Track[0].BusSFX = -1, true
	cfg.Scenes = []engine.Scene{{}}
	cfg.Song = []engine.SongEntry{{Scene: 0, Bars: 1}}
	cfg.LoopSong = true
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return liveplay.Score{
		Engine: created, SampleRate: 48_000, BPMMilli: 20_000, Name: "preview",
		Tracks: []liveplay.TrackSlots{{ID: "bass"}}, HasSFX: true,
		Song:       []liveplay.SongEntry{{Scene: "preview", StartBar: 1}},
		Parameters: []liveplay.ParameterValue{{Track: 0, ID: kernel.ParamMixGain, Value: gain}},
	}
}

func runPreviewLifecycle(t *testing.T, operations []previewOperation) error {
	t.Helper()
	p, err := liveplay.New(previewLifecycleScore(t, -6), 48_000)
	if err != nil {
		return err
	}
	defer p.Close()
	s := &studio{transport: &studioTransport{stream: p}}
	var sessions [3]livePreviewSession
	model := previewReference{committed: -6}
	for i := range sessions {
		sessions[i] = make(livePreviewSession)
		model.clients[i].open = true
	}
	activate := func() error {
		if err := p.StartSongEntry(0, "preview"); err != nil {
			return err
		}
		model.committed, model.hasOffer = model.offer, false
		if model.active && model.value == model.committed {
			model.active = false
		}
		return nil
	}
	var pcm [2048 * 8]byte
	for step, op := range operations {
		client := &model.clients[op.client]
		message := ""
		switch op.kind {
		case "set", "newer-gesture":
			if !client.open {
				sessions[op.client] = make(livePreviewSession)
			}
			model.sequence++
			*client = previewReferenceClient{open: true, gesture: model.sequence, value: op.value}
			model.owner, model.gesture, model.value, model.active = op.client, client.gesture, op.value, true
			message = fmt.Sprintf(`{"type":"preview-set","entity":"track:bass","param":"mix.gain","value":%g}`, op.value)
		case "cancel":
			if client.open {
				if model.active && model.owner == op.client && model.gesture == client.gesture {
					model.active = false
				}
				client.gesture = 0
				message = `{"type":"preview-end","entity":"track:bass","param":"mix.gain","commit":false}`
			}
		case "commit":
			if client.open && client.gesture != 0 {
				client.committed = true
				message = `{"type":"preview-end","entity":"track:bass","param":"mix.gain","commit":true}`
			}
		case "close", "drop":
			sessions[op.client].close()
			if !client.committed && model.active && model.owner == op.client && model.gesture == client.gesture {
				model.active = false
			}
			client.open = false
		case "offer":
			value := op.value
			if value == model.committed {
				value = -15 // Always offer a different value.
				if value == model.committed {
					value = 0
				}
			}
			if err := p.Offer(previewLifecycleScore(t, value)); err != nil {
				return err
			}
			model.offer, model.hasOffer = value, true
		case "activate":
			if model.hasOffer {
				if err := activate(); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unknown operation %q", op.kind)
		}
		if message != "" {
			if err := s.handleLiveMessage([]byte(message), sessions[op.client]); err != nil {
				return fmt.Errorf("step %d %v: %w", step, op, err)
			}
		}
		if op.kind == "commit" && message != "" {
			// Complete the matching save/activation handoff before a later edit.
			// The pending handoff on disconnect is covered separately above.
			if err := p.Offer(previewLifecycleScore(t, client.value)); err != nil {
				return err
			}
			model.offer, model.hasOffer = client.value, true
			if err := activate(); err != nil {
				return err
			}
		}
		if _, err := p.Read(pcm[:]); err != nil {
			return err
		}
		if committed, ok := p.CommittedValue(0, kernel.ParamMixGain); !ok || committed != model.committed {
			return fmt.Errorf("step %d %v: committed %g/%v, model %g", step, op, committed, ok, model.committed)
		}
		value, active := p.OverrideValue(0, kernel.ParamMixGain)
		if active != model.active || active && value != model.value {
			return fmt.Errorf("step %d %v: override %g/%v, model %g/%v", step, op, value, active, model.value, model.active)
		}
		want := model.committed
		if model.active {
			owner := model.clients[model.owner]
			if !owner.open || owner.committed {
				return fmt.Errorf("step %d: uncommitted override has no open owner", step)
			}
			want = model.value
		}
		peak := float64(0)
		for frame := 2048 - 256; frame < 2048; frame++ {
			sample := math.Float32frombits(binary.LittleEndian.Uint32(pcm[frame*8:]))
			peak = max(peak, math.Abs(float64(sample)))
		}
		wantPeak := .125 * math.Pow(10, float64(want)/20)
		if math.Abs(peak-wantPeak) > .002 {
			return fmt.Errorf("step %d %v: audible peak %g, want %g (%g dB)", step, op, peak, wantPeak, want)
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
			if runPreviewLifecycle(t, candidate) != nil {
				minimal, changed = candidate, true
				break
			}
		}
	}
	return minimal
}

func TestPreviewLifecycleGeneratedStateMachine(t *testing.T) {
	const seeds, steps = 2048, 24
	kinds := [...]string{"set", "cancel", "commit", "close", "drop", "offer", "activate", "newer-gesture"}
	values := [...]float32{-12, -9, -6, -3, 0}
	for seed := int64(0); seed < seeds; seed++ {
		random := rand.New(rand.NewSource(seed))
		operations := make([]previewOperation, 0, steps+3)
		lastGestureClient := -1
		for step := 0; step < steps; step++ {
			op := previewOperation{
				kind: kinds[random.Intn(len(kinds))], client: random.Intn(3), value: values[random.Intn(len(values))],
			}
			if op.kind == "newer-gesture" && lastGestureClient >= 0 {
				op.client = (lastGestureClient + 1 + random.Intn(2)) % 3
			}
			if op.kind == "set" || op.kind == "newer-gesture" {
				lastGestureClient = op.client
			}
			operations = append(operations, op)
		}
		for client := 0; client < 3; client++ {
			operations = append(operations, previewOperation{kind: "close", client: client})
		}
		if err := runPreviewLifecycle(t, operations); err != nil {
			minimal := minimizePreviewLifecycle(t, operations)
			t.Fatalf("seed %d: %v; minimized counterexample: %v", seed, err, minimal)
		}
	}
	t.Logf("%d seeds, %d random operations, plus final closure of every client", seeds, seeds*steps)
}
