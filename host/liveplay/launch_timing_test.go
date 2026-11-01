package liveplay

import (
	"io"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
)

func TestScoreLaunchNowAndPhraseBoundary(t *testing.T) {
	for _, timing := range []struct {
		name         string
		quantize     cmd.Quantize
		expectedBar  int64
		beforeFrames int64
	}{
		{"now", 0, 1, 0}, {"phrase", cmd.QuantizePhrase, 3, 192000 - blockFrames},
	} {
		t.Run(timing.name, func(t *testing.T) {
			score := testScore(t, "launch", 120000, engine.Scene{}, engine.Scene{})
			score.SceneIDs, score.PhraseBars = []string{"first", "second"}, 2
			player, err := New(score, 48000)
			if err != nil {
				t.Fatal(err)
			}
			defer player.Close()
			if err := player.LaunchSceneQuantized("second", timing.quantize); err != nil {
				t.Fatal(err)
			}
			if _, err := io.CopyN(io.Discard, player, timing.beforeFrames*8); err != nil {
				t.Fatal(err)
			}
			if timing.beforeFrames > 0 && player.CurrentScene() == "second" {
				t.Fatal("phrase launch landed before its boundary")
			}
			if _, err := io.CopyN(io.Discard, player, 2*blockFrames*8); err != nil {
				t.Fatal(err)
			}
			found := false
			for len(player.Events()) > 0 {
				event := <-player.Events()
				if event.Kind == "scene" && event.Name == "second" {
					if event.Bar != timing.expectedBar {
						t.Fatalf("launch bar %d, want %d", event.Bar, timing.expectedBar)
					}
					found = true
				}
			}
			if !found {
				t.Fatal("launch did not land")
			}
			if got := player.CurrentScene(); got != "second" {
				t.Fatalf("playing scene = %q, want second", got)
			}
		})
	}
	score := testScore(t, "no phrase", 120000, engine.Scene{})
	player, err := New(score, 48000)
	if err != nil {
		t.Fatal(err)
	}
	defer player.Close()
	if err := player.LaunchSceneQuantized("missing", cmd.QuantizePhrase); err == nil {
		t.Fatal("accepted phrase timing without a phrase")
	}
}

func TestImmediateLaunchReportsCurrentBar(t *testing.T) {
	score := testScore(t, "launch", 120000, engine.Scene{}, engine.Scene{})
	score.SceneIDs = []string{"first", "second"}
	player, err := New(score, 48000)
	if err != nil {
		t.Fatal(err)
	}
	defer player.Close()
	if _, err := io.CopyN(io.Discard, player, 100000*8); err != nil {
		t.Fatal(err)
	}
	if err := player.LaunchSceneQuantized("second", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, player, 2*blockFrames*8); err != nil {
		t.Fatal(err)
	}
	for len(player.Events()) > 0 {
		event := <-player.Events()
		if event.Kind == "scene" && event.Name == "second" {
			if event.Bar != 2 {
				t.Fatalf("immediate launch bar %d, want 2", event.Bar)
			}
			return
		}
	}
	t.Fatal("immediate launch did not land")
}

func TestLaunchPhraseFollowsNewestOfferedScore(t *testing.T) {
	initial := testScore(t, "initial", 120000, engine.Scene{})
	initial.PhraseBars = 2
	player, err := New(initial, 48000)
	if err != nil {
		t.Fatal(err)
	}
	defer player.Close()
	for _, bars := range []int{4, 8} {
		next := testScore(t, "replacement", 120000, engine.Scene{})
		next.PhraseBars = bars
		if err := player.Offer(next); err != nil {
			t.Fatal(err)
		}
	}
	if err := player.LaunchSceneQuantized("main", cmd.QuantizePhrase); err != nil {
		t.Fatal(err)
	}
	if got := player.launches.Load().targetTick; got != 8*3840 {
		t.Fatalf("newest phrase target = %d", got)
	}
}
