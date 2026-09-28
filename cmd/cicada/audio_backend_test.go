package main

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"m31labs.dev/cicada/internal/audiobackend"
)

func TestSelectCommandAudioEnvironmentAndFlagPrecedence(t *testing.T) {
	t.Setenv("CICADA_AUDIO", "null")
	args, name, _, err := selectCommandAudio("play", []string{"score.cicada"})
	if err != nil || name != audiobackend.Null || len(args) != 1 || args[0] != "score.cicada" {
		t.Fatalf("environment selection = (%v, %q, %v)", args, name, err)
	}

	t.Setenv("CICADA_AUDIO", "invalid")
	args, name, _, err = selectCommandAudio("play", []string{"--audio", "oto", "score.cicada"})
	if err != nil || name != audiobackend.Oto || len(args) != 1 || args[0] != "score.cicada" {
		t.Fatalf("flag selection = (%v, %q, %v)", args, name, err)
	}

	_, _, _, err = selectCommandAudio("play", []string{"--audio", "alsa"})
	if err == nil || err.Error() != `unknown audio backend "alsa" (valid: tymbal, oto, null)` {
		t.Fatalf("unknown backend error = %v", err)
	}
}

func TestNullBackendMatchesStudioReaderRenderer(t *testing.T) {
	const frames = 32
	inputPCM := make([]byte, frames*8)
	for frame := 0; frame < frames; frame++ {
		left := float32((frame%5)-2) / 8
		right := float32(2-(frame%5)) / 8
		binary.LittleEndian.PutUint32(inputPCM[frame*8:frame*8+4], math.Float32bits(left))
		binary.LittleEndian.PutUint32(inputPCM[frame*8+4:frame*8+8], math.Float32bits(right))
	}

	var expectedLeft, expectedRight [frames]float32
	var expectedPeriod [frames * 8]byte
	expectedOutput := [][]float32{expectedLeft[:], expectedRight[:]}
	if err := renderStudioAudioPeriod(bytes.NewReader(inputPCM), expectedPeriod[:], nil, expectedOutput, studioAudioMonitor{Muted: true}); err != nil {
		t.Fatal(err)
	}

	backend, err := audiobackend.New(audiobackend.Null)
	if err != nil {
		t.Fatal(err)
	}
	var gotLeft, gotRight [frames]float32
	var got atomic.Bool
	var callbackPCM [frames * 8]byte
	stream, err := backend.Open(audiobackend.Config{
		SampleRate: liveSampleRate, Channels: 2, FramesPerPeriod: frames,
	}, func(input [][]float32, output [][]float32) error {
		if err := renderStudioAudioPeriod(bytes.NewReader(inputPCM), callbackPCM[:], input, output, studioAudioMonitor{Muted: true}); err != nil {
			return err
		}
		copy(gotLeft[:], output[0])
		copy(gotRight[:], output[1])
		got.Store(true)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stream.Format().Backend != audiobackend.Null || stream.Format().Latency != 0 {
		t.Fatalf("null format = %+v", stream.Format())
	}
	if err := stream.Start(); err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	deadline := time.Now().Add(time.Second)
	for !got.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	stream.Pause()
	if !got.Load() {
		t.Fatal("null backend did not render a callback period")
	}
	for frame := 0; frame < frames; frame++ {
		if gotLeft[frame] != expectedLeft[frame] || gotRight[frame] != expectedRight[frame] {
			t.Fatalf("null render frame %d = (%f, %f), want (%f, %f)", frame, gotLeft[frame], gotRight[frame], expectedLeft[frame], expectedRight[frame])
		}
	}
	if strings.Contains(stream.Format().Device, "System default") {
		t.Fatalf("null backend reports a physical device: %+v", stream.Format())
	}
}
