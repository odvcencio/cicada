// cicada-kernel-wasm exposes the native audio engine to a TinyGo worklet host.
package main

import (
	"unsafe"

	"m31labs.dev/cicada/host/kernelimage"
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
var sceneBytes, songBytes []byte
var arrangementPrepared, songLoop bool
var projectBytes []byte
var legacyConfigured bool

//go:wasmexport gosx_audio_project_alloc
func projectAlloc(size int32) uint32 {
	if audioEngine != nil || legacyConfigured || len(projectBytes) != 0 || size < 32 || size > kernelimage.MaxImageBytes {
		return 0
	}
	projectBytes = make([]byte, int(size))
	return uint32(uintptr(unsafe.Pointer(&projectBytes[0])))
}

// Scene bytes are sixteen bindings per scene: 0=keep, 1=off, 2..17=slot 0..15.
// Song bytes are four per entry: little-endian scene index and bar count.
//
//go:wasmexport gosx_audio_arrangement_alloc
func arrangementAlloc(scenes, entries int32) int32 {
	if audioEngine != nil || arrangementPrepared || len(projectBytes) != 0 || scenes < 0 || scenes > 65535 || entries < 0 || entries > 65535 || entries > 0 && scenes == 0 {
		return -1
	}
	legacyConfigured = true
	sceneBytes = make([]byte, int(scenes)*16)
	songBytes = make([]byte, int(entries)*4)
	arrangementPrepared = true
	return 0
}

//go:wasmexport gosx_audio_scene_ptr
func scenePtr() uint32 {
	if len(sceneBytes) == 0 {
		return 0
	}
	return uint32(uintptr(unsafe.Pointer(&sceneBytes[0])))
}

//go:wasmexport gosx_audio_song_ptr
func songPtr() uint32 {
	if len(songBytes) == 0 {
		return 0
	}
	return uint32(uintptr(unsafe.Pointer(&songBytes[0])))
}

//go:wasmexport gosx_audio_song_loop
func songLoopMode(enabled int32) int32 {
	if audioEngine != nil || len(projectBytes) != 0 || enabled < 0 || enabled > 1 {
		return -1
	}
	legacyConfigured = true
	songLoop = enabled == 1
	return 0
}

//go:wasmexport gosx_audio_track_kind
func trackKind(index, kind int32) int32 {
	if audioEngine != nil || len(projectBytes) != 0 || index < 0 || index >= 16 || kind < 0 || kind > int32(engine.VoiceDrums) {
		return -1
	}
	legacyConfigured = true
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
	var cfg engine.Config
	if len(projectBytes) != 0 {
		var err error
		err = kernelimage.DecodeInto(projectBytes, int(sampleRate), int(maxBlock), &cfg)
		if err != nil {
			return -1
		}
	} else {
		cfg = engine.Config{SampleRate: int(sampleRate), MaxBlock: int(maxBlock), Tracks: trackCount, MaxVoices: 32}
		for i := 0; i < trackCount; i++ {
			cfg.Track[i].Kind = trackKinds[i]
		}
		cfg.Scenes = make([]engine.Scene, len(sceneBytes)/16)
		for i := range cfg.Scenes {
			for track := 0; track < 16; track++ {
				binding := sceneBytes[i*16+track]
				switch {
				case binding == 0:
				case binding == 1:
					cfg.Scenes[i].Track[track].Mode = engine.SceneOff
				case binding <= 17:
					cfg.Scenes[i].Track[track] = engine.SceneBinding{Mode: engine.SceneSlot, Slot: binding - 2}
				default:
					return -1
				}
			}
		}
		cfg.Song = make([]engine.SongEntry, len(songBytes)/4)
		for i := range cfg.Song {
			base := i * 4
			cfg.Song[i] = engine.SongEntry{
				Scene: uint16(songBytes[base]) | uint16(songBytes[base+1])<<8,
				Bars:  uint16(songBytes[base+2]) | uint16(songBytes[base+3])<<8,
			}
		}
		cfg.LoopSong = songLoop
	}
	created, err := engine.New(cfg)
	if err != nil {
		return -1
	}
	audioEngine = created
	trackCount = cfg.Tracks
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
