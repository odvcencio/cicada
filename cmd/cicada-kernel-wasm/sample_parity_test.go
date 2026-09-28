//go:build wasm_integration

package main

import (
	"bytes"
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestAudioWASMFirstAcidSampleParity(t *testing.T) {
	compareWASMFixture(t, "first-acid.cicada", 16)
}

func TestAudioWASMAuthoredKitSampleParity(t *testing.T) {
	compareWASMFixture(t, "authored-kit.cicada", 1)
}

func TestAudioWASMLegacyThreeDrumTracksSampleParity(t *testing.T) {
	compareWASMFixture(t, "../testdata/compat/three-drums.cicada", 1)
}

func TestAudioWASMElevenLaneKitSampleParity(t *testing.T) {
	compareWASMFixture(t, "drums-kit.cicada", 1)
}

func TestAudioWASMDriveInsertSampleParity(t *testing.T) {
	compareWASMFixture(t, "fx/drive-insert.cicada", 1)
}

func TestAudioWASMCustomVoiceSlideSampleParity(t *testing.T) {
	compareWASMFixture(t, "glassbass.cicada", 1)
}

func TestAudioWASMDelaySendSampleParity(t *testing.T) {
	compareWASMFixture(t, "fx/delay-send.cicada", 1)
}

func TestAudioWASMReverbSendSampleParity(t *testing.T) {
	compareWASMFixture(t, "fx-bus.cicada", 1)
}

func TestAudioWASMCompressorBusSampleParity(t *testing.T) {
	compareWASMFixture(t, "fx/compressor-bus.cicada", 1)
}

func TestAudioWASMSFXBusSampleParity(t *testing.T) {
	compareWASMFixture(t, "sfx-bus.cicada", 1)
}

func TestAudioWASMLiveParameterMuteSoloMeterParity(t *testing.T) {
	const rate, blockSize, blocks = 48_000, 128, 16
	wasm, err := os.ReadFile(wasmModulePath())
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "first-acid.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("parse: %+v", diagnostic)
		}
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatalf("project: %+v", diagnostics)
	}
	cfg, err := project.CompileEngine(p, rate, blockSize)
	if err != nil {
		t.Fatal(err)
	}
	image, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	native, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	t.Cleanup(func() { _ = runtime.Close(ctx) })
	module, err := runtime.Instantiate(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	call := func(name string, args ...uint64) uint64 {
		t.Helper()
		function := module.ExportedFunction(name)
		if function == nil {
			t.Fatalf("missing WASM export %s", name)
		}
		result, callErr := function.Call(ctx, args...)
		if callErr != nil {
			t.Fatalf("WASM %s: %v", name, callErr)
		}
		if len(result) == 0 {
			return 0
		}
		return result[0]
	}
	call("_initialize")
	imagePtr := uint32(call("gosx_audio_project_alloc", uint64(len(image))))
	if imagePtr == 0 || !module.Memory().Write(imagePtr, image) || call("gosx_audio_init", rate, blockSize, 2) != 0 {
		t.Fatal("WASM engine initialization failed")
	}
	commands := []cmd.Command{
		{Op: cmd.OpPlay, Track: 0xff},
		{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixGain), Arg0: math.Float32bits(-3.5)},
		{Op: cmd.OpSetParam, Track: 1, Index: uint16(kernel.ParamMixMute), Arg0: math.Float32bits(1)},
		{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixSolo), Arg0: math.Float32bits(1)},
	}
	commandPtr := uint32(call("gosx_audio_cmd_ptr"))
	for index, command := range commands {
		record, encodeErr := cmd.EncodeCommand(command, uint8(cfg.Tracks))
		if encodeErr != nil || !module.Memory().Write(commandPtr+uint32(index*cmd.CommandSize), record[:]) {
			t.Fatalf("encode live command %d: %v", index, encodeErr)
		}
	}
	call("gosx_audio_cmd_commit", uint64(len(commands)))
	if !native.PushBatch(commands) {
		t.Fatal("native live command batch was rejected")
	}
	outputPtr, messagePtr := uint32(call("gosx_audio_out_ptr")), uint32(call("gosx_audio_msg_ptr"))
	var nativeL, nativeR [blockSize]float32
	var wasmLog, nativeLog []byte
	var maxDifference float64
	var maxFrame, maxChannel int
	var sounded, sawMeter bool
	var meterSources [256]bool
	for block := 0; block < blocks; block++ {
		call("gosx_audio_render", blockSize)
		native.Render(nativeL[:], nativeR[:])
		for channel := 0; channel < 2; channel++ {
			for frame := 0; frame < blockSize; frame++ {
				wasmSample, ok := module.Memory().ReadFloat32Le(outputPtr + uint32((channel*blockSize+frame)*4))
				if !ok || math.IsNaN(float64(wasmSample)) || math.IsInf(float64(wasmSample), 0) {
					t.Fatalf("invalid WASM output at block %d channel %d frame %d", block, channel, frame)
				}
				nativeSample := nativeL[frame]
				if channel == 1 {
					nativeSample = nativeR[frame]
				}
				sounded = sounded || wasmSample != 0
				difference := math.Abs(float64(wasmSample) - float64(nativeSample))
				if difference > maxDifference {
					maxDifference, maxFrame, maxChannel = difference, block*blockSize+frame, channel
				}
			}
		}
		count := int(call("gosx_audio_msg_drain"))
		for index := 0; index < count; index++ {
			data, ok := module.Memory().Read(messagePtr+uint32(index*cmd.MessageSize), cmd.MessageSize)
			if !ok {
				t.Fatal("WASM message pointer out of bounds")
			}
			message, decodeErr := cmd.DecodeMessage(data)
			if decodeErr != nil || message.Kind == cmd.Fault {
				t.Fatalf("WASM live message: %+v %v", message, decodeErr)
			}
			wasmLog = append(wasmLog, data...)
			sawMeter = sawMeter || message.Kind == cmd.Meter
			if message.Kind == cmd.Meter {
				meterSources[message.Track] = true
			}
		}
		for _, message := range drainNativeMessages(native) {
			if message.Kind == cmd.Fault {
				t.Fatalf("native live engine fault: %+v", message)
			}
			encoded := cmd.EncodeMessage(message)
			nativeLog = append(nativeLog, encoded[:]...)
		}
	}
	if !sounded || maxDifference > 1e-6 {
		t.Fatalf("native/WASM samples: sounded=%v max difference %.9g at frame %d channel %d", sounded, maxDifference, maxFrame, maxChannel)
	}
	if !sawMeter {
		t.Fatal("live parity run emitted no meter messages")
	}
	for _, source := range []uint8{0, 1, 2, 0xf0, 0xf1, 0xf2, 0xf3, 0xfb, 0xfc, 0xfd, 0xff} {
		if !meterSources[source] {
			t.Fatalf("meter source %#x was absent", source)
		}
	}
	if !bytes.Equal(wasmLog, nativeLog) {
		t.Fatalf("native/WASM message logs differ: %d vs %d bytes", len(nativeLog), len(wasmLog))
	}
	t.Logf("live parameter, mute, solo, and meter parity: %d log bytes, max sample difference %.9g", len(wasmLog), maxDifference)
}

