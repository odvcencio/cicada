package main

// This test-only harness serves the actual domain API to the production
// workstation while supplying synthetic PCM through the existing input seam.
// It is inactive in ordinary test runs and never opens physical audio devices.
import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"testing"
	"time"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/internal/audiobackend"
)

type syntheticUIStream struct {
	mu     sync.Mutex
	active bool
	closed chan struct{}
	once   sync.Once
	audio  *backendStudioAudio
	format audiobackend.Format
}

func (d *syntheticUIStream) Start() error           { d.mu.Lock(); d.active = true; d.mu.Unlock(); return nil }
func (d *syntheticUIStream) Pause()                 { d.mu.Lock(); d.active = false; d.mu.Unlock() }
func (d *syntheticUIStream) StopClosesDevice() bool { return false }
func (d *syntheticUIStream) Err() error             { return nil }
func (d *syntheticUIStream) Close() error {
	d.Pause()
	d.once.Do(func() { close(d.closed) })
	return nil
}
func (d *syntheticUIStream) Format() audiobackend.Format { return d.format }
func (d *syntheticUIStream) Stats() audiobackend.Stats   { return audiobackend.Stats{} }
func (d *syntheticUIStream) run() {
	ticker := time.NewTicker(time.Duration(d.format.FramesPerPeriod) * time.Second / time.Duration(d.format.SampleRate))
	defer ticker.Stop()
	input := [][]float32{make([]float32, d.format.FramesPerPeriod), make([]float32, d.format.FramesPerPeriod)}
	output := [][]float32{make([]float32, d.format.FramesPerPeriod), make([]float32, d.format.FramesPerPeriod)}
	for i := range input[0] {
		input[0][i] = .2
		input[1][i] = .2
	}
	var frame uint64
	for {
		select {
		case <-d.closed:
			return
		case <-ticker.C:
			d.mu.Lock()
			if d.active {
				d.audio.captureInput(capture.Block{DeviceEpoch: 1, DeviceFrame: frame, SampleRate: d.format.SampleRate, Period: d.format.FramesPerPeriod, Frames: d.format.FramesPerPeriod, Layout: capture.LayoutStereo}, input)
				_ = d.audio.renderPeriod(input, output)
				frame += uint64(d.format.FramesPerPeriod)
			}
			d.mu.Unlock()
		}
	}
}
func TestSyntheticStudioUIHarness(t *testing.T) {
	score, manifest := os.Getenv("CICADA_SYNTHETIC_SCORE"), os.Getenv("CICADA_SYNTHETIC_MANIFEST")
	if score == "" || manifest == "" {
		t.Skip("explicit synthetic UI harness only")
	}
	s, err := newStudio(score)
	if err != nil {
		t.Fatal(err)
	}
	defer s.shutdown()
	s.transport.mu.Lock()
	s.transport.sampleRate = 48000
	s.transport.audioOptions = studioAudioOptions{InputEnabled: true, MonitorGain: 1, MonitorMode: "stereo", PeriodFrames: 128, AmpDrive: .35, AmpTone: .5}
	s.transport.takeInputOpener = func(reader io.Reader, options studioAudioOptions, _ string, rate int) (studioAudioDevice, error) {
		format := audiobackend.Format{SampleRate: rate, FramesPerPeriod: 128, Channels: 2, CaptureChannels: 2}
		stream := &syntheticUIStream{closed: make(chan struct{}), format: format}
		audio := &backendStudioAudio{reader: reader, stream: stream, pcm: make([]byte, 128*8), actual: format, engineEpoch: studioEngineEpoch.Add(1)}
		stream.audio = audio
		audio.SetMonitor(studioMonitorOptions(options))
		go stream.run()
		return audio, nil
	}
	s.transport.mu.Unlock()
	handler, token, err := privateStudioService(s.domainRoutes())
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go server.Serve(listener)
	defer server.Close()
	data, _ := json.Marshal(map[string]string{"backend": "http://" + listener.Addr().String(), "token": token, "source": "synthetic PCM; no physical device"})
	if err = os.WriteFile(manifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	<-signals
}
