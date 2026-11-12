package main

import (
	"fmt"
	"io"
	"strings"

	"m31labs.dev/cicada/internal/diagnostics"
)

const diagnosticsUsage = "usage: cicada diagnostics [--json] [code]"

// diagnosticsPrefix starts every diagnostic code; cicada diagnostics adds it
// to a code typed without it.
const diagnosticsPrefix = "CICADA-"

// diagnosticsSchema names the format of cicada diagnostics --json.
const diagnosticsSchema = "cicada.diagnostics.v1"

type diagnosticsDocument struct {
	Schema      string             `json:"schema"`
	Diagnostics []diagnostics.Code `json:"diagnostics"`
}

// diagnosticsFor is the cicada.diagnostics.v1 document of one code (with or
// without its CICADA- prefix, in any case), or of every code when name is
// empty. For a code the catalog lacks it returns unknown_code and the error.
func diagnosticsFor(name string) (diagnosticsDocument, string, error) {
	if name == "" {
		return diagnosticsDocument{Schema: diagnosticsSchema, Diagnostics: diagnostics.Catalog()}, "", nil
	}
	code := strings.ToUpper(name)
	if !strings.HasPrefix(code, diagnosticsPrefix) {
		code = diagnosticsPrefix + code
	}
	entry, ok := diagnostics.Lookup(code)
	if !ok {
		return diagnosticsDocument{}, "unknown_code", fmt.Errorf("unknown diagnostic code %q; run cicada diagnostics for the list", name)
	}
	return diagnosticsDocument{Schema: diagnosticsSchema, Diagnostics: []diagnostics.Code{entry}}, "", nil
}

// diagnosticsCommand prints the catalog of diagnostic codes, or one code.
func diagnosticsCommand(args []string, stdout io.Writer) error {
	asJSON := hasJSONFlag(args)
	fail := func(code string, err error) error {
		if asJSON {
			if writeErr := writeAgentJSON(stdout, jsonErrorDocument{Schema: diagnosticsSchema, Error: jsonError{Code: code, Message: err.Error()}}); writeErr != nil {
				return writeErr
			}
		}
		return err
	}
	var names []string
	for _, arg := range args {
		switch {
		case arg == "--json":
		case strings.HasPrefix(arg, "-"):
			return fail("usage", fmt.Errorf("%s", diagnosticsUsage))
		default:
			names = append(names, arg)
		}
	}
	if len(names) > 1 {
		return fail("usage", fmt.Errorf("%s", diagnosticsUsage))
	}
	name := ""
	if len(names) == 1 {
		name = names[0]
	}
	document, code, err := diagnosticsFor(name)
	if err != nil {
		return fail(code, err)
	}
	catalog := document.Diagnostics
	if asJSON {
		data, err := marshalAgentJSON(document)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, string(data))
		return err
	}
	if len(names) == 1 {
		entry := catalog[0]
		fmt.Fprintf(stdout, "%s (%s; reported by: %s)\n\n%s.\n\nFix: %s.\n", entry.Code, strings.Join(entry.Severities, " or "), strings.Join(entry.Scopes, ", "), capitalize(entry.Meaning), entry.Fix)
		if len(entry.Reasons) > 0 {
			fmt.Fprintln(stdout, "\nReasons:")
			for _, reason := range entry.Reasons {
				fmt.Fprintf(stdout, "  %-20s %s\n", reason.Reason, reason.Meaning)
			}
		}
		return nil
	}
	width := 0
	for _, entry := range catalog {
		width = max(width, len(entry.Code))
	}
	fmt.Fprintf(stdout, "Diagnostic codes (%d). Run \"cicada diagnostics <code>\" for the fix, or add --json.\n\n", len(catalog))
	for _, entry := range catalog {
		fmt.Fprintf(stdout, "  %-*s  %-13s  %s\n", width, entry.Code, strings.Join(entry.Severities, "/"), entry.Meaning)
	}
	return nil
}

func capitalize(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}
