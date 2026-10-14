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
		{"kernel/params_keys.go", paramdefs.GenerateKernelKeys},
		{"kernel/params_core.go", paramdefs.GenerateKernelCore},
		{"project/params.json", paramdefs.GenerateJSON},
		{"docs/manual/parameter-ranges.md", paramdefs.GenerateRangesDoc},
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
