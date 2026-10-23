package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/project"
)

func TestSoloPlacementKeepsLaterTempoAndAutomation(t *testing.T) {
	s := listeningStudio(t)
	source := []byte(strings.Replace(studioListeningScore, "arrange {", "arrange {\n  tempo @1.1.1 137.333\n  tempo @3.1.1 96", 1) + "\nautomate right.level {\n @1.1.1 -10dB\n @4.1.1 -3dB smooth\n}\n")
	if err := os.WriteFile(s.path, source, 0600); err != nil {
		t.Fatal(err)
	}
	r := listeningCall(t, s, studioTransportInput{Action: "audition", Placement: "pickup", Mode: "solo", Loop: true})
	if r.Code != http.StatusOK {
		t.Fatalf("solo phrase with later tempo/automation: %d %s", r.Code, r.Body)
	}
	current, _ := os.ReadFile(s.path)
	if !bytes.Equal(current, source) {
		t.Fatal("solo audition rewrote the song")
	}
}

func TestSoloPlacementLiveOfferResolvesReorderedIdentity(t *testing.T) {
	data, err := os.ReadFile("../../examples/arrangement-audition.cicada")
	if err != nil {
		t.Fatal(err)
	}
	path := studioTestPath(t, string(data))
	p, err := compileStudioSource(path, data)
	if err != nil {
		t.Fatal(err)
	}
	p, listening, err := prepareStudioListening(p, studioListening{Kind: "placement", Name: "phrase", Mode: "solo", Loop: true})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := compileStudioListeningScore(path, p, listening, 48000)
	if err != nil {
		t.Fatal(err)
	}
	frames := initial.Engine.FramesBetweenTicks(listening.StartTick, listening.EndTick)
	player, err := liveplay.New(initial, 48000)
	if err != nil {
		t.Fatal(err)
	}
	defer player.Close()
	r := listening.playbackRange()
	if err := player.PlayRange(r); err != nil {
		t.Fatal(err)
	}
	before, after := make([]byte, frames*8), make([]byte, frames*8)
	if _, err := io.ReadFull(player, before); err != nil {
		t.Fatal(err)
	}
	copy, arrangement := *p, *p.Arrange
	arrangement.Placements = slices.Clone(arrangement.Placements)
	arrangement.Placements[0], arrangement.Placements[1] = arrangement.Placements[1], arrangement.Placements[0]
	copy.Arrange = &arrangement
	next, err := compileStudioListeningScore(path, &copy, listening, 48000)
	if err != nil {
		t.Fatal(err)
	}
	next.Range = &r
	if err := player.Offer(next); err != nil {
		t.Fatal(err)
	}
	if err := player.PlayRange(r); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(player, after); err != nil || !bytes.Equal(before, after) {
		t.Fatalf("a new publication selected the wrong placement identity: %v", err)
	}
}

func TestSoloPerformanceUsesTheSongClockAndFullCurve(t *testing.T) {
	data, err := os.ReadFile("../../examples/arrangement-audition.cicada")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	for _, rate := range []int{44100, 48000} {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			path := studioTestPath(t, source)
			original, err := compileStudioSource(path, []byte(source))
			if err != nil {
				t.Fatal(err)
			}
			before, _ := project.CanonicalJSON(original)
			p, listening, err := prepareStudioListening(original, studioListening{Kind: "placement", Name: "phrase", Mode: "solo", Loop: true})
			if err != nil {
				t.Fatal(err)
			}
			solo, err := compileStudioListeningScore(path, p, listening, rate)
			if err != nil {
				t.Fatal(err)
			}
			// The independent reference keeps every placement, tempo and curve,
			// but its accompaniment pattern contains only rests.
			silentBed := strings.Replace(source, "steps=4 { 1 . 5 . }", "steps=4 { . . . . }", 1)
			if silentBed == source {
				t.Fatal("missing accompaniment fixture")
			}
			reference, err := compileStudioSource(path, []byte(silentBed))
			if err != nil {
				t.Fatal(err)
			}
			ref, err := compileLiveProjectAtRate(path, reference, rate)
			if err != nil {
				t.Fatal(err)
			}
			frames := solo.Engine.FramesBetweenTicks(listening.StartTick, listening.EndTick)
			read := func(score liveplay.Score, loop bool) []byte {
				t.Helper()
				player, err := liveplay.New(score, rate)
				if err != nil {
					t.Fatal(err)
				}
				defer player.Close()
				rangeValue := listening.playbackRange()
				rangeValue.Loop = loop
				if err := player.PlayRange(rangeValue); err != nil {
					t.Fatal(err)
				}
				pcm := make([]byte, frames*8*2)
				if _, err := io.ReadFull(player, pcm); err != nil {
					t.Fatal(err)
				}
				if loop && player.RangeCycles() != 1 || !loop && !player.RangeFinished() {
					t.Fatal("audition range did not retain its exact musical boundary")
				}
				return pcm
			}
			a, b := read(solo, true), read(ref, true)
			if !bytes.Equal(a, b) || bytes.Equal(a, make([]byte, len(a))) {
				t.Fatal("solo MIDI performance differs from the silent-bed reference")
			}
			full, err := compileLiveProjectAtRate(path, original, rate)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(a, read(full, true)) {
				t.Fatal("solo still contains the accompaniment")
			}
			after, _ := project.CanonicalJSON(original)
			if !bytes.Equal(before, after) {
				t.Fatal("audition mutated the original project")
			}
			t.Logf("two exact passes: rate=%d frames=%d sha256=%x", rate, 2*frames, sha256.Sum256(a))
		})
	}
}