func TestAudioWASMSceneSettingsSampleParity(t *testing.T) {
	source, err := os.ReadFile("testdata/scene-settings.cicada")
	if err != nil {
		t.Fatal(err)
	}
	compareWASMSource(t, "scene-settings.cicada", source, 2, true)
}

func compareWASMFixture(t *testing.T, fixture string, bars int) {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", fixture))
	if err != nil {
		t.Fatal(err)
	}
	compareWASMSource(t, fixture, source, bars, false)
}

func compareWASMSource(t *testing.T, fixture string, source []byte, bars int, exactMessages bool) {
	t.Helper()
	wasm, err := os.ReadFile(wasmModulePath())
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("parse: %+v", diagnostic)
		}
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatalf("project: %+v", diagnostics)
	}
	for _, rate := range []int{44_100, 48_000} {
		t.Run(sampleRateName(rate), func(t *testing.T) {
			const blockSize = 128
			cfg, err := project.CompileEngine(p, rate, blockSize)
			if err != nil {
				t.Fatal(err)
			}
			image, err := kernelimage.Encode(cfg)
			if err != nil {
				t.Fatal(err)
			}
			native, err := engine.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			runtime := wazero.NewRuntime(ctx)
			t.Cleanup(func() { _ = runtime.Close(ctx) })
			module, err := runtime.Instantiate(ctx, wasm)
			if err != nil {
				t.Fatal(err)
			}
			call := func(name string, args ...uint64) uint64 {
				t.Helper()
				fn := module.ExportedFunction(name)
				if fn == nil {
					t.Fatalf("missing WASM export %s", name)
				}
				result, err := fn.Call(ctx, args...)
				if err != nil {
					t.Fatalf("WASM %s: %v", name, err)
				}
				if len(result) == 0 {
					return 0
				}
				return result[0]
			}
			call("_initialize")
			imagePtr := uint32(call("gosx_audio_project_alloc", uint64(len(image))))
			if imagePtr == 0 || !module.Memory().Write(imagePtr, image) {
				t.Fatal("project image buffer unavailable")
			}
			if call("gosx_audio_init", uint64(rate), blockSize, 2) != 0 {
				t.Fatal("WASM project init failed")
			}
			play := cmd.Command{Op: cmd.OpPlay, Track: 0xff}
			record, err := cmd.EncodeCommand(play, uint8(cfg.Tracks))
			if err != nil || !module.Memory().Write(uint32(call("gosx_audio_cmd_ptr")), record[:]) {
				t.Fatalf("write play command: %v", err)
			}
			call("gosx_audio_cmd_commit", 1)
			if !native.Push(play) {
				t.Fatal("native play command rejected")
			}
			outputPtr := uint32(call("gosx_audio_out_ptr"))
			messagePtr := uint32(call("gosx_audio_msg_ptr"))
			frames := int(math.Round(float64(bars*4*60*rate*1000) / float64(cfg.BPMMilli)))
			var nativeL, nativeR [blockSize]float32
			var wasmEvents, nativeEvents []cmd.Message
			var peakDifference float64
			var peakSample int
			var peakChannel int
			var nonzero bool
			var stableMemory uint32
			for block := 0; block*blockSize < frames; block++ {
				call("gosx_audio_render", blockSize)
				native.Render(nativeL[:], nativeR[:])
				for channel := 0; channel < 2; channel++ {
					for frame := 0; frame < blockSize && block*blockSize+frame < frames; frame++ {
						wasmSample, ok := module.Memory().ReadFloat32Le(outputPtr + uint32((channel*blockSize+frame)*4))
						if !ok {
							t.Fatal("WASM output pointer out of bounds")
						}
						nativeSample := nativeL[frame]
						if channel == 1 {
							nativeSample = nativeR[frame]
						}
						if math.IsNaN(float64(wasmSample)) || math.IsInf(float64(wasmSample), 0) {
							t.Fatalf("nonfinite WASM sample at %d channel %d", block*blockSize+frame, channel)
						}
						nonzero = nonzero || wasmSample != 0
						difference := math.Abs(float64(wasmSample) - float64(nativeSample))
						if difference > peakDifference {
							peakDifference, peakSample, peakChannel = difference, block*blockSize+frame, channel
						}
					}
				}
				if block%64 == 63 || (block+1)*blockSize >= frames {
					count := int(call("gosx_audio_msg_drain"))
					for i := 0; i < count; i++ {
						data, ok := module.Memory().Read(messagePtr+uint32(i*cmd.MessageSize), cmd.MessageSize)
						if !ok {
							t.Fatal("WASM message pointer out of bounds")
						}
						message, err := cmd.DecodeMessage(data)
						if err != nil || message.Kind == cmd.Fault {
							t.Fatalf("WASM message: %+v %v", message, err)
						}
						wasmEvents = append(wasmEvents, message)
					}
					nativeEvents = append(nativeEvents, drainNativeMessages(native)...)
				}
				if block == 10 {
					stableMemory = module.Memory().Size()
				}
			}
			if !nonzero {
				t.Fatalf("%s rendered silence", fixture)
			}
			if stableMemory == 0 || module.Memory().Size() != stableMemory {
				t.Fatalf("WASM memory grew after warm-up: %d -> %d", stableMemory, module.Memory().Size())
			}
			if exactMessages {
				compareMessageLogs(t, wasmEvents, nativeEvents)
			} else {
				compareMusicalMessages(t, wasmEvents, nativeEvents)
			}
			if peakDifference > 1e-6 {
				t.Fatalf("native/WASM peak sample difference %.9g at frame %d channel %d exceeds 1e-6", peakDifference, peakSample, peakChannel)
			}
			t.Logf("%s: %d bars at %d Hz, peak native/WASM sample difference %.9g", fixture, bars, rate, peakDifference)
		})
	}
}

func compareMessageLogs(t *testing.T, wasm, native []cmd.Message) {
	t.Helper()
	var wasmLog, nativeLog []byte
	for _, message := range wasm {
		encoded := cmd.EncodeMessage(message)
		wasmLog = append(wasmLog, encoded[:]...)
	}
	for _, message := range native {
		encoded := cmd.EncodeMessage(message)
		nativeLog = append(nativeLog, encoded[:]...)
	}
	if !bytes.Equal(wasmLog, nativeLog) {
		t.Fatalf("native/WASM scene-setting message logs differ: %d vs %d bytes", len(nativeLog), len(wasmLog))
	}
}

func sampleRateName(rate int) string {
	if rate == 44_100 {
		return "44.1kHz"
	}
	return "48kHz"
}
