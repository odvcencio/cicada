package main

import (
	"fmt"
	"io"
	"strings"

	edits "m31labs.dev/cicada/edit"
)

const intentsUsage = "usage: cicada intents [--json] [--index | kind]"

// intentsSchema names the format of cicada intents --json.
const intentsSchema = "cicada.intents.v1"

// intentsIndexSchema names the format of cicada intents --json --index, a
// document of its own: it has none of the catalog's fields.
const intentsIndexSchema = "cicada.intents_index.v1"

// intentsIndexUsage is the refusal of --index without --json: the plain list
// already gives each kind and its meaning.
const intentsIndexUsage = "usage: cicada intents --json --index (--index needs --json; cicada intents alone lists every kind and its meaning)"

type intentsDocument struct {
	Schema          string              `json:"schema"`
	EnvelopeVersion int                 `json:"envelope_version"`
	Envelope        []edits.IntentField `json:"envelope"`
	Intents         []edits.IntentInfo  `json:"intents"`
}

// intentsIndexDocument is cicada intents --json --index: each kind with its
// one-line meaning and nothing else, so an agent can scan the kinds before
// it asks for one in full.
type intentsIndexDocument struct {
	Schema          string             `json:"schema"`
	EnvelopeVersion int                `json:"envelope_version"`
	Index           []intentIndexEntry `json:"index"`
}

type intentIndexEntry struct {
	Kind    string `json:"kind"`
	Meaning string `json:"meaning"`
}

// intentsIndex is the cicada.intents_index.v1 document: every kind with its
// one-line meaning.
func intentsIndex() intentsIndexDocument {
	catalog := edits.Catalog()
	document := intentsIndexDocument{Schema: intentsIndexSchema, EnvelopeVersion: edits.EnvelopeVersion, Index: make([]intentIndexEntry, 0, len(catalog))}
	for _, info := range catalog {
		document.Index = append(document.Index, intentIndexEntry{Kind: info.Kind, Meaning: info.Meaning})
	}
	return document
}

// intentsFor is the cicada.intents.v1 document of the named kinds, in the
// order given, or of every kind when none is named. For a kind the catalog
// lacks it returns the code unknown_kind and the error.
func intentsFor(kinds []string) (intentsDocument, string, error) {
	document := intentsDocument{Schema: intentsSchema, EnvelopeVersion: edits.EnvelopeVersion, Envelope: edits.EnvelopeFields(), Intents: edits.Catalog()}
	if len(kinds) == 0 {
		return document, "", nil
	}
	document.Intents = make([]edits.IntentInfo, 0, len(kinds))
	for _, kind := range kinds {
		info, ok := edits.DescribeIntent(kind)
		if !ok {
			return intentsDocument{}, "unknown_kind", fmt.Errorf("unknown intent kind %q; run cicada intents for the list", kind)
		}
		document.Intents = append(document.Intents, info)
	}
	return document, "", nil
}

// intentsCommand prints the catalog of the edit intents cicada apply accepts,
// or one kind of it. The catalog comes from the intent registry.
func intentsCommand(args []string, stdout io.Writer) error {
	asJSON := hasJSONFlag(args)
	schema := intentsSchema
	for _, arg := range args {
		if arg == "--index" {
			schema = intentsIndexSchema
		}
	}
	fail := func(code string, err error) error {
		if asJSON {
			if writeErr := writeAgentJSON(stdout, jsonErrorDocument{Schema: schema, Error: jsonError{Code: code, Message: err.Error()}}); writeErr != nil {
				return writeErr
			}
		}
		return err
	}
	var kinds []string
	index := false
	for _, arg := range args {
		switch {
		case arg == "--json":
		case arg == "--index":
			index = true
		case strings.HasPrefix(arg, "-"):
			return fail("usage", fmt.Errorf("%s", intentsUsage))
		default:
			kinds = append(kinds, arg)
		}
	}
	if len(kinds) > 1 || index && len(kinds) != 0 {
		return fail("usage", fmt.Errorf("%s", intentsUsage))
	}
	if index && !asJSON {
		return fail("usage", fmt.Errorf("%s", intentsIndexUsage))
	}
	if index {
		return writeAgentJSON(stdout, intentsIndex())
	}
	document, code, err := intentsFor(kinds)
	if err != nil {
		return fail(code, err)
	}
	catalog := document.Intents
	if asJSON {
		data, err := marshalAgentJSON(document)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, string(data))
		return err
	}
	if len(kinds) == 1 {
		return writeIntentText(stdout, catalog[0])
	}
	width := 0
	for _, info := range catalog {
		width = max(width, len(info.Kind))
	}
	fmt.Fprintf(stdout, "Edit intents for cicada apply (%d). Run \"cicada intents <kind>\" for its fields and an example, or add --json.\n\n", len(catalog))
	for _, info := range catalog {
		fmt.Fprintf(stdout, "  %-*s  %s\n", width, info.Kind, info.Meaning)
	}
	_, err = fmt.Fprintln(stdout, "\nApply them with: cicada apply <score.cicada> <intents.json> [--write], where intents.json is {\"version\":1,\"intents\":[...]}.")
	return err
}

func writeIntentText(w io.Writer, info edits.IntentInfo) error {
	fmt.Fprintf(w, "%s: %s\n", info.Kind, info.Meaning)
	if info.Notes != "" {
		fmt.Fprintf(w, "\n%s\n", info.Notes)
	}
	fmt.Fprintln(w, "\nFields:")
	if len(info.Fields) == 0 {
		fmt.Fprintln(w, "  (none)")
	}
	writeIntentFields(w, info.Fields, "  ")
	_, err := fmt.Fprintf(w, "\nExample:\n  %s\n", info.Example)
	return err
}

func writeIntentFields(w io.Writer, fields []edits.IntentField, indent string) {
	for _, field := range fields {
		kind := field.Type
		if field.Items != "" {
			kind += " of " + field.Items
		}
		need := "optional"
		if field.Required {
			need = "required"
		}
		var details []string
		if field.Unit != "" {
			details = append(details, "unit "+field.Unit)
		}
		if len(field.Values) > 0 {
			details = append(details, "one of "+strings.Join(field.Values, ", "))
		}
		if field.Meaning != "" {
			details = append(details, field.Meaning)
		}
		line := fmt.Sprintf("%s%s (%s, %s)", indent, field.Name, kind, need)
		if len(details) > 0 {
			line += ": " + strings.Join(details, "; ")
		}
		fmt.Fprintln(w, line)
		writeIntentFields(w, field.Fields, indent+"  ")
	}
}
