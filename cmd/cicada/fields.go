package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/project"
)

// explainSchema names the format of cicada explain <construct> --json.
const explainSchema = "cicada.explain.v1"

// explain prints a language construct, one of its fields, or a graph
// operation (graph.<name>) to w, as text or as a cicada.explain.v1 document.
func explain(w io.Writer, name string, asJSON bool) error {
	if strings.HasPrefix(name, "graph.") {
		if operation, ok := instrument.Operation(strings.TrimPrefix(name, "graph.")); ok {
			if asJSON {
				return writeExplainJSON(w, struct {
					Schema string `json:"schema"`
					instrument.OperationInfo
				}{explainSchema, operation})
			}
			fmt.Fprintf(w, "%s\n%s\n", operation.Signature, operation.Meaning)
			return nil
		}
	}
	parts := strings.Split(name, ".")
	if len(parts) > 2 || parts[0] == "" || (len(parts) == 2 && parts[1] == "") {
		return fmt.Errorf("expected construct or construct.field, such as pattern or pattern.steps")
	}
	catalog, err := project.Fields()
	if err != nil {
		return err
	}
	for _, construct := range catalog.Constructs {
		if construct.Name != parts[0] {
			continue
		}
		fields := []project.Field{}
		for _, field := range catalog.Fields {
			if field.Construct == construct.Name && (len(parts) == 1 || field.Name == parts[1]) {
				fields = append(fields, field)
			}
		}
		if len(fields) == 0 {
			return fmt.Errorf("unknown semantic field %q", name)
		}
		if asJSON {
			if len(parts) == 2 {
				return writeExplainJSON(w, struct {
					Schema string `json:"schema"`
					project.Field
				}{explainSchema, fields[0]})
			}
			return writeExplainJSON(w, struct {
				Schema    string                  `json:"schema"`
				Construct project.ConstructRecord `json:"construct"`
				Fields    []project.Field         `json:"fields"`
			}{explainSchema, construct, fields})
		}
		if len(parts) == 2 {
			field := fields[0]
			fmt.Fprintf(w, "%s.%s: %s\nType: %s\n", field.Construct, field.Name, field.Meaning, field.Type)
			if field.Unit != nil {
				fmt.Fprintf(w, "Unit: %s\n", *field.Unit)
			}
			if field.Range != nil {
				fmt.Fprintf(w, "Range: %s\n", *field.Range)
			}
			if field.Variant != "" {
				fmt.Fprintf(w, "Variant: %s\n", field.Variant)
			}
			return nil
		}
		fmt.Fprintf(w, "%s (%s, %s)\n", construct.Name, construct.Layer, construct.Profile)
		table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		fmt.Fprintln(table, "FIELD\tTYPE\tUNIT\tRANGE\tREQUIRED\tDEFAULT\tMEANING")
		for _, field := range fields {
			fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%t\t%s\t%s\n", field.Name, field.Type, optionalText(field.Unit), optionalText(field.Range), field.Required, optionalText(field.Default), field.Meaning)
		}
		return table.Flush()
	}
	return fmt.Errorf("unknown semantic construct %q", parts[0])
}

// writeExplainJSON writes one explain document, indented as it always was.
func writeExplainJSON(w io.Writer, document any) error {
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

func optionalText(value *string) string {
	if value == nil {
		return "—"
	}
	return *value
}
