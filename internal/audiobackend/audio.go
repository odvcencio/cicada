// Package audiobackend provides Cicada's native audio output backends.
package audiobackend

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"
)

type Name string

const (
	Tymbal Name = "tymbal"
	Oto    Name = "oto"
	Null   Name = "null"
)

var ErrUnsupported = errors.New("audio backend is unsupported on this platform")

var validNames = [...]Name{Tymbal, Oto, Null}

func ValidNames() []Name {
	return append([]Name(nil), validNames[:]...)
}

func validName(name string) bool {
	for _, valid := range validNames {
		if name == string(valid) {
			return true
		}
	}
	return false
}

// Select applies CLI, environment, and default precedence in that order.
func Select(flagValue string, flagSet bool, envValue string, fallback Name) (Name, error) {
	selected := string(fallback)
	if envValue != "" {
		selected = envValue
	}
	if flagSet {
		selected = flagValue
	}
	if !validName(selected) {
		return "", fmt.Errorf("unknown audio backend %q (valid: tymbal, oto, null)", selected)
	}
	return Name(selected), nil
}

func DefaultFor(command string) Name {
	return DefaultForOS(command, runtime.GOOS)
}

func DefaultForOS(command, goos string) Name {
	if command == "studio" && goos == "windows" {
		return Tymbal
	}
	return Oto
}

// ParseFlag removes --audio and its value from a command's arguments. The
// last occurrence wins; argument validation and env precedence are handled by
// Select.
func ParseFlag(args []string) (rest []string, value string, set bool, err error) {
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--audio" {
			if i+1 >= len(args) {
				return nil, "", false, fmt.Errorf("--audio needs a value (valid: tymbal, oto, null)")
			}
			i++
			value, set = args[i], true
			continue
		}
		if strings.HasPrefix(arg, "--audio=") {
			value, set = strings.TrimPrefix(arg, "--audio="), true
			continue
		}
		rest = append(rest, arg)
	}
	return rest, value, set, nil
}

type Device struct {
	ID, Name        string
	Inputs, Outputs int
	SampleRates     []int
	DefaultInput    bool
	DefaultOutput   bool
}

type Config struct {
	SampleRate        int
	Channels          int
	FramesPerPeriod   int
	Device            string
	CaptureDevice     string
	CaptureChannels   int
	AllowMissingInput bool
}

type Format struct {
	Backend         Name
	Device          string
	CaptureDevice   string
	SampleRate      int
	Channels        int
	CaptureChannels int
	FramesPerPeriod int
	Latency         time.Duration
	CaptureLatency  time.Duration
}

type Stats struct {
	Callbacks   uint64
	Dropouts    uint64
	Late        uint64
	CallbackMax time.Duration
}

type Callback func(input, output [][]float32) error

type Stream interface {
	Start() error
	Pause()
	StopClosesDevice() bool
	Err() error
	Close() error
	Format() Format
	Stats() Stats
}

type Backend interface {
	Name() Name
	Devices() ([]Device, bool, error)
	SampleRate(Config) (int, error)
	Open(Config, Callback) (Stream, error)
}

func New(name Name) (Backend, error) {
	switch name {
	case Null:
		return nullBackend{}, nil
	case Oto:
		return otoBackend{}, nil
	case Tymbal:
		return newTymbalBackend()
	default:
		return nil, fmt.Errorf("unknown audio backend %q (valid: tymbal, oto, null)", name)
	}
}

func audioConfigError(config Config) error {
	if config.SampleRate <= 0 || config.Channels < 1 || config.Channels > 2 || config.FramesPerPeriod <= 0 {
		return fmt.Errorf("audio format must have a positive sample rate and period, with 1 or 2 output channels")
	}
	if config.CaptureChannels < 0 || config.CaptureChannels > 2 {
		return fmt.Errorf("audio capture must request 0, 1, or 2 channels")
	}
	return nil
}
