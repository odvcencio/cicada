package audiobackend

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ebitengine/oto/v3"
)

const otoSampleRate = 48_000

type otoBackend struct{}

func (otoBackend) Name() Name { return Oto }

func (otoBackend) Devices() ([]Device, bool, error) { return nil, false, nil }

func (otoBackend) SampleRate(config Config) (int, error) {
	if config.Device != "" || config.CaptureDevice != "" || config.CaptureChannels != 0 {
		return 0, fmt.Errorf("oto uses the system default output and does not support device selection or capture")
	}
	if config.SampleRate > 0 && config.SampleRate != otoSampleRate {
		return 0, fmt.Errorf("oto supports %d Hz output", otoSampleRate)
	}
	return otoSampleRate, nil
}

var otoContextState struct {
	sync.Mutex
	device     *oto.Context
	sampleRate int
	channels   int
}

func (otoBackend) Open(config Config, render Callback) (Stream, error) {
	if err := audioConfigError(config); err != nil {
		return nil, err
	}
	if _, err := (otoBackend{}).SampleRate(config); err != nil {
		return nil, err
	}
	if render == nil {
		return nil, fmt.Errorf("audio render callback is required")
	}
	device, err := getOtoContext(config.SampleRate, config.Channels)
	if err != nil {
		return nil, err
	}
	reader := newCallbackReader(config, render)
	player := device.NewPlayer(reader)
	player.SetBufferSize(config.FramesPerPeriod * config.Channels * 4 * 4)
	return &otoStream{
		device: device,
		player: player,
		reader: reader,
		format: Format{
			Backend: Oto, SampleRate: config.SampleRate, Channels: config.Channels,
			FramesPerPeriod: config.FramesPerPeriod, Device: "System default",
		},
	}, nil
}

func getOtoContext(sampleRate, channels int) (*oto.Context, error) {
	otoContextState.Lock()
	defer otoContextState.Unlock()
	if otoContextState.device != nil {
		if otoContextState.sampleRate != sampleRate || otoContextState.channels != channels {
			return nil, fmt.Errorf("oto context already uses %d Hz and %d channels", otoContextState.sampleRate, otoContextState.channels)
		}
		return otoContextState.device, nil
	}
	device, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate: sampleRate, ChannelCount: channels, Format: oto.FormatFloat32LE,
		BufferSize: 20 * time.Millisecond, ApplicationName: "Cicada",
	})
	if err != nil {
		return nil, err
	}
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		return nil, fmt.Errorf("audio device did not become ready within 10 seconds")
	}
	if err := device.Err(); err != nil {
		return nil, err
	}
	otoContextState.device = device
	otoContextState.sampleRate, otoContextState.channels = sampleRate, channels
	return device, nil
}

type callbackReader struct {
	render Callback
	output [][]float32
	pcm    []byte
	offset int
	err    atomic.Pointer[audioError]
}

type audioError struct{ err error }

func newCallbackReader(config Config, render Callback) *callbackReader {
	reader := &callbackReader{
		render: render,
		output: make([][]float32, config.Channels),
		pcm:    make([]byte, config.FramesPerPeriod*config.Channels*4),
		offset: config.FramesPerPeriod * config.Channels * 4,
	}
	for channel := range reader.output {
		reader.output[channel] = make([]float32, config.FramesPerPeriod)
	}
	return reader
}

func (r *callbackReader) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	written := 0
	for written < len(dst) {
		if r.offset == len(r.pcm) {
			r.renderPeriod()
		}
		count := copy(dst[written:], r.pcm[r.offset:])
		r.offset += count
		written += count
	}
	return written, nil
}

func (r *callbackReader) renderPeriod() {
	if err := r.render(nil, r.output); err != nil {
		r.err.CompareAndSwap(nil, &audioError{err: err})
		for _, channel := range r.output {
			clear(channel)
		}
	}
	position := 0
	for frame := range r.output[0] {
		for _, channel := range r.output {
			bits := math.Float32bits(channel[frame])
			r.pcm[position] = byte(bits)
			r.pcm[position+1] = byte(bits >> 8)
			r.pcm[position+2] = byte(bits >> 16)
			r.pcm[position+3] = byte(bits >> 24)
			position += 4
		}
	}
	r.offset = 0
}

type otoStream struct {
	device *oto.Context
	player *oto.Player
	reader *callbackReader
	format Format
	closed atomic.Bool
}

func (s *otoStream) Start() error {
	if s == nil || s.closed.Load() {
		return errors.New("oto stream is closed")
	}
	if err := s.Err(); err != nil {
		return err
	}
	s.player.Play()
	return nil
}

func (s *otoStream) Pause() {
	if s != nil && s.player != nil {
		s.player.PauseAndStopReading()
	}
}

func (*otoStream) StopClosesDevice() bool { return false }

func (s *otoStream) Err() error {
	if s == nil {
		return nil
	}
	if failure := s.reader.err.Load(); failure != nil {
		return failure.err
	}
	if err := s.device.Err(); err != nil {
		return err
	}
	return s.player.Err()
}

func (s *otoStream) Close() error {
	if s == nil || !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	s.player.PauseAndStopReading()
	return s.player.Close()
}

func (s *otoStream) Format() Format { return s.format }

func (*otoStream) Stats() Stats { return Stats{} }
