// cicada-kernel-wasm exposes the native audio engine to a TinyGo worklet host.
package main

import (
	"unsafe"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
)

var audioEngine *engine.Engine
var maxFrames int
var trackKinds = [16]engine.VoiceKind{engine.VoiceAcid}
var trackCount = 1
var output [8192]float32
var commandBytes [512 * cmd.CommandSize]byte
var decoded [512]cmd.Command
var messageBytes [256 * cmd.MessageSize]byte

//go:wasmexport gosx_audio_track_kind
func trackKind(index, kind int32) int32 {
	if audioEngine != nil || index < 0 || index >= 16 || kind < 0 || kind > int32(engine.VoiceDrums) {
		return -1
	}
	trackKinds[index] = engine.VoiceKind(kind)
	if int(index)+1 > trackCount {
		trackCount = int(index) + 1
	}
	return 0
}

//go:wasmexport gosx_audio_init
func initAudio(sampleRate, maxBlock, channels int32) int32 {
	if audioEngine != nil || channels != 2 || maxBlock < 1 || maxBlock > 4096 {
		return -1
	}
	cfg := engine.Config{SampleRate: int(sampleRate), MaxBlock: int(maxBlock), Tracks: trackCount, MaxVoices: 32}
	for i := 0; i < trackCount; i++ {
		cfg.Track[i].Kind = trackKinds[i]
	}
	created, err := engine.New(cfg)
	if err != nil {
		return -1
	}
	audioEngine = created
	maxFrames = int(maxBlock)
	return 0
}

//go:wasmexport gosx_audio_out_ptr
func outPtr() uint32 { return uint32(uintptr(unsafe.Pointer(&output[0]))) }

//go:wasmexport gosx_audio_cmd_ptr
func commandPtr() uint32 { return uint32(uintptr(unsafe.Pointer(&commandBytes[0]))) }

//go:wasmexport gosx_audio_cmd_cap
func commandCap() int32 { return int32(len(decoded)) }

//go:wasmexport gosx_audio_cmd_commit
func commandCommit(n int32) {
	if audioEngine == nil {
		return
	}
	if n < 0 || int(n) > len(decoded) {
		audioEngine.InjectFault(12)
		return
	}
	written, err := cmd.DecodeCommands(commandBytes[:int(n)*cmd.CommandSize], uint8(trackCount), decoded[:])
	if err != nil || !audioEngine.PushBatch(decoded[:written]) {
		audioEngine.InjectFault(12)
	}
}

//go:wasmexport gosx_audio_render
func render(frames int32) {
	if audioEngine == nil {
		return
	}
	if frames < 1 || int(frames) > maxFrames {
		clear(output[:maxFrames*2])
		audioEngine.InjectFault(13)
		return
	}
	audioEngine.Render(output[:frames], output[maxFrames:maxFrames+int(frames)])
}

//go:wasmexport gosx_audio_msg_ptr
func messagePtr() uint32 { return uint32(uintptr(unsafe.Pointer(&messageBytes[0]))) }

//go:wasmexport gosx_audio_msg_drain
func messageDrain() int32 {
	if audioEngine == nil {
		return 0
	}
	var n int32
	var message cmd.Message
	for n < int32(len(messageBytes)/cmd.MessageSize) && audioEngine.Poll(&message) {
		encoded := cmd.EncodeMessage(message)
		copy(messageBytes[int(n)*cmd.MessageSize:], encoded[:])
		n++
	}
	return n
}

func main() {}
