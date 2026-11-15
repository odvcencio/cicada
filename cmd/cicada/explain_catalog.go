package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"m31labs.dev/cicada/host/soundprobe"
	"m31labs.dev/cicada/internal/controls"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// explainControlDocument explains one control, such as fm_bass.op3_level.
type explainControlDocument struct {
	Schema  string           `json:"schema"`
	Kind    string           `json:"kind"`
	Control controls.Control `json:"control"`
	Owner   controls.Owner   `json:"owner"`
}

// explainOwnerDocument explains a voice, patch, effect kind or mixer strip.
type explainOwnerDocument struct {
	Schema   string             `json:"schema"`
	Kind     string             `json:"kind"`
	Owner    controls.Owner     `json:"owner"`
	Controls []controls.Control `json:"controls"`
}

// explainTrackDocument explains what one track of a score accepts.
type explainTrackDocument struct {
	Schema string             `json:"schema"`
	Kind   string             `json:"kind"`
	Score  string             `json:"score"`
	Track  controls.TrackView `json:"track"`
}

// explainError is an explain failure with its stable code.
type explainError struct {
	code string
	err  error
}

func (e *explainError) Error() string { return e.err.Error() }

// explainCatalog explains a control or an owner of the control catalog. It
// reports false when name is neither.
func explainCatalog(name string, asJSON bool, stdout io.Writer) (bool, error) {
	catalog := controls.All()
	if control, ok := catalog.Control(name); ok {
		owner, _ := catalog.Owner(control.Owner)
		if asJSON {
			return true, writeAgentJSON(stdout, explainControlDocument{Schema: explainSchema, Kind: "control", Control: control, Owner: owner})
		}
		return true, writeControlText(stdout, control, owner)
	}
	if owner, ok := catalog.Owner(name); ok {
		list := catalog.Of(name)
		if asJSON {
			return true, writeAgentJSON(stdout, explainOwnerDocument{Schema: explainSchema, Kind: "owner", Owner: owner, Controls: list})
		}
		fmt.Fprintf(stdout, "%s (%s): %s\n", owner.Name, owner.Kind, owner.Meaning)
		if owner.Note != "" {
			fmt.Fprintf(stdout, "note: %s\n", owner.Note)
		}
		fmt.Fprintf(stdout, "validated by: %s\nread by: %s\n\n", owner.ValidatedBy, owner.ReadBy)
		return true, writeControlTable(stdout, list)
	}
	return false, nil
}

func writeControlText(w io.Writer, c controls.Control, owner controls.Owner) error {
	fmt.Fprintf(w, "%s: %s\n", c.ID, c.Meaning)
	fmt.Fprintf(w, "type: %s", c.Type)
	if c.Unit != "" {
		fmt.Fprintf(w, "; unit: %s", c.Unit)
	}
	fmt.Fprintf(w, "; range: %s; default: %s\n", controlRange(c), defaultText(c))
	fmt.Fprintf(w, "set in: %s; live: %t\n", strings.Join(c.SetIn, ", "), c.Live)
	if c.RegistryID != "" {
		fmt.Fprintf(w, "registry: %s\n", c.RegistryID)
	}
	if c.Note != "" {
		fmt.Fprintf(w, "note: %s\n", c.Note)
	}
	fmt.Fprintf(w, "directions: %s\n", directionText(c))
	for _, d := range c.Directions {
		if len(d.Measured) == 3 {
			fmt.Fprintf(w, "  %s: %s at %s, %s, %s\n", d.Property, d.Change, measuredText(d.Measured[0], d.Unit), measuredText(d.Measured[1], d.Unit), measuredText(d.Measured[2], d.Unit))
		}
	}
	_, err := fmt.Fprintf(w, "owner: %s (%s), validated by %s, read by %s\n", owner.Name, owner.Kind, owner.ValidatedBy, owner.ReadBy)
	return err
}

func measuredText(v float64, unit string) string { return formatNumber(v) + " " + unit }

