package main

import (
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
)

// hostFaultCode runs act against a fresh engine, as the WASM exports do, and
// returns the code of the first Fault message, or 0 when the engine sent none.
func hostFaultCode(t *testing.T, act func()) uint16 {
	t.Helper()
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 1, MaxVoices: 1, BPMMilli: 120_000}
	cfg.Track[0].Kind = engine.VoiceAcid
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	savedEngine, savedTracks, savedFrames := audioEngine, trackCount, maxFrames
	t.Cleanup(func() {
		audioEngine, trackCount, maxFrames = savedEngine, savedTracks, savedFrames
		clear(commandBytes[:])
	})
	audioEngine, trackCount, maxFrames = e, 1, 128
	clear(commandBytes[:])
	act()
	var message cmd.Message
	for e.Poll(&message) {
		if message.Kind == cmd.Fault {
			return message.A
		}
	}
	return 0
}

// The host reports bad wire input with InjectFault. Those faults used the same
// numbers as the drive-insert and delay-tempo faults in the engine, so a log
// line could not say which one happened.
func TestHostInjectedFaultCodes(t *testing.T) {
	for _, tc := range []struct {
		name string
		want uint16
		act  func()
	}{
		{"count below zero", engine.FaultHostCommandBatch, func() { commandCommit(-1) }},
		{"count above capacity", engine.FaultHostCommandBatch, func() { commandCommit(int32(len(decoded)) + 1) }},
		{"unknown opcode", engine.FaultHostCommandBatch, func() { commandCommit(1) }},
		{"cue command", engine.FaultHostCommandBatch, func() {
			commandBytes[0], commandBytes[1] = byte(cmd.OpCue), 0xff
			commandCommit(1)
		}},
		{"zero frames", engine.FaultHostRenderFrames, func() { render(0) }},
		{"more frames than the block", engine.FaultHostRenderFrames, func() { render(129) }},
		{"valid batch", 0, func() {
			encoded, err := cmd.EncodeCommand(cmd.Command{Op: cmd.OpPlay, Track: 0xff}, 1)
			if err != nil {
				t.Fatal(err)
			}
			copy(commandBytes[:], encoded[:])
			commandCommit(1)
		}},
		{"valid render", 0, func() { render(128) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hostFaultCode(t, tc.act); got != tc.want {
				t.Fatalf("fault code = %d, want %d", got, tc.want)
			}
		})
	}
}
