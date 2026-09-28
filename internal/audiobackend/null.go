package audiobackend

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type nullBackend struct{}

func (nullBackend) Name() Name { return Null }

func (nullBackend) Devices() ([]Device, bool, error) { return nil, false, nil }

func (nullBackend) SampleRate(config Config) (int, error) {
	if config.CaptureChannels != 0 || config.CaptureDevice != "" {
		return 0, fmt.Errorf("null audio does not support capture")
	}
	if config.SampleRate > 0 {
		return config.SampleRate, nil
	}
	return otoSampleRate, nil
}

func (nullBackend) Open(config Config, render Callback) (Stream, error) {
	if err := audioConfigError(config); err != nil {
		return nil, err
	}
	if config.CaptureChannels != 0 || config.CaptureDevice != "" {
		return nil, fmt.Errorf("null audio does not support capture")
	}
	if render == nil {
		return nil, fmt.Errorf("audio render callback is required")
	}
	period := time.Duration(config.FramesPerPeriod) * time.Second / time.Duration(config.SampleRate)
	if period <= 0 {
		period = time.Nanosecond
	}
	output := make([][]float32, config.Channels)
	for channel := range output {
		output[channel] = make([]float32, config.FramesPerPeriod)
	}
	return &nullStream{
		render: render,
		output: output,
		format: Format{
			Backend: Null, SampleRate: config.SampleRate, Channels: config.Channels,
			FramesPerPeriod: config.FramesPerPeriod, Device: "Null output",
		},
		period: period,
		closed: make(chan struct{}),
		done:   make(chan struct{}),
	}, nil
}

type nullStream struct {
	render  Callback
	output  [][]float32
	format  Format
	period  time.Duration
	active  atomic.Bool
	closed  chan struct{}
	done    chan struct{}
	lifeMu  sync.Mutex
	callMu  sync.Mutex
	close   sync.Once
	started atomic.Bool
	err     atomic.Pointer[audioError]
}

func (s *nullStream) Start() error {
	if s == nil {
		return errors.New("null stream is closed")
	}
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	select {
	case <-s.closed:
		return errors.New("null stream is closed")
	default:
	}
	if err := s.Err(); err != nil {
		return err
	}
	if s.started.CompareAndSwap(false, true) {
		go s.run()
	}
	s.active.Store(true)
	return s.Err()
}

func (s *nullStream) run() {
	defer close(s.done)
	ticker := time.NewTicker(s.period)
	defer ticker.Stop()
	for {
		select {
		case <-s.closed:
			return
		case <-ticker.C:
			s.renderPeriod()
		}
	}
}

func (s *nullStream) renderPeriod() {
	if !s.active.Load() {
		return
	}
	s.callMu.Lock()
	defer s.callMu.Unlock()
	if !s.active.Load() {
		return
	}
	if err := renderNullPeriod(s.render, nil, s.output); err != nil {
		s.err.CompareAndSwap(nil, &audioError{err: err})
	}
}

func renderNullPeriod(render Callback, input, output [][]float32) error {
	if err := render(input, output); err != nil {
		for _, channel := range output {
			clear(channel)
		}
		return err
	}
	// Rendering advances Cicada's engine and meters, but null output never
	// retains or plays the generated samples.
	for _, channel := range output {
		clear(channel)
	}
	return nil
}

func (s *nullStream) Pause() {
	if s != nil {
		s.active.Store(false)
		s.callMu.Lock()
		s.callMu.Unlock()
	}
}

func (*nullStream) StopClosesDevice() bool { return false }

func (s *nullStream) Err() error {
	if s == nil {
		return nil
	}
	if failure := s.err.Load(); failure != nil {
		return failure.err
	}
	return nil
}

func (s *nullStream) Close() error {
	if s == nil {
		return nil
	}
	s.lifeMu.Lock()
	s.close.Do(func() {
		s.active.Store(false)
		close(s.closed)
	})
	started := s.started.Load()
	s.lifeMu.Unlock()
	if started {
		<-s.done
	}
	return nil
}

func (s *nullStream) Format() Format { return s.format }

func (*nullStream) Stats() Stats { return Stats{} }
