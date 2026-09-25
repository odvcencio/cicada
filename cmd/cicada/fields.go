package main

import (
	"fmt"
	"strings"

	"m31labs.dev/cicada/project"
)

func explainField(name string) error {
	parts := strings.Split(name, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("expected construct.field, such as pattern.steps")
	}
	catalog, err := project.Fields()
	if err != nil {
		return err
	}
	for _, field := range catalog.Fields {
		if field.Construct == parts[0] && field.Name == parts[1] {
			fmt.Printf("%s.%s: %s\nType: %s\n", field.Construct, field.Name, field.Meaning, field.Type)
			if field.Unit != "" {
				fmt.Printf("Unit: %s\n", field.Unit)
			}
			if field.Range != "" {
				fmt.Printf("Range: %s\n", field.Range)
			}
			if field.Default != "" {
				fmt.Printf("Default: %s\n", field.Default)
			}
			return nil
		}
	}
	return fmt.Errorf("unknown semantic field %q", name)
}
