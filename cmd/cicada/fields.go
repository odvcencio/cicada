package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"m31labs.dev/cicada/project"
)

func explain(name string, asJSON bool) error {
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
			var value any = fields[0]
			if len(parts) == 1 {
				value = struct {
					Construct project.ConstructRecord `json:"construct"`
					Fields    []project.Field         `json:"fields"`
				}{construct, fields}
			}
			data, err := json.MarshalIndent(value, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(data))
			return nil
		}
		if len(parts) == 2 {
			field := fields[0]
			fmt.Printf("%s.%s: %s\nType: %s\n", field.Construct, field.Name, field.Meaning, field.Type)
			if field.Unit != nil {
				fmt.Printf("Unit: %s\n", *field.Unit)
			}
			if field.Range != nil {
				fmt.Printf("Range: %s\n", *field.Range)
			}
			if field.Variant != "" {
				fmt.Printf("Variant: %s\n", field.Variant)
			}
			return nil
		}
		fmt.Printf("%s (%s, %s)\n", construct.Name, construct.Layer, construct.Profile)
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "FIELD\tTYPE\tUNIT\tRANGE\tREQUIRED\tDEFAULT\tMEANING")
		for _, field := range fields {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%t\t%s\t%s\n", field.Name, field.Type, optionalText(field.Unit), optionalText(field.Range), field.Required, optionalText(field.Default), field.Meaning)
		}
		return w.Flush()
	}
	return fmt.Errorf("unknown semantic construct %q", parts[0])
}

func optionalText(value *string) string {
	if value == nil {
		return "—"
	}
	return *value
}