// explainTrack explains what one track of a score accepts: its voice's
// controls and its mixer strip, with current values and overrides.
func explainTrack(name, scorePath string, asJSON bool, stdout io.Writer) error {
	score, diagnostics, err := project.LoadScore(scorePath, nil)
	if err != nil {
		return &explainError{"unreadable_score", err}
	}
	if score == nil || hasDiagnosticErrors(diagnostics) {
		return &explainError{"invalid_score", diagnosticError(diagnostics)}
	}
	view, err := controls.ForTrack(score, name)
	if err != nil {
		names := make([]string, 0, len(score.Tracks))
		for _, track := range score.Tracks {
			names = append(names, track.Name)
		}
		suggestion := notation.AudioReferenceMessage("track", name, names)
		if len(names) > 0 && !strings.Contains(suggestion, "did you mean") {
			suggestion += fmt.Sprintf("; available track: %q", names[0])
		}
		return &explainError{"unknown_track", fmt.Errorf("%w; %s", err, suggestion)}
	}
	if asJSON {
		return writeAgentJSON(stdout, explainTrackDocument{Schema: explainSchema, Kind: "track", Score: jsonPath(scorePath), Track: view})
	}
	switch {
	case view.OwnerKind == controls.KindAuthored:
		fmt.Fprintf(stdout, "track %s plays %s (instrument graph %s)\n", view.Track, view.Kind, view.Instrument)
	default:
		fmt.Fprintf(stdout, "track %s plays %s (%s %s)\n", view.Track, view.Kind, view.OwnerKind, view.Owner)
	}
	table := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "CONTROL\tVALUE\tFROM\tRANGE\tDIRECTIONS\tMEANING")
	for _, c := range view.Controls {
		value := fmt.Sprint(c.Value)
		if c.Written != "" {
			value = c.Written
		} else if c.Value == nil {
			value = "—"
		} else if number, ok := c.Value.(float64); ok {
			value = controls.Literal(c.Control, number)
		}
		name := c.Name
		if c.Owner == "mix" {
			name = "mix " + name
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\n", name, value, c.ValueSource, controlRange(c.Control), directionText(c.Control), c.Meaning)
	}
	return table.Flush()
}

// explainCommand runs cicada explain. A track:<name> with a score explains
// that track; a control or owner of the catalog explains it; any other name
// is a language construct or graph function.
func explainCommand(args []string, stdout io.Writer) error {
	asJSON, probe := hasJSONFlag(args), false
	var rest []string
	for _, arg := range args {
		switch arg {
		case "--json", "--json=true":
		case "--probe":
			probe = true
		default:
			rest = append(rest, arg)
		}
	}
	var err error
	if probe {
		err = explainProbe(rest, asJSON, stdout)
	} else {
		err = explainRun(rest, asJSON, stdout)
	}
	if err != nil && asJSON {
		code := "explain_failed"
		var coded *explainError
		if errors.As(err, &coded) {
			code = coded.code
		}
		if writeErr := writeAgentJSON(stdout, jsonErrorDocument{Schema: explainSchema, Error: jsonError{Code: code, Message: err.Error()}}); writeErr != nil {
			return writeErr
		}
	}
	return err
}

const explainUsage = "usage: cicada explain <construct[.field] | control | voice | patch | effect> [--json] [--probe] | cicada explain track:<name> <score.cicada> [--json] | cicada explain <score.cicada> <path> [@bar[.beat[.step]]]"

func explainRun(args []string, asJSON bool, stdout io.Writer) error {
	var track, scorePath string
	var others []string
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "track:"):
			track = strings.TrimPrefix(arg, "track:")
		case strings.HasSuffix(arg, ".cicada") && scorePath == "":
			scorePath = arg
		default:
			others = append(others, arg)
		}
	}
	if track != "" {
		if scorePath == "" || len(others) != 0 {
			return &explainError{"usage", errors.New(explainUsage)}
		}
		return explainTrack(track, scorePath, asJSON, stdout)
	}
	if scorePath != "" {
		if asJSON || len(others) < 1 || len(others) > 2 || len(others) == 2 && !strings.HasPrefix(others[1], "@") {
			return &explainError{"usage", errors.New(explainUsage)}
		}
		location := ""
		if len(others) == 2 {
			location = others[1]
		}
		return explainParameter(scorePath, others[0], location, stdout)
	}
	if len(others) != 1 {
		return &explainError{"usage", errors.New(explainUsage)}
	}
	name := others[0]
	// A name may refer to both a language construct and a catalog entry.
	var construct bytes.Buffer
	constructErr := explain(&construct, name, asJSON)
	var catalog bytes.Buffer
	handled, catalogErr := explainCatalog(name, asJSON, &catalog)
	if catalogErr != nil {
		return catalogErr
	}
	if constructErr == nil && handled {
		if !asJSON {
			_, err := fmt.Fprintf(stdout, "%s\n%s", construct.String(), catalog.String())
			return err
		}
		var doc, entry map[string]any
		if err := json.Unmarshal(construct.Bytes(), &doc); err != nil {
			return err
		}
		if err := json.Unmarshal(catalog.Bytes(), &entry); err != nil {
			return err
		}
		delete(entry, "schema")
		doc["catalog"] = entry
		return writeExplainJSON(stdout, doc)
	}
	if constructErr == nil {
		_, err := stdout.Write(construct.Bytes())
		return err
	}
	if handled {
		_, err := stdout.Write(catalog.Bytes())
		return err
	}
	if ownerName, _, ok := strings.Cut(name, "."); ok {
		if _, found := controls.All().Owner(ownerName); found {
			return &explainError{"unknown_thing", fmt.Errorf("owner %q exists but has no control %q; %s", ownerName, name, catalogSuggestion(name, false))}
		}
	}
	return &explainError{"unknown_thing", fmt.Errorf("%w; %s", constructErr, catalogSuggestion(name, false))}

}

