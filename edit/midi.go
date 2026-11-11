package edit

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"m31labs.dev/cicada/notation"
)

// SetMIDIMapping writes one learned input to the score's logical port. Device
// names remain local. Relearning the same input replaces its previous target.
// The port must already be declared in the score's midi block.
//
// Example: {"kind":"setmidimapping","port":"pads","message":"cc","channel":1,"number":74,"target":"bass.cutoff"}
// Example: {"kind":"setmidimapping","port":"pads","message":"note","channel":1,"number":36,"target":"launch break"}
type SetMIDIMapping struct {
	Port    string `json:"port" cicada:"required"`                                  // the logical port's name
	Message string `json:"message" values:"midi-message" cicada:"required,selects"` // a controller or a note
	Channel int    `json:"channel" cicada:"required"`                               // MIDI channel 1–16
	Number  int    `json:"number"`                                                  // the controller or note number, 0–127
	Target  string `json:"target" cicada:"required"`                                // for cc, a parameter path such as bass.cutoff; for note, play, stop, launch <scene>, stop-track <track> or launch-slot <track> <pattern>
}

func (SetMIDIMapping) Kind() string { return "setmidimapping" }

// midiMessages are the inputs SetMIDIMapping learns.
var midiMessages = valueList("midi-message", "cc", "note")

func init() {
	Register("setmidimapping", func() Intent { return &SetMIDIMapping{} })
	Handle("setmidimapping", setMIDIMapping)
}

var midiName = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,63}$`)
var midiPath = regexp.MustCompile(`^[a-z_][a-z0-9_-]*(\.[a-z_][a-z0-9_-]*)+$`)

func setMIDIMapping(ctx *Context, intent Intent) error {
	in := intent.(*SetMIDIMapping)
	if !midiName.MatchString(in.Port) || in.Channel < 1 || in.Channel > 16 || in.Number < 0 || in.Number > 127 {
		return fmt.Errorf("invalid MIDI port, channel, or input")
	}
	if !containsString(midiMessages, in.Message) {
		return fmt.Errorf("MIDI Learn accepts a controller or note")
	}
	if in.Message == "cc" && !midiPath.MatchString(in.Target) {
		return fmt.Errorf("choose a parameter")
	}
	if in.Message == "note" {
		parts := strings.Split(in.Target, " ")
		valid := in.Target == "play" || in.Target == "stop"
		if len(parts) == 2 && (parts[0] == "launch" || parts[0] == "stop-track") {
			valid = midiName.MatchString(parts[1])
		}
		if len(parts) == 3 && parts[0] == "launch-slot" {
			valid = midiName.MatchString(parts[1]) && len(parts[2]) <= 64
			for _, part := range strings.Split(parts[2], ".") {
				valid = valid && midiName.MatchString(part)
			}
		}
		if !valid {
			return fmt.Errorf("choose a scene, slot, track stop, play, or stop")
		}
	}
	score, ds, err := ctx.ParseProject()
	if err != nil {
		return err
	}
	if score == nil || hasErrors(ds) || score.Version != 2 {
		return fmt.Errorf("save a valid edition-2 score before learning MIDI")
	}
	if score.MIDI == nil {
		return fmt.Errorf("declare and bind a logical MIDI port first")
	}
	portFound := false
	for _, p := range score.MIDI.Ports {
		portFound = portFound || p.Name == in.Port
	}
	if !portFound {
		return fmt.Errorf("logical MIDI port %s is not declared", in.Port)
	}
	path := score.MIDI.Position.File
	if path == "" {
		path = ctx.Options.Path
	}
	for _, file := range ctx.Options.Sources {
		if file.Path == path && file.Library != "" {
			return fmt.Errorf("library MIDI mappings are read-only")
		}
	}
	before, ok := ctx.FileSource(path)
	if !ok {
		return fmt.Errorf("MIDI declaration is outside editable project sources")
	}
	root, walker, err := notation.ParseTree(before)
	if err != nil {
		return err
	}
	text := fmt.Sprintf("%s ch %d %s %d -> %s", in.Port, in.Channel, in.Message, in.Number, in.Target)
	var start, end int = -1, -1
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if walker.Type(n) != "midi_decl" {
			continue
		}
		end = int(n.EndByte()) - 1
		for j := 0; j < n.NamedChildCount(); j++ {
			m := n.NamedChild(j)
			if walker.Type(m) != "midi_"+in.Message || walker.Text(walker.Field(m, "port")) != in.Port {
				continue
			}
			channel, _ := strconv.Atoi(walker.Text(walker.Field(m, "channel")))
			// Note spellings can differ; use the parsed mapping's source position.
			for _, current := range score.MIDI.Mappings {
				if current.Position.File != "" && current.Position.File != path {
					continue
				}
				offset, err := Offset(before, path, current.Position)
				if err == nil && offset == int(m.StartByte()) && current.Message == in.Message && current.Channel == channel && channel == in.Channel && current.Number == in.Number {
					start, end = int(m.StartByte()), int(m.EndByte())
				}
			}
		}
		break
	}
	if end < 0 {
		return fmt.Errorf("MIDI declaration could not be located")
	}
	var after []byte
	if start >= 0 {
		after, err = ReplaceSpan(before, path, Span{Start: start, End: end, File: path, Text: text})
	} else {
		nl := Newline(before)
		after, err = ReplaceSpan(before, path, Span{Start: end, End: end, File: path, Text: nl + "  " + text + nl})
	}
	if err != nil {
		return err
	}
	ctx.Source, ctx.plan = stageProjectSource(ctx, path, before, after), nil
	ctx.SetLabel("MIDI · learned " + in.Port)
	return nil
}
