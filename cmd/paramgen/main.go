package main

import (
	"fmt"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/internal/paramdefs"
)

func main() {
	outputs := []struct {
		path string
		make func() ([]byte, error)
	}{
		{"kernel/params_table.go", paramdefs.GenerateKernel},
		{"project/params.json", paramdefs.GenerateJSON},
	}
	for _, output := range outputs {
		data, err := output.make()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := os.MkdirAll(filepath.Dir(output.path), 0755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := os.WriteFile(output.path, data, 0644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