// probeSchema names the explain --probe --json document.
const probeSchema = "cicada.probe.v1"

// probeDocument is what explain --probe --json prints: each control measured
// now, beside what the checked-in table holds.
type probeDocument struct {
	Schema   string            `json:"schema"`
	Phrase   map[string]string `json:"phrase"`
	Method   string            `json:"method"`
	Controls []probedJSON      `json:"controls"`
}

type probedJSON struct {
	ID string `json:"id"`
	controls.ProbedControl
	Directions []controls.Direction `json:"directions"`
	// MatchesTable is false when the measurement differs from
	// internal/controls/directions.json, which then needs regenerating.
	MatchesTable bool `json:"matches_table"`
}

// explainProbe measures a control, or every control of an owner the probe
// can measure, by rendering the probe phrase at low, default and high values.
func explainProbe(args []string, asJSON bool, stdout io.Writer) error {
	if len(args) != 1 {
		return &explainError{"usage", errors.New(explainUsage)}
	}
	catalog := controls.All()
	var ids []string
	if c, ok := catalog.Control(args[0]); ok {
		owner, _ := catalog.Owner(c.Owner)
		if soundprobe.Probeable(owner, c) {
			ids = append(ids, c.ID)
		}
	} else if owner, ok := catalog.Owner(args[0]); ok {
		for _, c := range catalog.Of(owner.Name) {
			if soundprobe.Probeable(owner, c) {
				ids = append(ids, c.ID)
			}
		}
	} else {
		return &explainError{"unknown_thing", fmt.Errorf("no control or owner %q; %s", args[0], catalogSuggestion(args[0], true))}
	}
	if len(ids) == 0 {
		return &explainError{"not_probeable", fmt.Errorf("the probe measures numeric controls of voices, keyboard patches, library patches and kit lanes; %s has none; %s; for example fm_bass.gain", args[0], catalogSuggestion(args[0], true))}
	}
	table := controls.Measured()
	document := probeDocument{Schema: probeSchema, Phrase: soundprobe.Phrases, Method: soundprobe.Method, Controls: []probedJSON{}}
	for _, result := range soundprobe.ProbeAll(ids, 0) {
		if result.Err != nil {
			return &explainError{"probe_failed", result.Err}
		}
		held, ok := table.Controls[result.ID]
		document.Controls = append(document.Controls, probedJSON{ID: result.ID, ProbedControl: result.Probe, Directions: result.Probe.Directions(), MatchesTable: ok && soundprobe.Same(result.Probe, held)})
	}
	if asJSON {
		return writeAgentJSON(stdout, document)
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "CONTROL\tVALUES\tLEVEL dB\tCENTROID Hz\tATTACK ms\tDECAY ms\tMATCHES TABLE")
	for _, c := range document.Controls {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%t\n", c.ID, numbers(c.At), numbers(c.LevelDB), numbers(c.CentroidHz), numbers(c.AttackMS), numbers(c.DecayMS), c.MatchesTable)
	}
	return w.Flush()
}

func numbers(values []float64) string {
	var parts []string
	for _, v := range values {
		parts = append(parts, formatNumber(v))
	}
	return strings.Join(parts, " / ")
}

// catalogSuggestion reuses the spelling suggestions used for score diagnostics.
func catalogSuggestion(name string, probeable bool) string {
	catalog := controls.All()
	var names []string
	for _, c := range catalog.Controls {
		owner, _ := catalog.Owner(c.Owner)
		if !probeable || soundprobe.Probeable(owner, c) {
			names = append(names, c.ID)
		}
	}
	for _, owner := range catalog.Owners {
		if !probeable {
			names = append(names, owner.Name)
			continue
		}
		for _, c := range catalog.Of(owner.Name) {
			if soundprobe.Probeable(owner, c) {
				names = append(names, owner.Name)
				break
			}
		}
	}
	if !probeable {
		if fields, err := project.Fields(); err == nil {
			for _, construct := range fields.Constructs {
				names = append(names, construct.Name)
			}
			for _, field := range fields.Fields {
				names = append(names, field.Construct+"."+field.Name)
			}
		}
	}
	suggestion := notation.AudioReferenceMessage("control or owner", name, names)
	if len(names) > 0 && !strings.Contains(suggestion, "did you mean") {
		suggestion += fmt.Sprintf("; available name: %q", names[0])
	}
	return suggestion
}
