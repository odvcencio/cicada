package main

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"m31labs.dev/cicada/internal/controls"
	"m31labs.dev/cicada/project"
)

// paramsSchema names the params --json document.
const paramsSchema = "cicada.params.v1"

// paramsDocument is what params --json prints: every control of every voice,
// patch, effect and mixer strip, or those a filter selects.
type paramsDocument struct {
	Schema   string             `json:"schema"`
	Filter   string             `json:"filter"`
	Counts   controls.Counts    `json:"counts"`
	Owners   []controls.Owner   `json:"owners"`
	Controls []controls.Control `json:"controls"`
}

const paramsUsage = "usage: cicada params [<filter>] [--json]"

// paramsCommand prints the control catalog. With no arguments it prints the
// parameter registry as it always has; --json prints the whole catalog as one
// document, and a filter keeps the controls whose ID starts with it, such as
// fm_bass or fm_bass.op3.
func paramsCommand(args []string, stdout io.Writer) error {
	asJSON, filter := false, ""
	for _, arg := range args {
		switch {
		case arg == "--json" || arg == "--json=true":
			asJSON = true
		case strings.HasPrefix(arg, "-"):
			return paramsFailure(stdout, asJSON || hasJSONFlag(args), "usage", errors.New(paramsUsage))
		case filter != "":
			return paramsFailure(stdout, asJSON || hasJSONFlag(args), "usage", errors.New(paramsUsage))
		default:
			filter = arg
		}
	}
	if !asJSON && filter == "" {
		_, err := fmt.Fprintln(stdout, project.ParamsJSON())
		return err
	}
	selected := controls.All().Filter(filter)
	if len(selected.Controls) == 0 {
		return paramsFailure(stdout, asJSON, "no_match", fmt.Errorf("no control or owner matches %q; cicada params --json lists them all", filter))
	}
	if asJSON {
		return writeAgentJSON(stdout, paramsDocument{Schema: paramsSchema, Filter: filter, Counts: selected.Count(), Owners: selected.Owners, Controls: selected.Controls})
	}
	return writeControlTable(stdout, selected.Controls)
}

func paramsFailure(stdout io.Writer, asJSON bool, code string, err error) error {
	if asJSON {
		if writeErr := writeAgentJSON(stdout, jsonErrorDocument{Schema: paramsSchema, Error: jsonError{Code: code, Message: err.Error()}}); writeErr != nil {
			return writeErr
		}
	}
	return err
}

// writeControlTable prints controls as text, one per line.
func writeControlTable(w io.Writer, list []controls.Control) error {
	table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "CONTROL\tTYPE\tRANGE\tDEFAULT\tSET IN\tDIRECTIONS\tMEANING")
	for _, c := range list {
		meaning := c.Meaning
		if c.Refused {
			meaning = "(refused today) " + meaning
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", c.ID, c.Type, controlRange(c), defaultText(c), strings.Join(c.SetIn, ","), directionText(c), meaning)
	}
	return table.Flush()
}

// defaultText writes a control's default in source form, or a dash when it
// has no single default.
func defaultText(c controls.Control) string {
	switch v := c.Default.(type) {
	case float64:
		return controls.Literal(c, v)
	case string:
		return v
	}
	return "—"
}

func controlRange(c controls.Control) string {
	var parts []string
	if c.Min != nil && c.Max != nil {
		parts = append(parts, controls.Literal(c, *c.Min)+".."+controls.Literal(c, *c.Max))
	}
	if len(c.Values) > 0 {
		parts = append(parts, strings.Join(c.Values, "|"))
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, " or ")
}

func directionText(c controls.Control) string {
	var parts []string
	for _, d := range c.Directions {
		if d.Change == controls.ChangeNone {
			continue
		}
		parts = append(parts, d.Property+" "+d.Change+" ("+d.Basis+")")
	}
	if len(parts) == 0 {
		if controls.DirectionBasis(c) == controls.BasisMeasured {
			return "no measured change"
		}
		return "unknown"
	}
	return strings.Join(parts, ", ")
}

func formatNumber(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
