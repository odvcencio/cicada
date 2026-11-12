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

// paramsSchema names the params --json document: the controls a filter
// selects. paramsIndexSchema names params --json --index: the owners only.
const (
	paramsSchema      = "cicada.params.v1"
	paramsIndexSchema = "cicada.params_index.v1"
)

// paramsDocument is what params --json prints: every control of every voice,
// patch, effect and mixer strip, or those the filter selects.
type paramsDocument struct {
	Schema   string             `json:"schema"`
	Filter   controls.Query     `json:"filter"`
	Counts   controls.Counts    `json:"counts"`
	Owners   []controls.Owner   `json:"owners"`
	Controls []controls.Control `json:"controls"`
}

// paramsIndexDocument is what params --json --index prints: every owner of
// controls with its kind, family and number of controls, and the values the
// voice and scope filters take.
type paramsIndexDocument struct {
	Schema   string           `json:"schema"`
	Scopes   []string         `json:"scopes"`
	Families []string         `json:"families"`
	Owners   []controls.Owner `json:"owners"`
}

const paramsUsage = "usage: cicada params [--json] [--index | [--voice <voice>] [--scope <kind>] [<control>]]"

// paramsError is a params failure with its stable code: usage, or no_match
// when the filter selects nothing.
type paramsError struct {
	code string
	err  error
}

func (e *paramsError) Error() string { return e.err.Error() }

// paramsRequest is what one params call asks for. The command and the MCP
// params tool both build one and pass it to paramsResult.
type paramsRequest struct {
	index bool
	query controls.Query
}

// paramsResult returns the document params --json prints for request: the
// index, or the controls its query selects.
func paramsResult(request paramsRequest) (any, error) {
	catalog := controls.All()
	if request.index {
		if request.query != (controls.Query{}) {
			return nil, &paramsError{"usage", errors.New("--index takes no filter; " + paramsUsage)}
		}
		return paramsIndexDocument{Schema: paramsIndexSchema, Scopes: controls.Scopes, Families: catalog.Families(), Owners: catalog.Owners}, nil
	}
	selected, err := catalog.Select(request.query)
	if err != nil {
		var query *controls.QueryError
		if errors.As(err, &query) {
			return nil, &paramsError{query.Code, err}
		}
		return nil, err
	}
	return paramsDocument{Schema: paramsSchema, Filter: request.query, Counts: selected.Count(), Owners: selected.Owners, Controls: selected.Controls}, nil
}

// paramsCommand prints the control catalog. With no arguments it prints the
// live-parameter registry as it always has. --json prints the controls as one
// document, --index the owners only, and the filters keep the controls of one
// voice or family (--voice), one owner kind (--scope) or whose ID starts with
// <control>, such as fm_bass.op3.
func paramsCommand(args []string, stdout io.Writer) error {
	asJSON := hasJSONFlag(args)
	var request paramsRequest
	usage := func(message string) error {
		return paramsFailure(stdout, asJSON, &paramsError{"usage", errors.New(message)})
	}
	if len(args) == 0 {
		_, err := fmt.Fprintln(stdout, project.ParamsJSON())
		return err
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json" || arg == "--json=true":
		case arg == "--index":
			request.index = true
		case arg == "--voice" || arg == "--scope":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return usage(arg + " needs a value; " + paramsUsage)
			}
			if arg == "--voice" {
				request.query.Voice = args[i+1]
			} else {
				request.query.Scope = args[i+1]
			}
			i++
		case strings.HasPrefix(arg, "-"):
			return usage(paramsUsage)
		case request.query.Control != "":
			return usage(paramsUsage)
		default:
			request.query.Control = arg
		}
	}
	document, err := paramsResult(request)
	if err != nil {
		return paramsFailure(stdout, asJSON, err)
	}
	if asJSON {
		return writeAgentJSON(stdout, document)
	}
	switch document := document.(type) {
	case paramsIndexDocument:
		return writeOwnerTable(stdout, document.Owners)
	case paramsDocument:
		return writeControlTable(stdout, document.Controls)
	}
	return nil
}

func paramsFailure(stdout io.Writer, asJSON bool, err error) error {
	if asJSON {
		code := "params_failed"
		var coded *paramsError
		if errors.As(err, &coded) {
			code = coded.code
		}
		if writeErr := writeAgentJSON(stdout, jsonErrorDocument{Schema: paramsSchema, Error: jsonError{Code: code, Message: err.Error()}}); writeErr != nil {
			return writeErr
		}
	}
	return err
}

// writeOwnerTable prints owners as text, one per line.
func writeOwnerTable(w io.Writer, owners []controls.Owner) error {
	table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "OWNER\tKIND\tFAMILY\tCONTROLS\tMEANING")
	for _, owner := range owners {
		family := owner.Family
		if family == "" {
			family = "—"
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%d\t%s\n", owner.Name, owner.Kind, family, owner.Controls, owner.Meaning)
	}
	return table.Flush()
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
