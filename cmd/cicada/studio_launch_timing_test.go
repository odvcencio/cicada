package main

import (
	"io"
	"net/http"
	"os"
	"testing"

	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/kernel/cmd"
)

func TestStudioLaunchUsesScoreDefaultAndExplicitOverride(t *testing.T) {
	for _, timing := range []struct {
		name, block string
		expected    uint32
	}{
		{"default", "", 2}, {"now", "live { land = now }\n", 0}, {"beat", "live { land = beat }\n", 1}, {"phrase", "live { land = phrase phrase = 2bars }\n", uint32(cmd.QuantizePhrase)},
	} {
		t.Run(timing.name, func(t *testing.T) {
			source := "cicada 2\n" + studioScore + timing.block
			path := studioTestPath(t, source)
			score, err := compileLiveScore(path)
			if err != nil {
				t.Fatal(err)
			}
			stream, err := liveplay.New(score, liveSampleRate)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			transport := newStudioTransport(path)
			transport.audioNull, transport.stream, transport.playing = true, stream, true
			transport.last, err = playSourceHash(path)
			if err != nil {
				t.Fatal(err)
			}
			s := &studio{path: path, transport: transport}
			response := listeningCall(t, s, studioTransportInput{Action: "launch", Scene: "main"})
			if response.Code != http.StatusOK {
				t.Fatalf("score default: %d %s", response.Code, response.Body.String())
			}
			if got := stream.PendingSceneQuantize(); got != timing.expected {
				t.Fatalf("score timing = %d, want %d", got, timing.expected)
			}
			if stream.PendingScene() != "main" {
				t.Fatal("missing queued scene")
			}
			explicit := uint32(6)
			response = listeningCall(t, s, studioTransportInput{Action: "launch", Scene: "main", Quantize: &explicit})
			if response.Code != http.StatusOK || stream.PendingSceneQuantize() != explicit {
				t.Fatalf("explicit override: %d %s", response.Code, response.Body.String())
			}
			legacyZero := uint32(0)
			response = listeningCall(t, s, studioTransportInput{Action: "launch", Scene: "main", Quantize: &legacyZero})
			if response.Code != http.StatusOK || stream.PendingSceneQuantize() != 2 {
				t.Fatalf("legacy zero override: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestStudioLaunchUsesPhraseFromPendingScore(t *testing.T) {
	initial := "cicada 2\n" + studioScore + "live { land = bar }\n"
	path := studioTestPath(t, initial)
	score, err := compileLiveScore(path)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := liveplay.New(score, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	transport := newStudioTransport(path)
	transport.audioNull, transport.stream, transport.playing = true, stream, true
	transport.last, err = playSourceHash(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &studio{path: path, transport: transport}
	changed := "cicada 2\n" + studioScore + "live { land = phrase phrase = 2bars }\n"
	if err := os.WriteFile(path, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	response := listeningCall(t, s, studioTransportInput{Action: "launch", Scene: "main"})
	if response.Code != http.StatusOK {
		t.Fatalf("pending score timing: %d %s", response.Code, response.Body.String())
	}
	if got := stream.PendingSceneQuantize(); got != uint32(cmd.QuantizePhrase) {
		t.Fatalf("pending score timing = %d", got)
	}
	if _, err := io.CopyN(io.Discard, stream, (192000+512)*8); err != nil {
		t.Fatal(err)
	}
	for len(stream.Events()) > 0 {
		event := <-stream.Events()
		if event.Kind == "scene" && event.Name == "main" {
			if event.Bar != 3 {
				t.Fatalf("phrase launch landed at bar %d, want 3", event.Bar)
			}
			return
		}
	}
	t.Fatal("pending-score phrase launch did not land")
}

func TestStudioPhraseDefaultRequiresPhraseLength(t *testing.T) {
	source := "cicada 2\n" + studioScore + "live { land = phrase }\n"
	path := studioTestPath(t, source)
	s := &studio{path: path, transport: newStudioTransport(path)}
	response := listeningCall(t, s, studioTransportInput{Action: "launch", Scene: "main"})
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing phrase length: %d %s", response.Code, response.Body.String())
	}
}
