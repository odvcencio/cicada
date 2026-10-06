// Package demopolicy defines Studio permissions shared by GoSX browser code,
// UI controls, keyboard commands and optional validated agent commands.
// It is an integration primitive, not a public adapter for native Studio.
package demopolicy

import "errors"

type Action string

const (
	Play           Action = "transport.play"
	Stop           Action = "transport.stop"
	Note           Action = "instrument.note"
	Parameter      Action = "instrument.parameter"
	Edit           Action = "project.edit"
	Undo           Action = "project.undo"
	Redo           Action = "project.redo"
	Reset          Action = "session.reset"
	Capture        Action = "capture.browser"
	MIDIInput      Action = "midi.input"
	ExportAudio    Action = "export.audio"
	ExportMIDI     Action = "export.midi"
	ExportProject  Action = "export.project"
	ExportStems    Action = "export.stems"
	ExportJob      Action = "export.job"
	NativeAudio    Action = "audio.native"
	ReadLocalFile  Action = "filesystem.read"
	WriteLocalFile Action = "filesystem.write"
)

var ErrDenied = errors.New("CICADA-CAPABILITY: command unavailable in this session")

type profile uint8

const (
	invalid profile = iota
	localOSS
	publicDemo
)

// Policy cannot be elevated by decoding client-provided capability flags.
// The zero value denies every command. Select the profile at app construction.
type Policy struct{ profile profile }

func LocalOSS() Policy   { return Policy{profile: localOSS} }
func PublicDemo() Policy { return Policy{profile: publicDemo} }

// Allows recognizes exact command IDs; unknown commands fail closed in both
// profiles. Permission is not evidence that the app implements the feature.
func (p Policy) Allows(action Action) bool {
	switch action {
	case Play, Stop, Note, Parameter, Edit, Undo, Redo, Reset, Capture, MIDIInput:
		return p.profile == localOSS || p.profile == publicDemo
	case ExportAudio, ExportMIDI, ExportProject, ExportStems, ExportJob,
		NativeAudio, ReadLocalFile, WriteLocalFile:
		return p.profile == localOSS
	default:
		return false
	}
}

// Require belongs before parameter validation and side effects in the shared
// command dispatcher. Hiding a button alone never enforces this policy.
func (p Policy) Require(action Action) error {
	if !p.Allows(action) {
		return ErrDenied
	}
	return nil
}

// Dispatch checks permissions before invoking the command implementation.
// Implementations must still validate typed inputs, revision and limits.
// Keyboard and agent commands must use this same entrypoint.
func (p Policy) Dispatch(action Action, run func() error) error {
	if err := p.Require(action); err != nil {
		return err
	}
	if run == nil {
		return ErrDenied
	}
	return run()
}
