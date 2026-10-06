package liveplay

import (
	"runtime"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
)

func TestPendingLaunchesCanBeReadDuringAudioAndControlChanges(t *testing.T) {
	score := slotScore(t, "riff", 1, engine.Scene{})
	score.SceneIDs = []string{"main"}
	p, err := New(score, 48_000)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 2)
	go func() {
		var output [1024]byte
		for i := 0; i < 1000; i++ {
			if _, err := p.Read(output[:]); err != nil {
				finished <- err
				return
			}
			runtime.Gosched()
		}
		finished <- nil
	}()
	go func() {
		for i := 0; i < 1000; i++ {
			if err := p.LaunchScene("main"); err != nil {
				finished <- err
				return
			}
			if err := p.SelectPattern("bass", "riff"); err != nil {
				finished <- err
				return
			}
			runtime.Gosched()
			p.CancelScene()
			p.CancelPatterns()
		}
		finished <- nil
	}()
	for i := 0; i < 1000; i++ {
		if got := p.PendingScene(); got != "" && got != "main" {
			t.Errorf("invalid scene snapshot %q", got)
		}
		snapshot := p.PendingPatterns()
		for _, request := range snapshot {
			if request != (SlotRequest{}) && request != (SlotRequest{Track: "bass", Pattern: "riff"}) {
				t.Errorf("invalid slot snapshot %+v", request)
			}
		}
		runtime.Gosched()
	}
	for i := 0; i < 2; i++ {
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
	}
	p.CancelScene()
	p.CancelPatterns()
	if p.PendingScene() != "" || p.PendingPatterns() != ([16]SlotRequest{}) {
		t.Fatal("cancelled launches remain in the queue")
	}
}

func TestPendingSongClearsWhenLandingEventCannotBeDelivered(t *testing.T) {
	p, err := New(liveSongScore(t, "song", 2), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	for len(p.events) < cap(p.events) {
		p.events <- Event{Kind: "old"}
	}
	id, err := p.QueueSongEntry(1, "chorus")
	if err != nil || id == 0 || p.PendingSongEntryID() != id {
		t.Fatalf("queue: %d %v", id, err)
	}
	var frame [8]byte
	if _, err := p.Read(frame[:]); err != nil {
		t.Fatal(err)
	}
	if p.PendingSongEntryID() != 0 {
		t.Fatal("consumed song remains pending after its event was dropped")
	}
	id, err = p.QueueSongEntry(0, "dusk")
	if err != nil || p.PendingSongEntryID() != id {
		t.Fatalf("second queue: %d %v", id, err)
	}
	p.CancelStart()
	if p.PendingSongEntryID() != 0 {
		t.Fatal("cancelled song remains pending")
	}
}

func TestConcurrentSongRequestsKeepTheQueuedIdentity(t *testing.T) {
	p, err := New(liveSongScore(t, "song", 2), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 100; round++ {
		finished := make(chan error, 4)
		for worker := 0; worker < 4; worker++ {
			go func() {
				for i := 0; i < 50; i++ {
					if _, err := p.QueueSongEntry(1, "chorus"); err != nil {
						finished <- err
						return
					}
					runtime.Gosched()
				}
				finished <- nil
			}()
		}
		for worker := 0; worker < 4; worker++ {
			if err := <-finished; err != nil {
				t.Fatal(err)
			}
		}
		request := <-p.starts
		if got := p.PendingSongEntryID(); got != request.ID {
			t.Fatalf("pending identity %d does not name queued request %d", got, request.ID)
		}
	}
}

// The audio goroutine hands a due scene launch to the engine. That must not allocate, and it
// must mark the published record in place so readers never see a record replaced under them.
func TestQueueSceneLaunchDoesNotAllocate(t *testing.T) {
	score := slotScore(t, "riff", 1)
	score.SceneIDs = []string{"main"}
	p, err := New(score, 48_000)
	if err != nil {
		t.Fatal(err)
	}
	requests := make([]*sceneLaunch, 32)
	for i := range requests {
		requests[i] = &sceneLaunch{id: uint64(i + 1), name: "main", targetTick: 0}
	}
	i := 0
	allocs := testing.AllocsPerRun(20, func() {
		p.launches.Store(requests[i])
		i++
		p.queueSceneLaunch()
	})
	if allocs != 0 {
		t.Fatalf("queueSceneLaunch allocated %.1f times per launch, want 0", allocs)
	}
	published := requests[i-1]
	if p.launches.Load() != published || !published.submitted.Load() {
		t.Fatal("the launch was not marked submitted in place")
	}
}
