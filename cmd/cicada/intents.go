package main

import (
	"fmt"
	"io"
	"strings"

	edits "m31labs.dev/cicada/edit"
)

const intentsUsage = "usage: cicada intents [--json] [kind]"

// intentsSchema names the format of cicada intents --json.
const intentsSchema = "cicada.intents.v1"

type intentsDocument struct {
	Schema          string              `json:"schema"`
	EnvelopeVersion int                 `json:"envelope_version"`
	Envelope        []edits.IntentField `json:"envelope"`
	Intents         []edits.IntentInfo  `json:"intents"`
}

// intentsCommand prints the catalog of the edit intents cicada apply accepts,
// or one kind of it. The catalog comes from the intent registry.
func intentsCommand(args []string, stdout io.Writer) error {
	asJSON := false
	var kinds []string
	for _, arg := range args {
		switch {
		case arg == "--json":
			asJSON = true
		case strings.HasPrefix(arg, "-"):
			return fmt.Errorf("%s", intentsUsage)
		default:
			kinds = append(kinds, arg)
		}
	}
	if len(kinds) > 1 {
		return fmt.Errorf("%s", intentsUsage)
	}
	catalog := edits.Catalog()
	if len(kinds) == 1 {
		info, ok := edits.DescribeIntent(kinds[0])
		if !ok {
			return fmt.Errorf("unknown intent kind %q; run cicada intents for the list", kinds[0])
		}
		catalog = []edits.IntentInfo{info}
	}
	if asJSON {
		data, err := marshalAgentJSON(intentsDocument{Schema: intentsSchema, EnvelopeVersion: edits.EnvelopeVersion, Envelope: edits.EnvelopeFields(), Intents: catalog})
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
	_, err := fmt.Fprintln(stdout, "\nApply them with: cicada apply <score.cicada> <intents.json> [--write], where intents.json is {\"version\":1,\"intents\":[...]}.")
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
